package installer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDetectSession(t *testing.T) {
	dir := t.TempDir()
	zoneinfo := filepath.Join(dir, "zoneinfo")
	if err := os.MkdirAll(filepath.Join(zoneinfo, "Europe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zoneinfo, "Europe", "Berlin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	localtime := filepath.Join(dir, "localtime")
	if err := os.Symlink("/usr/share/zoneinfo/Europe/Berlin", localtime); err != nil {
		t.Fatal(err)
	}
	kb := filepath.Join(dir, "keyboard")
	if err := os.WriteFile(kb, []byte("XKBMODEL=\"pc105\"\nXKBLAYOUT=\"de,us\"\nXKBVARIANT=\"nodeadkeys,\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"LANG": "de_DE.utf8"}
	s := detectSession(func(k string) string { return env[k] }, zoneinfo, localtime, kb)
	if s.locale != "de_DE.UTF-8" || s.timezone != "Europe/Berlin" || s.keyboard != "de:nodeadkeys" {
		t.Errorf("detectSession = %+v", s)
	}

	env = map[string]string{"LANG": "C.UTF-8"}
	s = detectSession(func(k string) string { return env[k] }, zoneinfo, filepath.Join(dir, "absent"), filepath.Join(dir, "absent"))
	if s != (sessionDefaults{}) {
		t.Errorf("values the schema would reject must not prefill: %+v", s)
	}
}

func TestReadKeyboards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.lst")
	body := "! model\n  pc105  Generic 105-key PC\n\n! layout\n  us   English (US)\n  de   German\n\n! variant\n  nodeadkeys  de: German (no dead keys)\n\n! option\n  grp  Switching\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	codes, names := readKeyboards(path)
	if !slices.Equal(codes, []string{"de", "de:nodeadkeys", "us"}) {
		t.Errorf("codes = %v", codes)
	}
	if names["de:nodeadkeys"] != "German (no dead keys)" || names["us"] != "English (US)" {
		t.Errorf("names = %v", names)
	}
	if got, _ := readKeyboards(filepath.Join(t.TempDir(), "absent")); !slices.Equal(got, commonKeyboards) {
		t.Error("a missing rules file should fall back to the common list")
	}
}

func TestReadLocales(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SUPPORTED")
	if err := os.WriteFile(path, []byte("en_US.UTF-8 UTF-8\nen_US ISO-8859-1\nde_DE.UTF-8 UTF-8\nC.UTF-8 UTF-8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readLocales(path); !slices.Equal(got, []string{"de_DE.UTF-8", "en_US.UTF-8"}) {
		t.Errorf("readLocales = %v", got)
	}
}
