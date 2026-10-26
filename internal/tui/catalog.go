package tui

// The image catalog offered by the wizard lives in internal/catalog, shared
// with the graphical installer (ADR-0021); these names keep the wizard's
// vocabulary.

import (
	"fmt"
	"strings"

	"github.com/frostyard/firn/internal/catalog"
)

// CatalogEntry is one installable image the wizard offers.
type CatalogEntry = catalog.Entry

// LoadCatalog returns the image catalog: the override file when present
// and valid, the built-in list otherwise. A non-nil warn means the
// override existed but was unusable — the built-ins are returned and the
// caller should surface the warning loudly rather than install from a
// silently wrong list.
func LoadCatalog() (entries []CatalogEntry, warn error) {
	return catalog.Load()
}

// builtinCatalog is the default snosi image catalog.
func builtinCatalog() []CatalogEntry { return catalog.Builtin() }

// checkCatalog fail-closed-validates entries: the wizard must not offer an
// entry that cannot produce a valid recipe.
func checkCatalog(entries []CatalogEntry) error { return catalog.Check(entries) }

// formatCatalogOption renders one catalog entry as a picker line,
// including its one-line description.
func formatCatalogOption(e CatalogEntry) string {
	return strings.TrimSpace(fmt.Sprintf("%-14s (bootc image) %s", e.Name, e.Description))
}
