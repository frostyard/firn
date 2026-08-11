package steps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/runner"
)

// TestBootcPipelineEndToEnd drives the fully wired bootc pipeline
// against a fake runner and a fixture composefs target tree: no real
// commands, no real disks — but every step's Run executes.
func TestBootcPipelineEndToEnd(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	// Composefs fixture: state/deploy/<id>/etc is the writable etc the
	// sysconfig writer and TPM staging resolve to.
	etcDir := filepath.Join(target, "state", "deploy", "abc123", "etc")
	if err := os.MkdirAll(etcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "state", "os", "default", "var"), 0o755); err != nil {
		t.Fatal(err)
	}

	const lsblkJSON = `{"blockdevices": [
	  {"path": "/dev/vda", "type": "disk", "size": 64000000000, "fstype": null, "label": null, "mountpoints": [null]}]}`

	var argvs [][]string
	fake := runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			argvs = append(argvs, append([]string{name}, args...))
			switch name {
			case "lsblk":
				return []byte(lsblkJSON), nil
			case "findmnt":
				return []byte("overlay\n"), nil
			case "cryptsetup":
				if len(args) > 0 && args[0] == "luksUUID" {
					return []byte("dead-beef-uuid\n"), nil
				}
				return nil, nil
			case "du":
				return []byte("0\t/x\n"), nil
			case "flatpak":
				if len(args) > 0 && args[0] == "list" {
					return []byte(""), nil
				}
				return nil, errors.New("network unreachable")
			case "env": // FLATPAK_SYSTEM_DIR download path
				return nil, errors.New("network unreachable")
			case "skopeo":
				return []byte(`{"schemaVersion": 2}`), nil
			case "ls":
				// composefs marker probes resolve against the real fixture.
				if _, err := os.Stat(args[len(args)-1]); err != nil {
					return nil, errors.New("no such file")
				}
				return []byte(""), nil
			}
			return []byte(""), nil
		},
		func(name string) (string, error) {
			if name == "bootc" {
				return "", errors.New("not on PATH") // force the podman path
			}
			return "/usr/bin/" + name, nil
		},
	)

	src := `
version = 1
[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
[target]
disk = "/dev/vda"
filesystem = "btrfs"
btrfs_subvolumes = true
[security]
encryption = "tpm2-luks"
[system]
hostname = "frost01"
locale = "en_US.UTF-8"
timezone = "America/Chicago"
keyboard = "us"
flatpaks = ["org.mozilla.firefox"]
[system.user]
name = "bjk"
password_hash = "$6$salt$hash"
groups = ["wheel"]
`
	l := load(t, src)
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: fake, UEFI: true, Version: "test",
		TargetDir:  target,
		ScratchDir: filepath.Join(t.TempDir(), "scratch"),
		Emit:       func(e progress.Event) { events = append(events, e) },
	}
	env.Machine.TPM = true

	if err := Assemble(l).Run(context.Background(), env, false); err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	// Terminal event ordering and content.
	last := events[len(events)-1]
	if last.Kind() != "done" {
		t.Fatalf("last event %s, want done", last.Kind())
	}
	var sawRecoveryKey bool
	var summary *progress.Summary
	for _, e := range events {
		switch ev := e.(type) {
		case progress.RecoveryKey:
			sawRecoveryKey = ev.Key != ""
		case progress.Summary:
			summary = &ev
		}
	}
	if !sawRecoveryKey {
		t.Error("tpm2-luks must disclose a recovery key event")
	}
	if summary == nil || len(summary.Items) == 0 {
		t.Error("unreachable flatpak must appear in the summary")
	} else if summary.Items[0].Code != progress.CodeFlatpakUnreachable {
		t.Errorf("summary code %q", summary.Items[0].Code)
	}

	// Filesystem effects on the fixture tree.
	if data, err := os.ReadFile(filepath.Join(etcDir, "hostname")); err != nil || strings.TrimSpace(string(data)) != "frost01" {
		t.Errorf("hostname = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(etcDir, "systemd", "system", "firn-tpm2-enroll.service")); err != nil {
		t.Errorf("TPM first-boot unit not staged: %v", err)
	}
	if key, err := os.ReadFile(filepath.Join(etcDir, "firn", "tpm2-enroll.key")); err != nil || len(key) == 0 {
		t.Errorf("transient enroll key not staged: %v", err)
	}
	// Phase-5 matrix: locale/timezone/keyboard land in the deployment etc.
	if data, err := os.ReadFile(filepath.Join(etcDir, "locale.conf")); err != nil || string(data) != "LANG=en_US.UTF-8\n" {
		t.Errorf("locale.conf = %q, %v", data, err)
	}
	if link, err := os.Readlink(filepath.Join(etcDir, "localtime")); err != nil || !strings.HasSuffix(link, "America/Chicago") {
		t.Errorf("localtime -> %q, %v", link, err)
	}
	if data, err := os.ReadFile(filepath.Join(etcDir, "default", "keyboard")); err != nil || !strings.Contains(string(data), `XKBLAYOUT="us"`) {
		t.Errorf("keyboard = %q, %v", data, err)
	}

	// Command-sequence spot checks.
	joined := make([]string, len(argvs))
	for i, a := range argvs {
		joined[i] = strings.Join(a, " ")
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{
		"sfdisk",                         // partitioning
		"mkfs.fat -F32",                  // ESP
		"luksFormat",                     // encryption
		"mkfs.btrfs",                     // root
		"btrfs subvolume create",         // subvolumes
		"podman run --rm --privileged",   // bootc via podman
		"--composefs-backend",            // snosi images require it
		"useradd",                        // user creation
		"cryptsetup luksClose firn-root", // cleanup unwound
		"umount",                         // target unmounted
		"fstrim",                         // finalize
	} {
		if !strings.Contains(all, want) {
			t.Errorf("expected %q in command sequence:\n%s", want, all)
		}
	}

	// LUKS passphrase must never appear on any argv.
	for _, line := range joined {
		if env.LuksKey != "" && strings.Contains(line, env.LuksKey) {
			t.Fatalf("recovery key leaked onto argv: %s", line)
		}
	}
}

func TestBootcZFSRootRefusedClearly(t *testing.T) {
	src := strings.Replace(strings.Replace(bootcBase, "%s", "none", 1),
		`filesystem = "btrfs"`, `filesystem = "zfs"`, 1)
	l := load(t, src)
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: dryRunFake(t), UEFI: true, Version: "test",
		Emit: func(e progress.Event) { events = append(events, e) },
	}
	err := Assemble(l).Run(context.Background(), env, false)
	if err == nil || !strings.Contains(err.Error(), "zfs") {
		t.Errorf("zfs root must fail with a clear parity-gap error, got %v", err)
	}
}

// dryRunFake is a runner whose lsblk shows a clean /dev/vda.
func dryRunFake(t *testing.T) *runner.Runner {
	t.Helper()
	const lsblkJSON = `{"blockdevices": [
	  {"path": "/dev/vda", "type": "disk", "size": 64000000000, "fstype": null, "label": null, "mountpoints": [null]}]}`
	return runner.NewFake(
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			switch name {
			case "lsblk":
				return []byte(lsblkJSON), nil
			case "findmnt":
				return []byte("overlay\n"), nil
			}
			return []byte(""), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}
