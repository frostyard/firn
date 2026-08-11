package tui

// The image catalog offered by the wizard. The built-in list covers the
// snosi image families (ADR-0010: one installer ISO installs everything);
// an override file replaces it wholesale. The override mechanism follows
// fisherman's precedent of images.json at /etc/tuna-installer/images.json
// (frostyard/fisherman, GPL-3.0-only; see NOTICE).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/frostyard/firn/internal/recipe"
)

// catalogOverridePath, when present, replaces the built-in catalog.
const catalogOverridePath = "/etc/firn/catalog.json"

// CatalogEntry is one installable image the wizard offers. Ref is set
// for bootc entries, Product for ab entries — never both.
type CatalogEntry struct {
	Family      string `json:"family"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Ref         string `json:"ref,omitempty"`
	Product     string `json:"product,omitempty"`
}

// builtinCatalog is the default snosi image catalog. Descriptions match
// the snosi README's image table.
func builtinCatalog() []CatalogEntry {
	return []CatalogEntry{
		{Family: recipe.FamilyBootc, Name: "snow", Description: "GNOME desktop with backports kernel", Ref: "ghcr.io/frostyard/snow:latest"},
		{Family: recipe.FamilyBootc, Name: "snowfield", Description: "GNOME desktop with linux-surface kernel for Surface devices", Ref: "ghcr.io/frostyard/snowfield:latest"},
		{Family: recipe.FamilyBootc, Name: "cayo", Description: "Headless server with podman and backports kernel", Ref: "ghcr.io/frostyard/cayo:latest"},
		{Family: recipe.FamilyAB, Name: "snow-ab", Description: "Native A/B GNOME desktop with backports kernel", Product: "snow-ab"},
		{Family: recipe.FamilyAB, Name: "snowfield-ab", Description: "Native A/B GNOME desktop with linux-surface kernel", Product: "snowfield-ab"},
		{Family: recipe.FamilyAB, Name: "cayo-ab", Description: "Native A/B headless server with podman", Product: "cayo-ab"},
	}
}

// LoadCatalog returns the image catalog: the override file when present
// and valid, the built-in list otherwise. A non-nil warn means the
// override existed but was unusable — the built-ins are returned and the
// caller should surface the warning loudly rather than install from a
// silently wrong list.
func LoadCatalog() (entries []CatalogEntry, warn error) {
	return loadCatalogFrom(catalogOverridePath)
}

// loadCatalogFrom implements LoadCatalog against an explicit path, for
// tests.
func loadCatalogFrom(path string) ([]CatalogEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return builtinCatalog(), nil
	}
	if err != nil {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: %w (using built-in catalog)", path, err)
	}
	var entries []CatalogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: %w (using built-in catalog)", path, err)
	}
	if err := checkCatalog(entries); err != nil {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: %w (using built-in catalog)", path, err)
	}
	return entries, nil
}

// checkCatalog fail-closed-validates override entries: the wizard must
// not offer an entry that cannot produce a valid recipe.
func checkCatalog(entries []CatalogEntry) error {
	if len(entries) == 0 {
		return errors.New("catalog is empty")
	}
	for i, e := range entries {
		if e.Name == "" {
			return fmt.Errorf("entry %d: name is required", i)
		}
		switch e.Family {
		case recipe.FamilyBootc:
			if e.Ref == "" {
				return fmt.Errorf("entry %q: bootc entries require ref", e.Name)
			}
			if e.Product != "" {
				return fmt.Errorf("entry %q: product applies only to family %q", e.Name, recipe.FamilyAB)
			}
		case recipe.FamilyAB:
			if e.Product == "" {
				return fmt.Errorf("entry %q: ab entries require product", e.Name)
			}
			if e.Ref != "" {
				return fmt.Errorf("entry %q: ref applies only to family %q", e.Name, recipe.FamilyBootc)
			}
		default:
			return fmt.Errorf("entry %q: family must be %q or %q, got %q", e.Name, recipe.FamilyBootc, recipe.FamilyAB, e.Family)
		}
	}
	return nil
}

// orderedCatalog returns entries grouped by family (bootc first, then
// ab), preserving in-family order, so the picker reads as two blocks.
func orderedCatalog(entries []CatalogEntry) []CatalogEntry {
	out := make([]CatalogEntry, 0, len(entries))
	for _, e := range entries {
		if e.Family == recipe.FamilyBootc {
			out = append(out, e)
		}
	}
	for _, e := range entries {
		if e.Family != recipe.FamilyBootc {
			out = append(out, e)
		}
	}
	return out
}

// formatCatalogOption renders one catalog entry as a picker line,
// including its family and one-line description.
func formatCatalogOption(e CatalogEntry) string {
	kind := "bootc image"
	if e.Family == recipe.FamilyAB {
		kind = "A/B image"
	}
	return fmt.Sprintf("%-14s %-12s %s", e.Name, "("+kind+")", e.Description)
}
