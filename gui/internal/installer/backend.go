package installer

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/steps"
)

// backend is what the wizard needs from the machine. The real backend probes
// disks and images and runs firn's pipeline in-process; the demo backend
// simulates all of it without touching a disk.
type backend interface {
	// Disks lists whole-disk targets with the reason firn refuses each, if
	// any.
	Disks(ctx context.Context) ([]diskInfo, error)
	// InspectCore reads the core Flatpak set ref publishes (ADR-0018), for
	// display only: preflight reads the label again at install.
	InspectCore(ctx context.Context, ref string) corePreview
	// Install runs the pipeline for a validated recipe, reporting every
	// event to emit and ending with exactly one terminal event.
	Install(ctx context.Context, l *recipe.Loaded, emit progress.Emitter) error
	// Reboot restarts the machine after a successful install.
	Reboot() error
}

// machineInfo is what the wizard knows about the install machine.
type machineInfo struct {
	env     recipe.Env
	uefi    bool
	version string
	demo    bool
}

// diskInfo is one candidate target disk.
type diskInfo struct {
	disk.Device
	// refused is why firn will not install here; empty when it will.
	refused string
}

// title names the disk the way people know it: its vendor and model.
func (d diskInfo) title() string {
	name := strings.TrimSpace(strings.Join(strings.Fields(d.Vendor+" "+d.Model), " "))
	if name == "" {
		return d.Path
	}
	return name
}

// corePreviewState is what the apps page knows about the chosen image's core
// Flatpak set. The zero value, unavailable, keeps the toggle: preflight reads
// the label again either way.
type corePreviewState int

const (
	corePreviewUnavailable corePreviewState = iota // inspect failed or not attempted
	corePreviewNone                                // image publishes no core set
	corePreviewInvalid                             // label present but malformed
	corePreviewAvailable                           // valid, non-empty set
)

// corePreview describes the core set of the image whose ref it records.
type corePreview struct {
	ref    string
	state  corePreviewState
	apps   []flatpak.CoreApp
	detail string
}

// corePreviewTimeout bounds the image inspection, so an unreachable registry
// delays the preview instead of hanging it.
const corePreviewTimeout = 30 * time.Second

// realBackend drives the install machine.
type realBackend struct {
	machine machineInfo
	runner  *runner.Runner
}

func (b *realBackend) Disks(ctx context.Context) ([]diskInfo, error) {
	devices, err := disk.List(ctx, b.runner)
	if err != nil {
		return nil, err
	}
	rootDev := disk.RootDevice(ctx, b.runner)
	out := make([]diskInfo, 0, len(devices))
	for _, d := range devices {
		out = append(out, diskInfo{Device: d, refused: disk.RefusalReason(d, rootDev)})
	}
	return out, nil
}

// InspectCore uses the same local-first selection preflight uses, without
// signature verification, exactly as the TUI's flatpaks page does.
func (b *realBackend) InspectCore(ctx context.Context, ref string) corePreview {
	p := corePreview{ref: ref}
	if ref == "" {
		p.detail = "no image chosen"
		return p
	}
	ictx, cancel := context.WithTimeout(ctx, corePreviewTimeout)
	defer cancel()
	source, err := bootcimg.CheckAndPinImage(ictx, b.runner, ref, "", nil, nil)
	if err == nil && !source.Inspected {
		err = errors.New("image inspection returned no readable metadata")
	}
	if err != nil {
		p.detail = firstLine(err.Error())
		return p
	}
	value, present := source.Labels[flatpak.CoreLabel]
	apps, err := flatpak.ParseCoreLabelApps(value, present)
	switch {
	case errors.Is(err, flatpak.ErrMalformedCoreLabel):
		p.state = corePreviewInvalid
		p.detail = firstLine(err.Error())
	case err != nil:
		p.detail = firstLine(err.Error())
	case len(apps) == 0:
		p.state = corePreviewNone
	default:
		p.state = corePreviewAvailable
		p.apps = apps
	}
	return p
}

// Install runs firn's pipeline in-process, with progress over emit — the
// TUI's model (ADR-0007, ADR-0021); there is no subprocess. Unlike the TUI
// it adds no deprecationEmitter (cmd/firn): the GUI only builds version-2
// recipes, so there is no recipe_v1_deprecated warning to emit. A GUI that
// loads existing recipes must add it.
func (b *realBackend) Install(ctx context.Context, l *recipe.Loaded, emit progress.Emitter) error {
	env := &pipeline.Env{
		Recipe:        &l.Recipe,
		Machine:       b.machine.env,
		UEFI:          b.machine.uefi,
		Version:       b.machine.version,
		Runner:        b.runner,
		Emitter:       emit,
		RegistryProbe: bootcimg.HTTPRegistryProbe,
	}
	return steps.Assemble(l).Run(ctx, env, false)
}

func (b *realBackend) Reboot() error {
	return exec.Command("systemctl", "reboot").Run()
}

// firstLine returns s's first line with non-printable runes replaced, so
// registry or skopeo text cannot carry control characters into the window.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, line)
}
