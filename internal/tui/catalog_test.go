package tui

import (
	"os"
	"path/filepath"
	"reflect"
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
		if e.Family != recipe.FamilyBootc || !strings.HasPrefix(e.Ref, "ghcr.io/frostyard/") || e.CosignPubKey != builtinCosignPubKey {
			t.Errorf("entry %q: want signed frostyard bootc ref, got family=%q ref=%q key=%q", n, e.Family, e.Ref, e.CosignPubKey)
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
		"parse error":        `{not json`,
		"empty list":         `[]`,
		"missing name":       `[{"family": "bootc", "ref": "r"}]`,
		"bad family":         `[{"family": "flatcar", "name": "x", "ref": "r"}]`,
		"bootc no ref":       `[{"family": "bootc", "name": "x"}]`,
		"bootc invalid ref":  `[{"family": "bootc", "name": "x", "ref": "ghcr.io/foo bar:latest"}]`,
		"ab no product":      `[{"family": "ab", "name": "x"}]`,
		"ab bad product":     `[{"family": "ab", "name": "x", "product": "../Snow.*-ab"}]`,
		"ab with ref":        `[{"family": "ab", "name": "x", "product": "p", "ref": "r"}]`,
		"bootc w product":    `[{"family": "bootc", "name": "x", "ref": "r", "product": "p"}]`,
		"ab with cosign key": `[{"family": "ab", "name": "x", "product": "p", "cosign_pub_key": "/key.pub"}]`,
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

func TestCatalogFamiliesRepresented(t *testing.T) {
	tests := []struct {
		name    string
		entries []CatalogEntry
		want    []string
	}{
		{name: "bootc only", entries: []CatalogEntry{bootcEntry()}, want: []string{recipe.FamilyBootc}},
		{name: "A/B only", entries: []CatalogEntry{abEntry()}, want: []string{recipe.FamilyAB}},
		{
			name:    "mixed",
			entries: []CatalogEntry{bootcEntry(), abEntry()},
			want:    []string{recipe.FamilyBootc, recipe.FamilyAB},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := catalogFamilies(orderedCatalog(tt.entries))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("catalogFamilies() = %v, want %v", got, tt.want)
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

func TestCatalogDefaultGroups(t *testing.T) {
	// Override entries parse default_groups; absent field stays empty.
	path := filepath.Join(t.TempDir(), "catalog.json")
	body := `[
		{"family": "bootc", "name": "desk", "description": "d", "ref": "r:1", "default_groups": ["sudo", "video", "lpadmin"]},
		{"family": "ab", "name": "plain", "description": "p", "product": "plain-ab"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, warn := loadCatalogFrom(path)
	if warn != nil {
		t.Fatalf("valid override must not warn: %v", warn)
	}
	if got := entries[0].DefaultGroups; len(got) != 3 || got[1] != "video" {
		t.Errorf("default_groups not parsed: %+v", got)
	}
	if len(entries[1].DefaultGroups) != 0 {
		t.Errorf("absent default_groups must stay empty, got %+v", entries[1].DefaultGroups)
	}
}

func TestBuiltinCatalogDefaultGroups(t *testing.T) {
	// Every builtin entry declares defaults: desktops get the device/admin
	// set, servers the minimal set; sudo is always included
	// (frostyard/snosi#789).
	for _, e := range builtinCatalog() {
		if len(e.DefaultGroups) == 0 {
			t.Errorf("builtin entry %q has no default_groups", e.Name)
			continue
		}
		if e.DefaultGroups[0] != "sudo" {
			t.Errorf("builtin entry %q defaults must start with sudo, got %v", e.Name, e.DefaultGroups)
		}
	}
}
