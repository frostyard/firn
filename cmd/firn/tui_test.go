package firn

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/pipeline"
	"github.com/frostyard/firn/internal/platform"
	"github.com/frostyard/firn/internal/recipe"
	"github.com/frostyard/firn/internal/tui"
)

func commandTUIRecipe(t *testing.T) (*recipe.Recipe, *recipe.Loaded) {
	t.Helper()
	l, err := recipe.Parse([]byte(`
version = 1
[image]
family = "bootc"
ref = "ghcr.io/frostyard/cayo:latest"
[target]
disk = "/dev/vda"
filesystem = "btrfs"
[security]
encryption = "none"
[system]
hostname = "frost01"
`))
	if err != nil {
		t.Fatal(err)
	}
	return &l.Recipe, l
}

func TestRunTUIRejectsNonUEFIBeforeWizardOrPipeline(t *testing.T) {
	displayed := errors.New("displayed")
	var shown error
	wizardCalls := 0
	rt := tuiRuntime{
		holdError: func(_ context.Context, title string, err error) error {
			if title != "unsupported machine" {
				t.Fatalf("error title = %q, want unsupported machine", title)
			}
			shown = err
			return displayed
		},
		runWizard: func(context.Context, tui.WizardOpts) (*recipe.Recipe, error) {
			wizardCalls++
			return nil, nil
		},
	}

	err := runTUIWithRuntime(context.Background(), tuiOptions{
		secureBoot: "off",
		tpm:        "off",
		uefi:       "off",
	}, rt)
	if !errors.Is(err, displayed) {
		t.Fatalf("runTUIWithRuntime error = %v, want displayed sentinel", err)
	}
	if !errors.Is(shown, platform.ErrUEFIRequired) {
		t.Fatalf("displayed error = %v, want shared UEFI diagnostic", shown)
	}
	if wizardCalls != 0 {
		t.Fatalf("wizard called %d times on unsupported machine; confirmation and pipeline became reachable", wizardCalls)
	}
}

func TestRunTUISetupReachesWizardAndQuitStopsBridge(t *testing.T) {
	var got tui.WizardOpts
	root := t.TempDir()
	rt := tuiRuntime{
		holdError: func(_ context.Context, _ string, err error) error {
			t.Fatalf("unexpected held error: %v", err)
			return err
		},
		runWizard: func(_ context.Context, opts tui.WizardOpts) (*recipe.Recipe, error) {
			got = opts
			return nil, nil // interactive quit
		},
		createSession: func() (string, error) { return newTUISession(root) },
		writeRecipe: func(string, *recipe.Recipe, recipe.Env) (string, *recipe.Loaded, error) {
			t.Fatal("quit wizard persisted a recipe")
			return "", nil, nil
		},
		runInstall: func(context.Context, *pipeline.Env, *recipe.Loaded) (tui.InstallResult, error) {
			t.Fatal("quit wizard launched the engine")
			return tui.InstallResult{}, nil
		},
		stderr: new(bytes.Buffer),
	}
	err := runTUIWithRuntime(context.Background(), tuiOptions{
		secureBoot: "on", tpm: "on", uefi: "on", pubring: "/keys/update.gpg",
	}, rt)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UEFI || !got.Machine.SecureBoot || !got.Machine.TPM || got.Runner == nil {
		t.Fatalf("wizard setup = %+v", got)
	}
	if got.Machine.ZoneinfoDir != "/usr/share/zoneinfo" || len(got.Catalog) == 0 {
		t.Fatalf("wizard missing environment/catalog setup: %+v", got)
	}
	if got.SessionDir == "" || filepath.Dir(got.SessionDir) != root {
		t.Fatalf("wizard session dir = %q, want unique child of %q", got.SessionDir, root)
	}
	if _, err := os.Stat(got.SessionDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("quit wizard retained abandoned session %q: %v", got.SessionDir, err)
	}
}

func TestRunTUISetupErrorsUseRuntimeErrorView(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts tuiOptions
		want string
	}{
		{name: "secure boot", opts: tuiOptions{secureBoot: "maybe", tpm: "off", uefi: "on"}, want: "--secure-boot"},
		{name: "TPM", opts: tuiOptions{secureBoot: "off", tpm: "maybe", uefi: "on"}, want: "--tpm"},
		{name: "UEFI", opts: tuiOptions{secureBoot: "off", tpm: "off", uefi: "maybe"}, want: "--uefi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var title string
			displayed := errors.New("displayed")
			err := runTUIWithRuntime(context.Background(), tc.opts, tuiRuntime{
				holdError: func(_ context.Context, gotTitle string, err error) error {
					title = gotTitle
					if !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("held error = %v, want %q", err, tc.want)
					}
					return displayed
				},
			})
			if !errors.Is(err, displayed) || title != "installer setup failed" {
				t.Fatalf("runTUI error = %v, title = %q", err, title)
			}
		})
	}
}

func TestWriteRecipeToPersistsReloadablePrivateArtifact(t *testing.T) {
	r, _ := commandTUIRecipe(t)
	dir := filepath.Join(t.TempDir(), "recipes")
	path, loaded, err := writeRecipeTo(dir, r, recipe.Env{})
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "recipe.toml") || !reflect.DeepEqual(&loaded.Recipe, r) {
		t.Fatalf("persisted recipe path=%q recipe=%+v", path, loaded.Recipe)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("recipe mode = %o, want 600", got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("recipe dir mode = %o, want 700", got)
	}
}

func TestNewTUISessionCreatesUniquePrivateDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "firn")
	first, err := newTUISession(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTUISession(root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("sequential wizard sessions reused %q", first)
	}
	for _, dir := range []string{root, first, second} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("session path %s mode = %o, want 700", dir, got)
		}
	}
}

func TestRunTUIPropagatesInstallResults(t *testing.T) {
	r, loaded := commandTUIRecipe(t)
	uiFailure := errors.New("install view failed")
	tests := []struct {
		name    string
		result  tui.InstallResult
		uiErr   error
		wantErr string
		isErr   error
	}{
		{name: "done", result: tui.InstallResult{Done: true}},
		{name: "pipeline failure", result: tui.InstallResult{Failed: true, FailedStep: "partition", ErrorMessage: "disk busy"}, wantErr: "install failed at partition: disk busy"},
		{name: "view failure", uiErr: uiFailure, isErr: uiFailure},
		{name: "cancelled", wantErr: "install cancelled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			root := t.TempDir()
			var sessionDir string
			err := runTUIWithRuntime(context.Background(), tuiOptions{
				secureBoot: "off", tpm: "off", uefi: "on", pubring: "/keys/update.gpg",
			}, tuiRuntime{
				holdError: func(_ context.Context, _ string, err error) error { return err },
				createSession: func() (string, error) {
					var err error
					sessionDir, err = newTUISession(root)
					return sessionDir, err
				},
				runWizard: func(_ context.Context, opts tui.WizardOpts) (*recipe.Recipe, error) {
					if opts.SessionDir != sessionDir {
						t.Fatalf("wizard session = %q, want %q", opts.SessionDir, sessionDir)
					}
					return r, nil
				},
				writeRecipe: func(dir string, _ *recipe.Recipe, _ recipe.Env) (string, *recipe.Loaded, error) {
					if dir != sessionDir {
						t.Fatalf("recipe session = %q, want %q", dir, sessionDir)
					}
					return filepath.Join(dir, "recipe.toml"), loaded, nil
				},
				runInstall: func(_ context.Context, env *pipeline.Env, got *recipe.Loaded) (tui.InstallResult, error) {
					if got != loaded || env.Recipe != &loaded.Recipe || env.Trust.PubringPath != "/keys/update.gpg" {
						t.Fatalf("engine bridge received env=%+v recipe=%p", env, got)
					}
					return tc.result, tc.uiErr
				},
				stderr: &stderr,
			})
			switch {
			case tc.isErr != nil && !errors.Is(err, tc.isErr):
				t.Fatalf("error = %v, want sentinel %v", err, tc.isErr)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			case tc.isErr == nil && tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stderr.String(), filepath.Join(sessionDir, "recipe.toml")) || !strings.Contains(stderr.String(), "/dev/vda") || !strings.Contains(stderr.String(), "until this installer environment reboots") {
				t.Fatalf("bridge output lost reproduction details: %q", stderr.String())
			}
			if _, err := os.Stat(sessionDir); err != nil {
				t.Fatalf("persisted wizard session was removed: %v", err)
			}
		})
	}
}

func TestReportTUIResultDoesNotLogRecoveryKey(t *testing.T) {
	const (
		theKey = "1234-ABCD-5678-EFGH"
		path   = "/run/firn/session-test/recipe.toml"
		disk   = "/dev/vda"
	)
	var stderr bytes.Buffer

	err := reportTUIResult(&stderr, tui.InstallResult{
		Done:        true,
		RecoveryKey: theKey,
	}, nil, path, disk)
	if err != nil {
		t.Fatalf("reportTUIResult: %v", err)
	}
	got := stderr.String()
	if strings.Contains(got, theKey) || strings.Contains(got, "RECOVERY KEY") {
		t.Fatalf("TUI command path logged the recovery key after RunInstall returned: %q", got)
	}
	if !strings.Contains(got, path) || !strings.Contains(got, disk) || !strings.Contains(got, "until this installer environment reboots") {
		t.Fatalf("TUI command path lost non-secret reproduction details: %q", got)
	}
}
