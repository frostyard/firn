package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/recipe"
)

func TestBuiltinCatalog(t *testing.T) {
	entries := builtinCatalog()
	if err := checkCatalog(entries); err != nil {
		t.Fatalf("built-in catalog fails its own validation: %v", err)
	}
	names := make(map[string]CatalogEntry, len(entries))
	for _, e := range entries {
		names[e.Name] = e
	}
	for _, want := range []string{"snow", "snowfield", "cayo", "snow-ab", "snowfield-ab", "cayo-ab"} {
		if _, ok := names[want]; !ok {
			t.Errorf("built-in catalog missing %q", want)
		}
	}
	for _, n := range []string{"snow", "snowfield", "cayo"} {
		e := names[n]
		if e.Family != recipe.FamilyBootc || !strings.HasPrefix(e.Ref, "ghcr.io/frostyard/") {
			t.Errorf("entry %q: want bootc with ghcr.io/frostyard ref, got family=%q ref=%q", n, e.Family, e.Ref)
		}
	}
	for _, n := range []string{"snow-ab", "snowfield-ab", "cayo-ab"} {
		e := names[n]
		if e.Family != recipe.FamilyAB || e.Product != n {
			t.Errorf("entry %q: want ab with product %q, got family=%q product=%q", n, n, e.Family, e.Product)
		}
	}
	for _, e := range entries {
		if e.Description == "" {
			t.Errorf("entry %q has no description", e.Name)
		}
	}
}

func TestLoadCatalogNoOverride(t *testing.T) {
	entries, warn := loadCatalogFrom(filepath.Join(t.TempDir(), "absent.json"))
	if warn != nil {
		t.Errorf("missing override file must not warn: %v", warn)
	}
	if len(entries) != len(builtinCatalog()) {
		t.Errorf("missing override must return built-ins, got %d entries", len(entries))
	}
}

func TestLoadCatalogOverrideReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	body := `[
		{"family": "bootc", "name": "custom", "description": "in-house image", "ref": "registry.example.com/custom:1"},
		{"family": "ab", "name": "custom-ab", "description": "in-house ab", "product": "custom-ab"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, warn := loadCatalogFrom(path)
	if warn != nil {
		t.Fatalf("valid override must not warn: %v", warn)
	}
	if len(entries) != 2 || entries[0].Name != "custom" || entries[1].Product != "custom-ab" {
		t.Errorf("override must replace built-ins entirely, got %+v", entries)
	}
}

func TestLoadCatalogOverrideErrors(t *testing.T) {
	cases := map[string]string{
		"parse error":     `{not json`,
		"empty list":      `[]`,
		"missing name":    `[{"family": "bootc", "ref": "r"}]`,
		"bad family":      `[{"family": "flatcar", "name": "x", "ref": "r"}]`,
		"bootc no ref":    `[{"family": "bootc", "name": "x"}]`,
		"ab no product":   `[{"family": "ab", "name": "x"}]`,
		"ab with ref":     `[{"family": "ab", "name": "x", "product": "p", "ref": "r"}]`,
		"bootc w product": `[{"family": "bootc", "name": "x", "ref": "r", "product": "p"}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			entries, warn := loadCatalogFrom(path)
			if warn == nil {
				t.Fatal("bad override must warn loudly")
			}
			if len(entries) != len(builtinCatalog()) {
				t.Errorf("bad override must fall back to built-ins, got %d entries", len(entries))
			}
		})
	}
}

func TestOrderedCatalogGroupsByFamily(t *testing.T) {
	in := []CatalogEntry{
		{Family: recipe.FamilyAB, Name: "b-ab", Product: "b-ab"},
		{Family: recipe.FamilyBootc, Name: "a", Ref: "r1"},
		{Family: recipe.FamilyAB, Name: "c-ab", Product: "c-ab"},
		{Family: recipe.FamilyBootc, Name: "d", Ref: "r2"},
	}
	got := orderedCatalog(in)
	wantOrder := []string{"a", "d", "b-ab", "c-ab"}
	for i, name := range wantOrder {
		if got[i].Name != name {
			t.Fatalf("orderedCatalog order = %v, want %v", got, wantOrder)
		}
	}
}

func TestFormatCatalogOption(t *testing.T) {
	b := formatCatalogOption(CatalogEntry{Family: recipe.FamilyBootc, Name: "snow", Description: "GNOME desktop", Ref: "r"})
	if !strings.Contains(b, "snow") || !strings.Contains(b, "GNOME desktop") || !strings.Contains(b, "bootc") {
		t.Errorf("bootc option line missing detail: %q", b)
	}
	a := formatCatalogOption(CatalogEntry{Family: recipe.FamilyAB, Name: "snow-ab", Description: "A/B desktop", Product: "snow-ab"})
	if !strings.Contains(a, "snow-ab") || !strings.Contains(a, "A/B") {
		t.Errorf("ab option line missing detail: %q", a)
	}
}
