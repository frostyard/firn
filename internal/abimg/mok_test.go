package abimg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageMOK(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "mok.pem")
	if err := os.WriteFile(cert, []byte("-----BEGIN CERTIFICATE-----"), 0o644); err != nil {
		t.Fatal(err)
	}
	pwFile := filepath.Join(dir, "mok-password")
	if err := os.WriteFile(pwFile, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var importedHash string
	rec := &recorder{}
	rec.respond = func(c call) ([]byte, error) {
		if c.name == "mokutil" && strings.HasPrefix(c.args[0], "--generate-hash=") {
			return []byte("grub.pbkdf2.sha512.fakehash\n"), nil
		}
		if c.name == "mokutil" && c.args[0] == "--import" {
			// Capture the staged hash while the scratch file still
			// exists (it is removed after StageMOK returns).
			b, err := os.ReadFile(c.args[3])
			if err != nil {
				return nil, err
			}
			importedHash = string(b)
		}
		return nil, nil
	}
	if err := StageMOK(context.Background(), rec.runner(), cert, pwFile); err != nil {
		t.Fatalf("StageMOK: %v", err)
	}

	// The trailing newline is trimmed from the password file; the
	// plaintext rides mokutil's argv (an inherent mokutil interface
	// limitation — see StageMOK's honest note).
	assertCall(t, rec, 0, "mokutil", "--generate-hash=hunter2")

	der := rec.calls[1].args[6]
	if !strings.HasSuffix(der, ".der") {
		t.Errorf("DER output path = %q, want .der suffix", der)
	}
	assertCall(t, rec, 1, "openssl", "x509", "-in", cert, "-outform", "DER", "-out", der)

	imp := rec.calls[2]
	assertCall(t, rec, 2, "mokutil", "--import", der, "--hash-file", imp.args[3])
	if importedHash != "grub.pbkdf2.sha512.fakehash\n" {
		t.Errorf("imported hash file content = %q, want the generated hash", importedHash)
	}
	if _, err := os.Stat(imp.args[3]); !os.IsNotExist(err) {
		t.Errorf("hash scratch file %q must be removed after staging", imp.args[3])
	}
	if _, err := os.Stat(der); !os.IsNotExist(err) {
		t.Errorf("DER scratch file %q must be removed after staging", der)
	}
	if len(rec.calls) != 3 {
		t.Errorf("calls = %d, want 3: %+v", len(rec.calls), rec.calls)
	}
}

func TestStageMOKMissingCert(t *testing.T) {
	dir := t.TempDir()
	pwFile := filepath.Join(dir, "mok-password")
	if err := os.WriteFile(pwFile, []byte("pw"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	err := StageMOK(context.Background(), rec.runner(), filepath.Join(dir, "missing.pem"), pwFile)
	if err == nil || !strings.Contains(err.Error(), "MOK certificate not found") {
		t.Fatalf("err = %v, want missing-cert error", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("no commands must run without a certificate: %+v", rec.calls)
	}
}
