package steps

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/progress"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/runner"
	"github.com/frostyard/firn/internal/sysconfig"
	"github.com/frostyard/firn/internal/trust"
)

// fixtureXZ is a real xz container (of ~1 MB of deterministic bytes)
// so trust.MinimumDiskBytes can parse a genuine xz index over Range
// requests. Generated once with `xz -6`.
const fixtureXZB64 = "/Td6WFoAAATm1rRGBMDyAZCLQCEBFgAAAAAAAHbFmRLwBY8A6l0AIxJGimgcfCss9tR3msXKcvFQOyLne3qwvMGhG+hkxjH95CNHxS16ZLuWVALmQ5EiAPIWAMSKmMPPAOUTuf+eysL8tE75g30gpjvL/+GC+VRz+5NQ3/PMkBSy+gegLCRskqMnZKG1NaVHDNGREaqtYB26zrEnGFxZhulmUli+6XasWeTlWwUI+cfarfz7Uit0zR5bIEL53VM9+ClkCTuAyyps37U78MS9Ll+qDz5LZkKQEw7/EJP4cXhZ+AvN/5UoRg+p/Hze+5owLlbAj4Xzg4HAZcQlU/j1kTYxBaWw7m/BcE1HC8ptH4AAAAAAX/m8ZoKM68wAAY4CkItAANWodj2xxGf7AgAAAAAEWVo="

const abVersion = "20260811110127"

const abLayoutJSON = `{"partitiontable":{"label":"gpt","device":"DISK","unit":"sectors","partitions":[
  {"node":"/dev/fake1","start":2048,"size":2097152,"name":"esp"},
  {"node":"/dev/fake2","start":2099200,"size":524288,"name":"cayo-ab_20260811110127_v"},
  {"node":"/dev/fake3","start":2623488,"size":16777216,"name":"cayo-ab_20260811110127_r"},
  {"node":"/dev/fake4","start":19400704,"size":524288,"name":"_empty"},
  {"node":"/dev/fake5","start":19924992,"size":16777216,"name":"_empty"},
  {"node":"/dev/fake6","start":36702208,"size":8388608,"name":"var"}]}}`

func TestABPipelineEndToEnd(t *testing.T) {
	restore := sysconfig.SetChownForTesting(func(string, int, int) error { return nil })
	defer restore()

	xzBytes, err := base64.StdEncoding.DecodeString(fixtureXZB64)
	if err != nil {
		t.Fatal(err)
	}
	xzSum := sha256.Sum256(xzBytes)

	manifest := []byte(`{"manifest_version":1,"config":{"name":"cayo-ab","architecture":"x86-64","version":"` + abVersion + `"}}`)
	manSum := sha256.Sum256(manifest)
	sums := fmt.Sprintf("%s  cayo-ab_%s.disk.raw.xz\n%s  cayo-ab_%s.manifest.json\n",
		hex.EncodeToString(xzSum[:]), abVersion, hex.EncodeToString(manSum[:]), abVersion)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		base := "/os/native/v1/cayo/x86-64/"
		switch strings.TrimPrefix(req.URL.Path, base) {
		case "SHA256SUMS":
			io.WriteString(w, sums)
		case "SHA256SUMS.gpg":
			w.Write([]byte("fake-signature"))
		case "cayo-ab_" + abVersion + ".manifest.json":
			w.Write(manifest)
		case "cayo-ab_" + abVersion + ".disk.raw.xz":
			http.ServeContent(w, req, "disk.raw.xz", time.Time{}, strings.NewReader(string(xzBytes)))
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	// The "disk" is a temp file the stream step writes into.
	disk := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(disk, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pubring := filepath.Join(t.TempDir(), "pubring.gpg")
	if err := os.WriteFile(pubring, []byte("fake-keyring"), 0o644); err != nil {
		t.Fatal(err)
	}
	rootKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake root@test"

	// Fixture baselines created by the fake "mount" handler.
	baselineEtc := func(dir string) error {
		etc := filepath.Join(dir, ".etc.lower")
		if err := os.MkdirAll(filepath.Join(etc, "skel"), 0o755); err != nil {
			return err
		}
		files := map[string]struct {
			content string
			mode    os.FileMode
		}{
			"passwd":  {"root:x:0:0:root:/root:/bin/bash\n", 0o644},
			"group":   {"root:x:0:\nsudo:x:27:\nshadow:x:42:\n", 0o644},
			"shadow":  {"root:*:19000:0:99999:7:::\n", 0o640},
			"gshadow": {"root:*::\nsudo:*::\n", 0o640},
		}
		for name, f := range files {
			if err := os.WriteFile(filepath.Join(etc, name), []byte(f.content), f.mode); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(etc, "skel", ".bashrc"), []byte("# skel\n"), 0o644)
	}

	var argvs [][]string
	fake := runner.NewFake(
		func(ctx context.Context, name string, args ...string) ([]byte, error) {
			argvs = append(argvs, append([]string{name}, args...))
			switch name {
			case "gpgv":
				return nil, nil
			case "blockdev":
				if args[0] == "--getsize64" {
					return []byte("21474836480\n"), nil
				}
				return nil, nil
			case "udevadm", "sfdisk":
				if len(args) > 0 && args[0] == "--json" {
					return []byte(strings.ReplaceAll(abLayoutJSON, "DISK", args[1])), nil
				}
				return nil, nil
			case "xz":
				in, out, ok := runner.Stream(ctx)
				if !ok {
					return nil, errors.New("xz called without stream")
				}
				_, err := io.Copy(out, in) // fake decompression: identity
				return nil, err
			case "mount":
				dir := args[len(args)-1]
				switch {
				case len(args) >= 2 && args[len(args)-2] == "/dev/fake1": // ESP
					uki := filepath.Join(dir, "EFI", "Linux", "cayo-ab_"+abVersion+".efi")
					if err := os.MkdirAll(filepath.Dir(uki), 0o755); err != nil {
						return nil, err
					}
					return nil, os.WriteFile(uki, []byte("fake-uki"), 0o644)
				case len(args) >= 2 && args[len(args)-2] == "/dev/fake3": // erofs root
					return nil, baselineEtc(dir)
				}
				return nil, nil
			case "objcopy":
				for _, a := range args {
					if path, ok := strings.CutPrefix(a, ".pcrpkey="); ok {
						return nil, os.WriteFile(path, []byte("fake-pcr-key"), 0o600)
					}
					if path, ok := strings.CutPrefix(a, "--dump-section=.pcrpkey="); ok {
						return nil, os.WriteFile(path, []byte("fake-pcr-key"), 0o600)
					}
				}
				return nil, nil
			}
			return []byte(""), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)

	rootKeyFile := filepath.Join(t.TempDir(), "root.pub")
	if err := os.WriteFile(rootKeyFile, []byte(rootKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`
version = 1
[image]
family = "ab"
product = "cayo-ab"
[target]
disk = "%s"
var_filesystem = "btrfs"
var_subvolumes = true
[security]
encryption = "tpm2-luks"
[system]
hostname = "frost-ab"
locale = "en_US.UTF-8"
timezone = "America/Chicago"
keyboard = "us"
root_ssh_authorized_key_file = "%s"
[system.user]
name = "bjk"
password_hash = "$6$salt$hash"
groups = ["sudo", "wheel"]
`, disk, rootKeyFile)

	l, err := recipe.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var events []progress.Event
	env := &pipeline.Env{
		Recipe: &l.Recipe, Runner: fake, UEFI: true, Version: "test",
		Trust: trust.Options{Origin: srv.URL, PubringPath: pubring, Client: srv.Client()},
		Emit:  func(e progress.Event) { events = append(events, e) },
	}
	env.Machine.TPM = true

	// Preflight's lsblk would not know our temp-file "disk"; run the
	// work steps directly (preflight has its own tests).
	p := Assemble(l)
	work := &pipeline.Pipeline{Steps: p.Steps[3:]}
	if err := work.Run(context.Background(), env, false); err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	// Terminal ordering, recovery key, group_missing summary.
	if last := events[len(events)-1]; last.Kind() != "done" {
		t.Fatalf("last event %s, want done", last.Kind())
	}
	var recoveryKey string
	var summaryCodes []string
	for _, e := range events {
		switch ev := e.(type) {
		case progress.RecoveryKey:
			recoveryKey = ev.Key
		case progress.Summary:
			for _, item := range ev.Items {
				summaryCodes = append(summaryCodes, item.Code)
			}
		}
	}
	if recoveryKey == "" {
		t.Error("tpm2-luks must disclose a recovery key")
	}
	if len(summaryCodes) != 1 || summaryCodes[0] != "group_missing" {
		t.Errorf("summary codes = %v, want [group_missing] (wheel not in image)", summaryCodes)
	}

	// The disk received the (fake-decompressed) image bytes.
	if data, err := os.ReadFile(disk); err != nil || len(data) != len(xzBytes) {
		t.Errorf("disk bytes = %d, want %d (%v)", len(data), len(xzBytes), err)
	}

	// Overlay effects. VarMount is a real temp dir the fake mount left
	// writable; the overlay writer wrote into it.
	upper := filepath.Join(env.VarMount, "lib", "snosi", "etc-overlay", "upper")
	for path, want := range map[string]string{
		"hostname":                   "frost-ab",
		"locale.conf":                "LANG=en_US.UTF-8",
		"timezone":                   "America/Chicago",
		"ssh/authorized_keys.d/root": rootKey,
	} {
		data, err := os.ReadFile(filepath.Join(upper, path))
		if err != nil || !strings.Contains(string(data), want) {
			t.Errorf("overlay %s = %q, want to contain %q (%v)", path, data, want, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(upper, "localtime")); err != nil || !strings.HasSuffix(target, "America/Chicago") {
		t.Errorf("localtime -> %q, %v", target, err)
	}
	passwd, err := os.ReadFile(filepath.Join(upper, "passwd"))
	if err != nil || !strings.Contains(string(passwd), "bjk:x:1000:1000") {
		t.Errorf("overlay passwd = %q (%v)", passwd, err)
	}
	group, _ := os.ReadFile(filepath.Join(upper, "group"))
	if !strings.Contains(string(group), "sudo:x:27:bjk") {
		t.Errorf("bjk not joined to sudo: %q", group)
	}
	if strings.Contains(string(group), "wheel") {
		t.Errorf("nonexistent wheel group must not be created: %q", group)
	}
	if _, err := os.Stat(filepath.Join(env.VarMount, "home", "bjk", ".bashrc")); err != nil {
		t.Errorf("skel not copied into var home: %v", err)
	}
	info, err := os.ReadFile(filepath.Join(env.VarMount, "lib", "snosi", "install-info.json"))
	if err != nil || !strings.Contains(string(info), abVersion) {
		t.Errorf("install-info.json = %q (%v)", info, err)
	}

	// Command spot-checks: enrollment argv and secret hygiene.
	all := ""
	for _, a := range argvs {
		all += strings.Join(a, " ") + "\n"
		if recoveryKey != "" && strings.Contains(strings.Join(a, " "), recoveryKey) {
			t.Fatalf("recovery key leaked onto argv: %v", a)
		}
	}
	for _, want := range []string{
		"gpgv", "sfdisk --lock=yes --relocate gpt-bak-std",
		"cryptsetup luksFormat", "mkfs.btrfs -f -L var", "btrfs subvolume create",
		"--tpm2-public-key-pcrs=11", "cryptsetup luksClose firn-var-install",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("expected %q in command sequence:\n%s", want, all)
		}
	}
}

func TestABDryRunAssembly(t *testing.T) {
	l, err := recipe.Parse([]byte(`
version = 1
[image]
family = "ab"
product = "snow-ab"
[target]
disk = "/dev/vda"
[security]
encryption = "none"
[system]
hostname = "h"
`))
	if err != nil {
		t.Fatal(err)
	}
	p := Assemble(l)
	for _, s := range p.Steps {
		if !s.Preflight && s.Run == nil {
			t.Errorf("step %s has no Run implementation", s.Name)
		}
	}
}
