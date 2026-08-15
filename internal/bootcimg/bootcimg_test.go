// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/install/bootc_test.go.

package bootcimg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

// forceBool overrides a package-level bool hook for the test's duration.
func forceBool(t *testing.T, hook *func() bool, v bool) {
	t.Helper()
	old := *hook
	*hook = func() bool { return v }
	t.Cleanup(func() { *hook = old })
}

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "base",
			opts: Options{Image: "ghcr.io/tuna-os/yellowfin:gnome-hwe", TargetDir: "/mnt/target"},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "ghcr.io/tuna-os/yellowfin:gnome-hwe",
				"--source-imgref", "containers-storage:ghcr.io/tuna-os/yellowfin:gnome-hwe",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "target imgref set",
			opts: Options{
				Image:        "ghcr.io/tuna-os/yellowfin:gnome-hwe",
				TargetImgref: "ghcr.io/tuna-os/yellowfin:gnome50",
				TargetDir:    "/mnt/target",
			},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "ghcr.io/tuna-os/yellowfin:gnome50",
				"--source-imgref", "containers-storage:ghcr.io/tuna-os/yellowfin:gnome-hwe",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "transport prefix stripped from image",
			opts: Options{Image: "containers-storage:ghcr.io/ublue-os/bluefin:stable", TargetDir: "/mnt/target"},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "ghcr.io/ublue-os/bluefin:stable",
				"--source-imgref", "containers-storage:ghcr.io/ublue-os/bluefin:stable",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "docker transport prefix stripped",
			opts: Options{Image: "docker://ghcr.io/ublue-os/bluefin:stable", TargetDir: "/mnt/target"},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "ghcr.io/ublue-os/bluefin:stable",
				"--source-imgref", "containers-storage:ghcr.io/ublue-os/bluefin:stable",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "selinux disabled",
			opts: Options{Image: "img.example/os:1", TargetDir: "/mnt/target", SELinuxDisabled: true},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:1",
				"--disable-selinux",
				"--source-imgref", "containers-storage:img.example/os:1",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "bootloader systemd",
			opts: Options{Image: "img.example/os:1", TargetDir: "/mnt/target", Bootloader: "systemd"},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:1",
				"--source-imgref", "containers-storage:img.example/os:1",
				"--bootloader", "systemd",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "bootloader grub2 emits no flag",
			opts: Options{Image: "img.example/os:1", TargetDir: "/mnt/target", Bootloader: "grub2"},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:1",
				"--source-imgref", "containers-storage:img.example/os:1",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "btrfs subvolumes emits no flag",
			opts: Options{Image: "img.example/os:1", TargetDir: "/mnt/target", BtrfsSubvolumes: true},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:1",
				"--source-imgref", "containers-storage:img.example/os:1",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "no image and no target imgref omits both refs",
			opts: Options{TargetDir: "/mnt/target"},
			want: []string{
				"install", "to-filesystem",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "all flags",
			opts: Options{
				Image:           "img.example/os:1",
				TargetImgref:    "img.example/os:stable",
				TargetDir:       "/mnt/target",
				Bootloader:      "systemd",
				SELinuxDisabled: true,
			},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:stable",
				"--disable-selinux",
				"--source-imgref", "containers-storage:img.example/os:1",
				"--bootloader", "systemd",
				"--skip-finalize",
				"/mnt/target",
			},
		},
		{
			name: "verified digest source retains tracking tag",
			opts: Options{
				Image:        "img.example/os@" + remoteDigest,
				TargetImgref: "img.example/os:stable",
				TargetDir:    "/mnt/target",
			},
			want: []string{
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:stable",
				"--source-imgref", "containers-storage:img.example/os@" + remoteDigest,
				"--skip-finalize",
				"/mnt/target",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildArgs(tt.opts)
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildArgs() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

func TestBuildPodmanArgs(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		efiVars bool
		want    []string
	}{
		{
			name:    "base no efivars",
			opts:    Options{Image: "ghcr.io/tuna-os/yellowfin:gnome-hwe", TargetDir: "/mnt/target", ScratchDir: "/scratch"},
			efiVars: false,
			want: []string{
				"run", "--rm",
				"--privileged",
				"--pid=host",
				"--network", "host",
				"--security-opt", "label=disable",
				"-v", "/dev:/dev",
				"-v", "/sys:/sys",
				"-v", "/scratch:/var/tmp:z",
				"--mount", "type=bind,src=/mnt/target,dst=/target,bind-propagation=rslave",
				"-v", "/var/lib/containers:/var/lib/containers",
				"ghcr.io/tuna-os/yellowfin:gnome-hwe",
				"bootc",
				"install", "to-filesystem",
				"--target-imgref", "ghcr.io/tuna-os/yellowfin:gnome-hwe",
				"--skip-finalize",
				"/target",
			},
		},
		{
			name:    "efivars mounted when present",
			opts:    Options{Image: "img.example/os:1", TargetDir: "/mnt/target", ScratchDir: "/scratch"},
			efiVars: true,
			want: []string{
				"run", "--rm",
				"--privileged",
				"--pid=host",
				"--network", "host",
				"--security-opt", "label=disable",
				"-v", "/dev:/dev",
				"-v", "/sys:/sys",
				"-v", "/sys/firmware/efi/efivars:/sys/firmware/efi/efivars",
				"-v", "/scratch:/var/tmp:z",
				"--mount", "type=bind,src=/mnt/target,dst=/target,bind-propagation=rslave",
				"-v", "/var/lib/containers:/var/lib/containers",
				"img.example/os:1",
				"bootc",
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:1",
				"--skip-finalize",
				"/target",
			},
		},
		{
			name:    "default scratch dir, systemd bootloader, selinux disabled",
			opts:    Options{Image: "img.example/os:1", TargetDir: "/mnt/target", Bootloader: "systemd", SELinuxDisabled: true},
			efiVars: false,
			want: []string{
				"run", "--rm",
				"--privileged",
				"--pid=host",
				"--network", "host",
				"--security-opt", "label=disable",
				"-v", "/dev:/dev",
				"-v", "/sys:/sys",
				"-v", "/var/firn-tmp:/var/tmp:z",
				"--mount", "type=bind,src=/mnt/target,dst=/target,bind-propagation=rslave",
				"-v", "/var/lib/containers:/var/lib/containers",
				"img.example/os:1",
				"bootc",
				"install", "to-filesystem",
				"--target-imgref", "img.example/os:1",
				"--disable-selinux",
				"--bootloader", "systemd",
				"--skip-finalize",
				"/target",
			},
		},
		{
			name: "target imgref stripped of transport, podman image ref kept raw",
			opts: Options{
				Image:      "containers-storage:ghcr.io/ublue-os/bluefin:stable",
				TargetDir:  "/mnt/target",
				ScratchDir: "/scratch",
			},
			efiVars: false,
			want: []string{
				"run", "--rm",
				"--privileged",
				"--pid=host",
				"--network", "host",
				"--security-opt", "label=disable",
				"-v", "/dev:/dev",
				"-v", "/sys:/sys",
				"-v", "/scratch:/var/tmp:z",
				"--mount", "type=bind,src=/mnt/target,dst=/target,bind-propagation=rslave",
				"-v", "/var/lib/containers:/var/lib/containers",
				"containers-storage:ghcr.io/ublue-os/bluefin:stable",
				"bootc",
				"install", "to-filesystem",
				"--target-imgref", "ghcr.io/ublue-os/bluefin:stable",
				"--skip-finalize",
				"/target",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forceBool(t, &efiVarsPresentFn, tt.efiVars)
			got := BuildPodmanArgs(tt.opts)
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuildPodmanArgs() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

// call records one fake-runner execution.
type call struct {
	name string
	args []string
}

func TestInstall_Selection(t *testing.T) {
	opts := Options{Image: "img.example/os:1", TargetDir: "/mnt/target", ScratchDir: "/scratch"}

	tests := []struct {
		name          string
		haveBootc     bool
		havePodman    bool
		selinuxActive bool
		opts          Options
		// wantTool selects the expected executable ("bootc" or "podman");
		// its argv is computed inside the subtest (after the efivars/selinux
		// hooks are forced) via BuildArgs / BuildPodmanArgs.
		wantTool string
		wantErr  string
	}{
		{
			name:      "bootc on PATH runs directly",
			haveBootc: true, havePodman: true,
			opts:     opts,
			wantTool: "bootc",
		},
		{
			name:      "no bootc falls back to podman",
			haveBootc: false, havePodman: true,
			opts:     opts,
			wantTool: "podman",
		},
		{
			name:      "neither tool errors without executing",
			haveBootc: false, havePodman: false,
			opts:    opts,
			wantErr: "neither bootc nor podman found",
		},
		{
			name:      "podman path with selinux-disabled target on selinux-active host errors",
			haveBootc: false, havePodman: true,
			selinuxActive: true,
			opts:          Options{Image: "img.example/os:1", TargetDir: "/mnt/target", SELinuxDisabled: true},
			wantErr:       "BuildSelinuxBypassShim",
		},
		{
			name:      "direct path unaffected by selinux-active host",
			haveBootc: true, havePodman: true,
			selinuxActive: true,
			opts:          Options{Image: "img.example/os:1", TargetDir: "/mnt/target", SELinuxDisabled: true},
			wantTool:      "bootc",
		},
		{
			name:      "missing image errors",
			haveBootc: true, havePodman: true,
			opts:    Options{TargetDir: "/mnt/target"},
			wantErr: "no image",
		},
		{
			name:      "missing target dir errors",
			haveBootc: true, havePodman: true,
			opts:    Options{Image: "img.example/os:1"},
			wantErr: "no target directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forceBool(t, &efiVarsPresentFn, false)
			forceBool(t, &selinuxActiveFn, tt.selinuxActive)
			var calls []call
			r := runner.NewFake(
				func(_ context.Context, name string, args ...string) ([]byte, error) {
					calls = append(calls, call{name: name, args: args})
					return nil, nil
				},
				func(name string) (string, error) {
					switch {
					case name == "bootc" && tt.haveBootc:
						return "/usr/bin/bootc", nil
					case name == "podman" && tt.havePodman:
						return "/usr/bin/podman", nil
					}
					return "", errors.New("not found")
				},
			)

			err := Install(context.Background(), r, tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Install() = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Install() error = %q, want it to contain %q", err, tt.wantErr)
				}
				if len(calls) != 0 {
					t.Fatalf("Install() executed %v before erroring; want no executions", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Install() error = %v", err)
			}
			wantArgs := BuildArgs(tt.opts)
			if tt.wantTool == "podman" {
				wantArgs = BuildPodmanArgs(tt.opts)
			}
			if len(calls) != 1 {
				t.Fatalf("Install() made %d calls %v, want 1", len(calls), calls)
			}
			if calls[0].name != tt.wantTool || !slices.Equal(calls[0].args, wantArgs) {
				t.Errorf("call = %s %q, want %s %q", calls[0].name, calls[0].args, tt.wantTool, wantArgs)
			}
		})
	}
}

func TestInstall_WrapsExecError(t *testing.T) {
	forceBool(t, &selinuxActiveFn, false)
	execErr := errors.New("boom")
	r := runner.NewFake(
		func(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, execErr },
		func(name string) (string, error) {
			if name == "bootc" {
				return "/usr/bin/bootc", nil
			}
			return "", errors.New("not found")
		},
	)
	err := Install(context.Background(), r, Options{Image: "img.example/os:1", TargetDir: "/mnt/target"})
	if !errors.Is(err, execErr) {
		t.Fatalf("Install() error = %v, want wrapped %v", err, execErr)
	}
	if !strings.Contains(err.Error(), "bootc install to-filesystem") {
		t.Fatalf("Install() error = %q, want bootc context", err)
	}
}

// checkImageFake returns a fake runner whose skopeo inspect calls are
// answered per transport, and records the exact argv of every call.
func checkImageFake(t *testing.T, calls *[]call, remoteOut []byte, remoteErr error, localOut []byte, localErr error) *runner.Runner {
	t.Helper()
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			*calls = append(*calls, call{name: name, args: args})
			if name != "skopeo" || len(args) != 2 || args[0] != "inspect" {
				t.Fatalf("unexpected command: %s %q", name, args)
			}
			if strings.HasPrefix(args[1], "docker://") {
				return remoteOut, remoteErr
			}
			return localOut, localErr
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

func TestCheckImage(t *testing.T) {
	manifest := []byte(`{"Digest":"sha256:aaaa","Layers":["sha256:l1","sha256:l2"]}`)
	tests := []struct {
		name      string
		remoteOut []byte
		remoteErr error
		localOut  []byte
		localErr  error
		wantErr   bool
	}{
		{
			name:      "remote reachable, not cached locally",
			remoteOut: manifest,
			localErr:  fmt.Errorf("image not known"),
		},
		{
			name:      "offline with local cache",
			remoteErr: fmt.Errorf("network unreachable"),
			localOut:  manifest,
		},
		{
			name:      "remote newer, local still used",
			remoteOut: []byte(`{"Digest":"sha256:remote-newer","Layers":["sha256:l1"]}`),
			localOut:  []byte(`{"Digest":"sha256:local-embedded","Layers":["sha256:l1"]}`),
		},
		{
			name:      "offline and not cached",
			remoteErr: fmt.Errorf("network error"),
			localErr:  fmt.Errorf("image not known"),
			wantErr:   true,
		},
		{
			name:      "offline with corrupt local manifest",
			remoteErr: fmt.Errorf("network error"),
			localOut:  []byte("not json"),
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			r := checkImageFake(t, &calls, tt.remoteOut, tt.remoteErr, tt.localOut, tt.localErr)
			err := CheckImage(context.Background(), r, "ghcr.io/tuna-os/yellowfin:gnome-hwe")
			if tt.wantErr && err == nil {
				t.Fatal("CheckImage() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("CheckImage() error = %v", err)
			}
			want := []call{
				{name: "skopeo", args: []string{"inspect", "docker://ghcr.io/tuna-os/yellowfin:gnome-hwe"}},
				{name: "skopeo", args: []string{"inspect", "containers-storage:ghcr.io/tuna-os/yellowfin:gnome-hwe"}},
			}
			if len(calls) != len(want) {
				t.Fatalf("CheckImage() made calls %v, want %v", calls, want)
			}
			for i := range want {
				if calls[i].name != want[i].name || !slices.Equal(calls[i].args, want[i].args) {
					t.Errorf("call %d = %s %q, want %s %q", i, calls[i].name, calls[i].args, want[i].name, want[i].args)
				}
			}
		})
	}
}

// TestCheckImage_StripsTransportPrefix mirrors fisherman's bareImageRef
// behavior: a recipe image ref carrying a transport prefix must not be
// double-prepended.
func TestCheckImage_StripsTransportPrefix(t *testing.T) {
	var calls []call
	r := checkImageFake(t, &calls, []byte(`{"Digest":"d","Layers":[]}`), nil, nil, fmt.Errorf("not known"))
	if err := CheckImage(context.Background(), r, "containers-storage:ghcr.io/ublue-os/bluefin:stable"); err != nil {
		t.Fatalf("CheckImage() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
	if got, want := calls[0].args[1], "docker://ghcr.io/ublue-os/bluefin:stable"; got != want {
		t.Errorf("remote ref = %q, want %q", got, want)
	}
	if got, want := calls[1].args[1], "containers-storage:ghcr.io/ublue-os/bluefin:stable"; got != want {
		t.Errorf("local ref = %q, want %q", got, want)
	}
}
