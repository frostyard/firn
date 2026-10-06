package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/flatpak"
)

// corePreviewTimeout bounds the flatpaks page's image inspection, so an
// unreachable registry delays the page instead of hanging it.
const corePreviewTimeout = 30 * time.Second

// corePreviewState is what the flatpaks page knows about the chosen image's
// core Flatpak set (ADR-0018). The zero value, unavailable, keeps the
// toggle: install-time preflight reads the label again either way.
type corePreviewState int

const (
	corePreviewUnavailable corePreviewState = iota // inspect failed or not attempted
	corePreviewNone                                // image publishes no core set
	corePreviewInvalid                             // label present but malformed
	corePreviewAvailable                           // valid, non-empty set
)

// corePreview describes the core set of the image whose ref it records. It
// is display-only: the recipe carries just core_flatpaks, and preflight
// reads the label from the image it selects at install time.
type corePreview struct {
	ref    string
	state  corePreviewState
	apps   []flatpak.CoreApp
	detail string
}

// refreshCorePreview inspects the chosen image when the preview describes a
// different one (first visit, a new image, or Start over), using the same
// local-first selection preflight uses, without signature verification.
func (w *wizard) refreshCorePreview(ctx context.Context) {
	ref := w.c.entry.Ref
	if w.preview.ref == ref && ref != "" {
		return
	}
	w.preview = inspectCorePreview(ctx, w, ref)
}

func inspectCorePreview(ctx context.Context, w *wizard, ref string) corePreview {
	p := corePreview{ref: ref}
	if w.opts.Runner == nil || ref == "" {
		p.detail = "no image inspector"
		return p
	}
	ictx, cancel := context.WithTimeout(ctx, corePreviewTimeout)
	defer cancel()
	source, err := bootcimg.CheckAndPinImage(ictx, w.opts.Runner, ref, "", nil)
	if err != nil {
		// Keep the next visit retrying rather than caching a transient
		// failure such as a slow or offline registry.
		p.ref = ""
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
		p.ref = ""
		p.detail = firstLine(err.Error())
	case len(apps) == 0:
		p.state = corePreviewNone
	default:
		p.state = corePreviewAvailable
		p.apps = apps
	}
	return p
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// wrapNames joins app names into lines no wider than width.
func wrapNames(apps []flatpak.CoreApp, width int) string {
	var lines []string
	var line string
	for i, app := range apps {
		item := app.Name
		if i < len(apps)-1 {
			item += ","
		}
		switch {
		case line == "":
			line = item
		case len(line)+1+len(item) > width:
			lines = append(lines, line)
			line = item
		default:
			line += " " + item
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
