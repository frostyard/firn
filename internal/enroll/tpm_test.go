package enroll

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestEnrollTPMFromUKI(t *testing.T) {
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "objcopy" {
			return nil, os.WriteFile(strings.TrimPrefix(c.args[1], ".pcrpkey="), []byte("public-key"), 0o600)
		}
		return nil, nil
	}}
	if err := EnrollTPMFromUKI(context.Background(), rec.runner(), "/esp/EFI/Linux/uki.efi", "/dev/vdb4", "/run/unlock.key"); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 2 {
		t.Fatalf("calls = %+v", rec.calls)
	}
	oc := rec.calls[0]
	if oc.name != "objcopy" || oc.args[0] != "--dump-section" || oc.args[2] != "/esp/EFI/Linux/uki.efi" || oc.args[3] == "/dev/null" {
		t.Fatalf("objcopy = %+v", oc)
	}
	key := strings.TrimPrefix(oc.args[1], ".pcrpkey=")
	want := []string{"systemd-cryptenroll", "--tpm2-device=auto", "--tpm2-pcrs=", "--tpm2-pcrlock=", "--tpm2-public-key=" + key, "--tpm2-public-key-pcrs=11", "--unlock-key-file=/run/unlock.key", "/dev/vdb4"}
	if !slices.Equal(rec.calls[1].argv(), want) {
		t.Errorf("cryptenroll = %v, want %v", rec.calls[1].argv(), want)
	}
	for _, path := range []string{key, oc.args[3]} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("scratch %s still exists: %v", path, err)
		}
	}
}

func TestEnrollTPMFromUKIEmptyPcrpkey(t *testing.T) {
	rec := &recorder{}
	err := EnrollTPMFromUKI(context.Background(), rec.runner(), "/esp/uki.efi", "/dev/vdb4", "/run/unlock.key")
	if err == nil || !strings.Contains(err.Error(), ".pcrpkey section is empty") {
		t.Fatalf("error = %v", err)
	}
	if rec.findCall("systemd-cryptenroll") != -1 {
		t.Fatal("enrolled without key")
	}
}
