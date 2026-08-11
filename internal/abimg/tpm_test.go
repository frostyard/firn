package abimg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// espWithUKI builds a fake mounted ESP carrying the named UKI.
func espWithUKI(t *testing.T, channel, version string) string {
	t.Helper()
	esp := t.TempDir()
	dir := filepath.Join(esp, "EFI", "Linux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, channel+"_"+version+".efi"), []byte("PE\x00\x00fake-uki"), 0o644); err != nil {
		t.Fatal(err)
	}
	return esp
}

func TestEnrollTPMVar(t *testing.T) {
	esp := espWithUKI(t, "stable", "1.2.3")
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "objcopy" {
			// Simulate objcopy writing the dumped section to the
			// path given in ".pcrpkey=<path>".
			path := strings.TrimPrefix(c.args[1], ".pcrpkey=")
			if err := os.WriteFile(path, []byte("-----BEGIN PUBLIC KEY-----"), 0o600); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}}
	if err := EnrollTPMVar(context.Background(), rec.runner(), esp, "stable", "1.2.3", "/dev/vdb4", "/run/firn/unlock.key"); err != nil {
		t.Fatalf("EnrollTPMVar: %v", err)
	}

	oc := rec.calls[0]
	if oc.name != "objcopy" || oc.args[0] != "--dump-section" {
		t.Fatalf("call 0 = %v, want objcopy --dump-section", oc.argv())
	}
	pcrpkey := strings.TrimPrefix(oc.args[1], ".pcrpkey=")
	if oc.args[2] != filepath.Join(esp, "EFI", "Linux", "stable_1.2.3.efi") {
		t.Errorf("objcopy input = %q, want the installed UKI", oc.args[2])
	}
	if oc.args[3] == "/dev/null" {
		t.Error("objcopy copy target must be a real scratch file, never /dev/null")
	}

	// Byte-exact enrollment argv: empty raw PCRs, pcrlock explicitly
	// off, signed PCR 11 only.
	ce := rec.calls[1]
	want := []string{
		"systemd-cryptenroll",
		"--tpm2-device=auto",
		"--tpm2-pcrs=",
		"--tpm2-pcrlock=",
		"--tpm2-public-key=" + pcrpkey,
		"--tpm2-public-key-pcrs=11",
		"--unlock-key-file=/run/firn/unlock.key",
		"/dev/vdb4",
	}
	if !slices.Equal(ce.argv(), want) {
		t.Errorf("cryptenroll argv = %v, want %v", ce.argv(), want)
	}
	if len(rec.calls) != 2 {
		t.Errorf("calls = %d, want 2: %+v", len(rec.calls), rec.calls)
	}
}

func TestEnrollTPMVarMissingUKI(t *testing.T) {
	esp := t.TempDir()
	rec := &recorder{}
	err := EnrollTPMVar(context.Background(), rec.runner(), esp, "stable", "1.2.3", "/dev/vdb4", "/run/firn/unlock.key")
	if err == nil || !strings.Contains(err.Error(), "installed UKI not found at EFI/Linux/stable_1.2.3.efi") {
		t.Fatalf("err = %v, want missing-UKI error", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("no commands must run when the UKI is missing: %+v", rec.calls)
	}
}

func TestEnrollTPMVarEmptyPcrpkey(t *testing.T) {
	esp := espWithUKI(t, "stable", "1.2.3")
	// The fake objcopy writes nothing: the non-empty check is the
	// actual success signal and must gate enrollment.
	rec := &recorder{}
	err := EnrollTPMVar(context.Background(), rec.runner(), esp, "stable", "1.2.3", "/dev/vdb4", "/run/firn/unlock.key")
	if err == nil || !strings.Contains(err.Error(), ".pcrpkey section is empty") {
		t.Fatalf("err = %v, want empty-pcrpkey error", err)
	}
	if i := rec.findCall("systemd-cryptenroll"); i != -1 {
		t.Errorf("cryptenroll must not run with an empty pcrpkey: %v", rec.calls[i].argv())
	}
}
