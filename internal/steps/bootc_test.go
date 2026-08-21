package steps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/disk"
	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/runner"
)

const verifiedBootcDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

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
	// The bootc UKI the tpm-enroll step globs for its signed PCR 11 key,
	// on the ESP mounted at boot/efi.
	ukiDir := filepath.Join(target, "boot", "efi", "EFI", "Linux")
	if err := os.MkdirAll(ukiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ukiDir, "7.1.3-amd64.efi"), []byte("fake-uki"), 0o644); err != nil {
		t.Fatal(err)
	}
	cosignKey := filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(cosignKey, []byte("public key"), 0o644); err != nil {
		t.Fatal(err)
	}
	// retag-root's WaitForPartition stats /dev/vda2, absent in this
	// fixture; make it appear immediately.
	defer disk.SwapStat(func(string) (os.FileInfo, error) { return nil, nil })()

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
				return []byte(`{"Digest":"` + verifiedBootcDigest + `"}`), nil
			case "cosign":
				return nil, nil
			case "objcopy":
				// abimg.EnrollTPMFromUKI dumps the UKI's .pcrpkey; write a
				// non-empty file so its extraction check passes.
				for _, a := range args {
					if p, ok := strings.CutPrefix(a, ".pcrpkey="); ok {
						_ = os.WriteFile(p, []byte("PCRPKEY"), 0o600)
					}
				}
				return nil, nil
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

	src := fmt.Sprintf(`
version = 1
[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
cosign_pub_key = %q
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
`, cosignKey)
	l := load(t, src)
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: fake, UEFI: true, Version: "test",
		TargetDir:  target,
		ScratchDir: filepath.Join(t.TempDir(), "scratch"),
		Emitter: progress.EmitterFunc(func(e progress.Event) error {
			events = append(events, e)
			return nil
		}),
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
		"sfdisk",                       // partitioning
		"mkfs.fat -F32",                // ESP
		"luksFormat",                   // encryption
		"mkfs.btrfs",                   // root
		"btrfs subvolume create",       // subvolumes
		"podman run --rm --privileged", // bootc via podman
		"cosign verify --key",          // preflight verifies a digest
		"ghcr.io/frostyard/snow@" + verifiedBootcDigest, // verified source is installed
		"--target-imgref ghcr.io/frostyard/snow:latest", // day-two tag retained
		"--composefs-backend",                           // snosi images require it
		"useradd",                                       // user creation
		"sfdisk --part-type /dev/vda 2",                 // retag-root -> DPS root GUID (encrypted too)
		"cryptsetup luksOpen",                           // retag reopens the mapper after retype
		"systemd-cryptenroll",                           // install-time TPM enrollment
		"--tpm2-public-key-pcrs=11",                     // bound to the UKI's signed PCR 11
		"cryptsetup luksClose firn-root",                // cleanup unwound
		"umount",                                        // target unmounted
		"fstrim",                                        // finalize
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

func TestBootcVerificationFailureLeavesTargetUntouched(t *testing.T) {
	key := filepath.Join(t.TempDir(), "cosign.pub")
	if err := os.WriteFile(key, []byte("wrong public key"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`
version = 1
[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
cosign_pub_key = %q
[target]
disk = "/dev/vda"
filesystem = "btrfs"
[security]
encryption = "none"
[system]
hostname = "frost01"
`, key)
	l := load(t, src)
	var commands []string
	fake := runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			switch name {
			case "skopeo":
				if strings.HasPrefix(args[1], "docker://") {
					return []byte(`{"Digest":"` + verifiedBootcDigest + `"}`), nil
				}
				return nil, errors.New("not cached")
			case "cosign":
				return nil, errors.New("no matching signatures")
			default:
				t.Fatalf("command %s ran after a preflight verification failure", name)
				return nil, nil
			}
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: fake, UEFI: true, Version: "test",
		Emitter: progress.EmitterFunc(func(e progress.Event) error {
			events = append(events, e)
			return nil
		}),
	}
	err := Assemble(l).Run(context.Background(), env, false)
	if err == nil || !strings.Contains(err.Error(), "no matching signatures") {
		t.Fatalf("pipeline verification error = %v", err)
	}
	terminal, ok := events[len(events)-1].(progress.Error)
	if !ok || terminal.Step != "preflight-image" || terminal.Code != progress.CodeImageVerifyFailed {
		t.Fatalf("terminal verification event = %#v", events[len(events)-1])
	}
	for _, command := range commands {
		for _, destructive := range []string{"sfdisk", "wipefs", "mkfs", "cryptsetup", "bootc", "podman"} {
			if strings.HasPrefix(command, destructive+" ") {
				t.Fatalf("target-modifying command ran after failed verification: %s", command)
			}
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
		Emitter: progress.EmitterFunc(func(e progress.Event) error {
			events = append(events, e)
			return nil
		}),
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
