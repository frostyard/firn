package installer

// The demo backend lets anyone try the installer, unprivileged, on any
// machine: it offers made-up disks, previews made-up core apps and plays an
// install's progress events. It never runs a host tool or opens a device.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/flatpak"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/steps"
)

type demoBackend struct {
	machine machineInfo
	// pace scales every simulated delay; tests set it to zero.
	pace time.Duration
}

func newDemoBackend(m machineInfo) *demoBackend {
	return &demoBackend{machine: m, pace: time.Millisecond}
}

func (b *demoBackend) sleep(ctx context.Context, units int) error {
	if b.pace == 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(units) * b.pace):
		return nil
	}
}

func (b *demoBackend) Disks(ctx context.Context) ([]diskInfo, error) {
	if err := b.sleep(ctx, 350); err != nil {
		return nil, err
	}
	const gib = int64(1) << 30
	return []diskInfo{
		{Device: disk.Device{
			Path: "/dev/nvme0n1", Type: "disk", Size: 953 * gib, Model: "Samsung SSD 980 PRO 1TB",
			Serial: "S5GXNF0R123456", WWN: "eui.002538b111b2c3d4", Transport: "nvme",
			Children: []disk.Device{{Path: "/dev/nvme0n1p1", Type: "part", Fstype: "vfat", Label: "EFI"}, {Path: "/dev/nvme0n1p2", Type: "part", Fstype: "ntfs", Label: "Windows"}},
		}},
		{Device: disk.Device{
			Path: "/dev/sda", Type: "disk", Size: 477 * gib, Vendor: "ATA", Model: "Crucial MX500",
			Serial: "2041E4A1B2C3", WWN: "0x500a0751e4a1b2c3", Transport: "sata",
			Children: []disk.Device{{Path: "/dev/sda1", Type: "part", Fstype: "ext4", Label: "data"}},
		}},
		{Device: disk.Device{
			Path: "/dev/sdb", Type: "disk", Size: 29 * gib, Vendor: "SanDisk", Model: "Ultra Fit",
			Transport: "usb",
			Children:  []disk.Device{{Path: "/dev/sdb1", Type: "part", Fstype: "iso9660", Label: "SNOSI_INSTALL", Mountpoints: []string{"/run/initramfs/live"}}},
		}, refused: "it holds the running installer"},
	}, nil
}

func (b *demoBackend) InspectCore(ctx context.Context, ref string) corePreview {
	p := corePreview{ref: ref}
	if err := b.sleep(ctx, 1200); err != nil {
		p.detail = err.Error()
		return p
	}
	if strings.Contains(ref, "floe") {
		p.state = corePreviewNone
		return p
	}
	p.state = corePreviewAvailable
	for _, name := range []string{
		"Calculator", "Calendar", "Characters", "Clocks", "Contacts", "Document Viewer",
		"Disk Usage Analyzer", "Extension Manager", "Fonts", "Image Viewer", "Logs",
		"Maps", "Text Editor", "Weather", "Web Browser", "Videos", "Music", "Flatseal",
	} {
		p.apps = append(p.apps, flatpak.CoreApp{Name: name})
	}
	return p
}

// demoNarration is what each simulated step says as it starts.
var demoNarration = map[string]string{
	"preflight-uefi":  "firmware is UEFI",
	"preflight-tools": "all required tools are present",
	"preflight-image": "resolved and verified the image digest",
	"preflight-disk":  "the target disk is not in use",
	"partition":       "wrote a GPT partition table",
	"format-boot":     "created the EFI system partition",
	"luks-format":     "set up LUKS2 encryption on the root partition",
	"format-root":     "created the root filesystem",
	"mount-target":    "mounted the target",
	"bootc-install":   "deploying the image with bootc",
	"tpm-enroll":      "bound the disk key to this machine's TPM",
	"esp-stage":       "staged the Secure Boot chain",
	"retag-root":      "tagged the root partition for discovery",
	"flatpaks":        "installing apps from Flathub",
	"sysconfig":       "applied hostname, locale, users and keys",
	"finalize":        "trimmed and froze the filesystems",
	"mok-stage":       "staged MOK enrollment for the next boot",
}

// Install plays the events the real pipeline would emit for l, step by step,
// using the real assembly for the step list and weights.
func (b *demoBackend) Install(ctx context.Context, l *recipe.Loaded, emit progress.Emitter) error {
	p := steps.Assemble(l)
	if err := emit.Emit(progress.Start{Protocol: progress.Protocol, Firn: b.machine.version, Steps: p.ProgressSteps()}); err != nil {
		return err
	}
	fail := func(step string, err error) error {
		_ = emit.Emit(progress.Error{Step: step, Code: progress.CodeStepFailed, Message: err.Error()})
		return err
	}
	for i, s := range p.Steps {
		if err := emit.Emit(progress.StepStart{Index: i, Name: s.Name}); err != nil {
			return err
		}
		if msg := demoNarration[s.Name]; msg != "" {
			_ = emit.Emit(progress.Info{Message: msg})
		}
		switch s.Name {
		case "bootc-install":
			const total = int64(3_400_000_000)
			for n := 1; n <= 40; n++ {
				if err := b.sleep(ctx, 90); err != nil {
					return fail(s.Name, fmt.Errorf("canceled: %w", err))
				}
				_ = emit.Emit(progress.StepProgress{Index: i, Bytes: total * int64(n) / 40, TotalBytes: total})
			}
		case "flatpaks":
			for n := 1; n <= 10; n++ {
				if err := b.sleep(ctx, 120); err != nil {
					return fail(s.Name, fmt.Errorf("canceled: %w", err))
				}
				_ = emit.Emit(progress.StepProgress{Index: i, Fraction: float64(n) / 10})
			}
		case "luks-format":
			if l.Recipe.Security.Encryption == "tpm2-luks" {
				_ = emit.Emit(progress.RecoveryKey{Key: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"})
			}
			fallthrough
		default:
			if err := b.sleep(ctx, 450); err != nil {
				return fail(s.Name, fmt.Errorf("canceled: %w", err))
			}
		}
	}
	if len(l.Recipe.System.Flatpaks) > 0 {
		item := progress.SummaryItem{Code: progress.CodeFlatpakUnreachable, Detail: l.Recipe.System.Flatpaks[0] + " (demo: pretending Flathub did not answer)"}
		_ = emit.Emit(progress.Summary{Items: []progress.SummaryItem{item}})
	}
	return emit.Emit(progress.Done{OK: true})
}

func (b *demoBackend) Reboot() error { return nil }
