package tui

import (
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/catalog"
	"github.com/frostyard/firn/internal/recipe"
)

func TestFormatCatalogOption(t *testing.T) {
	b := formatCatalogOption(CatalogEntry{Family: recipe.FamilyBootc, Name: "snow", Description: "GNOME desktop", Ref: "r"})
	if !strings.Contains(b, "snow") || !strings.Contains(b, "GNOME desktop") || !strings.Contains(b, "bootc") {
		t.Errorf("bootc option line missing detail: %q", b)
	}
	if strings.Contains(b, "A/B") {
		t.Fatalf("bootc picker includes A/B label: %q", b)
	}
}

func TestLoadCatalogIsTheSharedCatalog(t *testing.T) {
	got, gotWarn := LoadCatalog()
	want, wantWarn := catalog.Load()
	if len(got) != len(want) || (gotWarn == nil) != (wantWarn == nil) {
		t.Fatalf("LoadCatalog = %d entries (warn %v), catalog.Load = %d (warn %v)", len(got), gotWarn, len(want), wantWarn)
	}
}
