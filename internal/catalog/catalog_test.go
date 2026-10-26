package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/recipe"
)

func TestBuiltin(t *testing.T) {
	entries := Builtin()
	if err := Check(entries); err != nil {
		t.Fatalf("built-in catalog fails its own validation: %v", err)
	}
	names := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if e.Family != recipe.FamilyBootc {
			t.Errorf("builtin %q is not bootc: %q", e.Name, e.Family)
		}
		names[e.Name] = e
	}
	for _, want := range []string{"snow", "snowfield", "floe"} {
		if _, ok := names[want]; !ok {
			t.Errorf("built-in catalog missing %q", want)
		}
	}
	for _, n := range []string{"snow", "snowfield", "floe"} {
		e := names[n]
		if e.Family != recipe.FamilyBootc || !strings.HasPrefix(e.Ref, "ghcr.io/frostyard/") || e.CosignPubKey != BuiltinCosignPubKey {
			t.Errorf("entry %q: want signed frostyard bootc ref, got family=%q ref=%q key=%q", n, e.Family, e.Ref, e.CosignPubKey)
		}
	}
	for _, e := range entries {
		if e.Description == "" {
			t.Errorf("entry %q has no description", e.Name)
		}
	}
}

func TestLoadNoOverride(t *testing.T) {
	entries, warn := LoadFrom(filepath.Join(t.TempDir(), "absent.json"))
	if warn != nil {
		t.Errorf("missing override file must not warn: %v", warn)
	}
	if len(entries) != len(Builtin()) {
		t.Errorf("missing override must return built-ins, got %d entries", len(entries))
	}
}

func TestLoadOverrideReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	body := `[{"family": "bootc", "name": "custom", "description": "in-house image", "ref": "registry.example.com/custom:1"}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, warn := LoadFrom(path)
	if warn != nil {
		t.Fatalf("valid override must not warn: %v", warn)
	}
	if len(entries) != 1 || entries[0].Name != "custom" {
		t.Errorf("override must replace built-ins entirely, got %+v", entries)
	}
}

func TestLoadOverrideErrors(t *testing.T) {
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
		"ab after bootc":     `[{"family":"bootc","name":"floe","ref":"r"},{"family":"ab","name":"legacy","ref":"r"}]`,
		"unknown field":      `[{"family":"bootc","name":"floe","ref":"r","unexpected":true}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			entries, warn := LoadFrom(path)
			if warn == nil {
				t.Fatal("bad override must warn loudly")
			}
			if len(entries) != len(Builtin()) {
				t.Errorf("bad override must fall back to built-ins, got %d entries", len(entries))
			}
			for _, entry := range entries {
				if entry.Family != recipe.FamilyBootc {
					t.Fatalf("fallback includes unsupported family: %+v", entry)
				}
			}
		})
	}
}

func TestCheckRejectsABAfterBootc(t *testing.T) {
	entries := []Entry{
		{Family: recipe.FamilyBootc, Name: "floe", Ref: "ghcr.io/frostyard/floe:latest"},
		{Family: "ab", Name: "legacy", Ref: "ghcr.io/frostyard/floe:latest"},
	}
	err := Check(entries)
	if err == nil || !strings.Contains(err.Error(), `entry "legacy": family must be "bootc", got "ab"`) {
		t.Fatalf("checkCatalog error = %v, want A/B family rejection", err)
	}
}

func TestDefaultGroups(t *testing.T) {
	// Override entries parse default_groups; absent field stays empty.
	path := filepath.Join(t.TempDir(), "catalog.json")
	body := `[
		{"family": "bootc", "name": "desk", "description": "d", "ref": "r:1", "default_groups": ["sudo", "video", "lpadmin"]},
		{"family": "bootc", "name": "plain", "description": "p", "ref": "r:2"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, warn := LoadFrom(path)
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

func TestBuiltinDefaultGroups(t *testing.T) {
	// Every builtin entry declares defaults: desktops get the device/admin
	// set, servers the minimal set; sudo is always included
	// (frostyard/snosi#789).
	for _, e := range Builtin() {
		if len(e.DefaultGroups) == 0 {
			t.Errorf("builtin entry %q has no default_groups", e.Name)
			continue
		}
		if e.DefaultGroups[0] != "sudo" {
			t.Errorf("builtin entry %q defaults must start with sudo, got %v", e.Name, e.DefaultGroups)
		}
	}
}

func TestGroupsCoverBuiltinDefaults(t *testing.T) {
	offered := make(map[string]bool)
	for _, g := range Groups() {
		if g.Name == "" {
			t.Fatalf("offered group with no name: %+v", g)
		}
		if offered[g.Name] {
			t.Errorf("group %q offered twice", g.Name)
		}
		offered[g.Name] = true
	}
	for _, e := range Builtin() {
		for _, g := range e.DefaultGroups {
			if !offered[g] {
				t.Errorf("builtin entry %q preselects %q, which Groups does not offer", e.Name, g)
			}
		}
	}
}

func TestLoadReadsTheOverridePath(t *testing.T) {
	// Load is LoadFrom(OverridePath), whatever this machine has there.
	got, gotWarn := Load()
	want, wantWarn := LoadFrom(OverridePath)
	if len(got) != len(want) || (gotWarn == nil) != (wantWarn == nil) {
		t.Fatalf("Load = %d entries (warn %v), LoadFrom(OverridePath) = %d (warn %v)", len(got), gotWarn, len(want), wantWarn)
	}
	for i := range got {
		if got[i].Name != want[i].Name || got[i].Ref != want[i].Ref {
			t.Errorf("entry %d: %+v != %+v", i, got[i], want[i])
		}
	}
}

func TestLoadUnreadableOrTrailingOverrideFallsBack(t *testing.T) {
	dir := t.TempDir()
	trailing := filepath.Join(dir, "trailing.json")
	body := `[{"family": "bootc", "name": "x", "ref": "r"}] []`
	if err := os.WriteFile(trailing, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"directory":     dir, // reading it fails with an error other than not-exist
		"trailing JSON": trailing,
	} {
		entries, warn := LoadFrom(path)
		if warn == nil || !strings.Contains(warn.Error(), "using built-in catalog") {
			t.Errorf("%s: warn = %v, want a loud fallback", name, warn)
		}
		if len(entries) != len(Builtin()) {
			t.Errorf("%s: got %d entries, want the built-ins", name, len(entries))
		}
	}
}
