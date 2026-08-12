package secureboot

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

const validContract = `{
  "schema": 1,
  "mok_certificate": "/usr/lib/snosi/mok.crt",
  "installer": {
    "oci": { "capability_label": "io.snosi.bootc.secureboot-capable", "capability_value": "true" },
    "secure_boot": { "shim": "debian", "second_stage": "mok-signed-systemd-boot", "mok_manager": "MokManager" },
    "unknown_future_field": "must not fail the parse"
  }
}`

func writeContract(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "usr/lib/snosi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootc-secure.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestContractLoad(t *testing.T) {
	// Valid contract, including an unknown additive field (drift resilience).
	c, err := Load(writeContract(t, validContract))
	if err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	if c.MokCertificate != "/usr/lib/snosi/mok.crt" {
		t.Errorf("mok cert = %q", c.MokCertificate)
	}

	// Fail-closed cases.
	for name, body := range map[string]string{
		"wrong schema":       strings.Replace(validContract, `"schema": 1`, `"schema": 2`, 1),
		"not capable":        strings.Replace(validContract, `"capability_value": "true"`, `"capability_value": "false"`, 1),
		"wrong second stage": strings.Replace(validContract, "mok-signed-systemd-boot", "plain-systemd-boot", 1),
		"wrong shim":         strings.Replace(validContract, `"shim": "debian"`, `"shim": "grub"`, 1),
		"malformed json":     "{not json",
	} {
		if _, err := Load(writeContract(t, body)); err == nil {
			t.Errorf("%s: expected fail-closed error, got nil", name)
		}
	}

	// Missing contract file entirely.
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("missing contract: expected error")
	}
}

// fakeSB returns a runner whose sbverify reports a signed binary and whose
// other commands succeed, recording argv.
func fakeSB(argv *[][]string) *runner.Runner {
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			*argv = append(*argv, append([]string{name}, args...))
			if name == "sbverify" && len(args) > 0 && args[0] == "--list" {
				return []byte("image signature issuers:\n /CN=Test\n"), nil
			}
			return nil, nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func TestStageESPChain(t *testing.T) {
	imageRoot := t.TempDir()
	// Source binaries in the extracted image tree.
	for rel, body := range map[string]string{
		imageShim:               "SHIM-BYTES",
		imageMokManager:         "MMX64-BYTES",
		imageSecondStage:        "SYSTEMD-BOOT-BYTES",
		"usr/lib/snosi/mok.crt": "CERT",
	} {
		p := filepath.Join(imageRoot, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Target ESP, pre-seeded with bootc's plain systemd-boot at BOOTX64.EFI so
	// the shim overwrite is observable.
	target := t.TempDir()
	esp := filepath.Join(target, "boot/efi/EFI/BOOT")
	if err := os.MkdirAll(esp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(esp, "BOOTX64.EFI"), []byte("PLAIN-SYSTEMD-BOOT"), 0o644); err != nil {
		t.Fatal(err)
	}

	var argv [][]string
	if err := StageESPChain(context.Background(), fakeSB(&argv), target, imageRoot, "/usr/lib/snosi/mok.crt"); err != nil {
		t.Fatalf("StageESPChain: %v", err)
	}

	// The MOK-signed second stage is verified against the cert; shim + MokManager
	// are checked for signature presence.
	joined := make([]string, len(argv))
	for i, a := range argv {
		joined[i] = strings.Join(a, " ")
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"sbverify --cert " + filepath.Join(imageRoot, "usr/lib/snosi/mok.crt") + " " + filepath.Join(imageRoot, imageSecondStage),
		"sbverify --list " + filepath.Join(imageRoot, imageShim),
		"sbverify --list " + filepath.Join(imageRoot, imageMokManager),
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing argv %q in:\n%s", want, all)
		}
	}

	// File effects: the three components land with the image bytes, and shim
	// overwrote bootc's plain systemd-boot at BOOTX64.EFI.
	for name, want := range map[string]string{
		"BOOTX64.EFI": "SHIM-BYTES",
		"grubx64.efi": "SYSTEMD-BOOT-BYTES",
		"mmx64.efi":   "MMX64-BYTES",
	} {
		got, err := os.ReadFile(filepath.Join(esp, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v (want %q)", name, got, err, want)
		}
	}
}

func TestStageESPChainRefusesUnsignedShim(t *testing.T) {
	imageRoot := t.TempDir()
	for _, rel := range []string{imageShim, imageMokManager, imageSecondStage, "usr/lib/snosi/mok.crt"} {
		p := filepath.Join(imageRoot, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	target := t.TempDir()
	_ = os.MkdirAll(filepath.Join(target, "boot/efi/EFI/BOOT"), 0o755)

	// sbverify --list returns no "image signature issuers:" -> unsigned.
	r := runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "sbverify" && len(args) > 0 && args[0] == "--list" {
				return []byte("No signature table present\n"), nil
			}
			return nil, nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
	err := StageESPChain(context.Background(), r, target, imageRoot, "/usr/lib/snosi/mok.crt")
	if err == nil || !strings.Contains(err.Error(), "unsigned ESP component") {
		t.Errorf("expected unsigned-component refusal, got %v", err)
	}
}

func TestUntarIntoRefusesTraversal(t *testing.T) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	_ = w.WriteHeader(&tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1})
	_, _ = w.Write([]byte("x"))
	_ = w.Close()
	if err := untarInto(&buf, t.TempDir()); err == nil || !strings.Contains(err.Error(), "outside the extraction root") {
		t.Errorf("expected traversal refusal, got %v", err)
	}
}
