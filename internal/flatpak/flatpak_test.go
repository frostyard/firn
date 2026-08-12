// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/post/post_test.go (CopyFlatpaks tests).
package flatpak

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

// fakeCmds is a scriptable command table for runner.NewFake. Commands
// are keyed by their full argv joined with spaces; unknown commands
// succeed with empty output unless listed in fail.
type fakeCmds struct {
	calls [][]string
	out   map[string]string
	fail  map[string]bool
}

func (f *fakeCmds) exec(_ context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	f.calls = append(f.calls, argv)
	key := strings.Join(argv, " ")
	if f.fail[key] {
		return nil, fmt.Errorf("fake failure: %s", key)
	}
	return []byte(f.out[key]), nil
}

func (f *fakeCmds) runner() *runner.Runner {
	return runner.NewFake(f.exec, func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	})
}

// called reports whether an exact argv was invoked.
func (f *fakeCmds) called(argv ...string) bool {
	return slices.ContainsFunc(f.calls, func(c []string) bool {
		return slices.Equal(c, argv)
	})
}

// plainVarFakes returns a fake where the target has neither composefs
// nor ostree markers, so the flatpak dir resolves to var/lib/flatpak.
func plainVarFakes(target string) *fakeCmds {
	return &fakeCmds{
		out: map[string]string{},
		fail: map[string]bool{
			"ls " + filepath.Join(target, "state", "deploy"): true,
			"ls " + filepath.Join(target, "ostree"):          true,
		},
	}
}

func TestProvisionTargetMissingIsStructuralError(t *testing.T) {
	f := &fakeCmds{out: map[string]string{}, fail: map[string]bool{}}
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: missing,
		Apps:      []string{"org.mozilla.firefox"},
	})
	if err == nil {
		t.Fatal("expected structural error for missing target dir, got nil")
	}
	if unreachable != nil {
		t.Errorf("expected no unreachable apps on structural error, got %v", unreachable)
	}
	if len(f.calls) != 0 {
		t.Errorf("expected no commands run for missing target, got %v", f.calls)
	}
}

func TestProvisionEmptyAppsDoesNothing(t *testing.T) {
	// Divergence from fisherman: no hardcoded fallback set. An empty
	// recipe list provisions nothing and runs no commands.
	f := &fakeCmds{out: map[string]string{}, fail: map[string]bool{}}
	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if unreachable != nil {
		t.Errorf("expected no unreachable apps, got %v", unreachable)
	}
	if len(f.calls) != 0 {
		t.Errorf("expected no commands for empty app list, got %v", f.calls)
	}
}

func TestProvisionCopiesMediumFlatpaksViaTar(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	dst := filepath.Join(target, "var", "lib", "flatpak")
	f.out["flatpak list --system --columns=ref"] = "org.mozilla.firefox/x86_64/stable\norg.freedesktop.Platform/x86_64/24.08\n"
	f.out["flatpak list --system --columns=ref --app"] = "org.mozilla.firefox/x86_64/stable\n"
	f.out["du -sb /var/lib/flatpak"] = "123456\t/var/lib/flatpak\n"

	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox"},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if unreachable != nil {
		t.Errorf("expected no unreachable apps, got %v", unreachable)
	}
	if !f.called("mkdir", "-p", dst) {
		t.Errorf("expected mkdir -p %s; calls: %v", dst, f.calls)
	}
	if !f.called("sh", "-c", tarPipeScript, "sh", "/var/lib/flatpak", dst) {
		t.Errorf("expected tar pipe sh invocation for %s; calls: %v", dst, f.calls)
	}
	// Present on the medium: no download attempt.
	for _, c := range f.calls {
		if c[0] == "env" {
			t.Errorf("unexpected download invocation for medium-present app: %v", c)
		}
	}
}

func TestProvisionSkipsTarWhenMediumEmpty(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	f.out["du -sb /var/lib/flatpak"] = "0\t/var/lib/flatpak\n"

	if _, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox"},
	}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for _, c := range f.calls {
		if c[0] == "sh" || c[0] == "tar" {
			t.Errorf("expected no tar copy for empty medium repo, got %v", c)
		}
	}
}

// TestProvisionPromotesUserOnlyRefs mirrors fisherman's
// TestCopyFlatpaks_PromotesUserApps: a wanted app present only in the
// user installation is installed to the system repo so tar picks it up.
func TestProvisionPromotesUserOnlyRefs(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	ref := "org.gnome.Console/x86_64/stable"
	f.out["flatpak list --user --columns=ref --app"] = ref + "\n"
	// After promotion the re-list shows it in the system repo.
	f.out["flatpak list --system --columns=ref --app"] = ref + "\n"
	f.out["du -sb /var/lib/flatpak"] = "1024\t/var/lib/flatpak\n"

	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.gnome.Console"},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if unreachable != nil {
		t.Errorf("expected no unreachable apps, got %v", unreachable)
	}
	if !f.called("flatpak", "install", "--system", "-y", "--noninteractive", ref) {
		t.Errorf("expected %s to be promoted to system; calls: %v", ref, f.calls)
	}
}

func TestProvisionDoesNotPromoteUnwantedUserRefs(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	f.out["flatpak list --user --columns=ref --app"] = "org.example.Unwanted/x86_64/stable\n"
	f.out["flatpak list --system --columns=ref --app"] = "org.mozilla.firefox/x86_64/stable\n"
	f.out["du -sb /var/lib/flatpak"] = "1024\t/var/lib/flatpak\n"

	if _, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox"},
	}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	for _, c := range f.calls {
		if c[0] == "flatpak" && len(c) > 1 && c[1] == "install" {
			t.Errorf("unexpected promotion of unwanted user ref: %v", c)
		}
	}
}

func TestProvisionDownloadsMissingAppsIntoTarget(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	dst := filepath.Join(target, "var", "lib", "flatpak")
	f.out["flatpak list --system --columns=ref --app"] = "org.mozilla.firefox/x86_64/stable\n"
	f.out["du -sb /var/lib/flatpak"] = "1024\t/var/lib/flatpak\n"

	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox", "org.gnome.TextEditor"},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if unreachable != nil {
		t.Errorf("expected no unreachable apps, got %v", unreachable)
	}
	if !f.called("env", "FLATPAK_SYSTEM_DIR="+dst,
		"flatpak", "install", "--system", "-y", "--noninteractive", "org.gnome.TextEditor") {
		t.Errorf("expected download of org.gnome.TextEditor into %s; calls: %v", dst, f.calls)
	}
}

// TestProvisionOfflineReportsUnreachable is ADR-0006's core contract:
// with no network the install still succeeds and every undeliverable
// app is returned for the summary — not dropped, not fatal.
func TestProvisionOfflineReportsUnreachable(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	dst := filepath.Join(target, "var", "lib", "flatpak")
	// Empty medium, offline: every download fails.
	f.out["du -sb /var/lib/flatpak"] = "0\t/var/lib/flatpak\n"
	apps := []string{"org.mozilla.firefox", "org.gnome.TextEditor"}
	for _, app := range apps {
		f.fail["env FLATPAK_SYSTEM_DIR="+dst+" flatpak install --system -y --noninteractive "+app] = true
	}

	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      apps,
	})
	if err != nil {
		t.Fatalf("Provision must succeed offline, got: %v", err)
	}
	if !slices.Equal(unreachable, apps) {
		t.Errorf("unreachable = %v, want %v", unreachable, apps)
	}
}

func TestProvisionPartialDownloadFailure(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	dst := filepath.Join(target, "var", "lib", "flatpak")
	f.out["du -sb /var/lib/flatpak"] = "0\t/var/lib/flatpak\n"
	f.fail["env FLATPAK_SYSTEM_DIR="+dst+" flatpak install --system -y --noninteractive org.gnome.TextEditor"] = true

	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox", "org.gnome.TextEditor"},
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !slices.Equal(unreachable, []string{"org.gnome.TextEditor"}) {
		t.Errorf("unreachable = %v, want [org.gnome.TextEditor]", unreachable)
	}
	// The reachable app was still attempted.
	if !f.called("env", "FLATPAK_SYSTEM_DIR="+dst,
		"flatpak", "install", "--system", "-y", "--noninteractive", "org.mozilla.firefox") {
		t.Errorf("expected download attempt for org.mozilla.firefox; calls: %v", f.calls)
	}
}

// TestProvisionTarFailureIsStructural: flatpaks that ARE on the medium
// failing to copy is a broken install, not an unreachable app.
func TestProvisionTarFailureIsStructural(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	dst := filepath.Join(target, "var", "lib", "flatpak")
	f.out["flatpak list --system --columns=ref --app"] = "org.mozilla.firefox/x86_64/stable\n"
	f.out["du -sb /var/lib/flatpak"] = "1024\t/var/lib/flatpak\n"
	f.fail["sh -c "+tarPipeScript+" sh /var/lib/flatpak "+dst] = true

	unreachable, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox"},
	})
	if err == nil {
		t.Fatal("expected structural error when tar copy fails, got nil")
	}
	if unreachable != nil {
		t.Errorf("expected no unreachable apps on structural error, got %v", unreachable)
	}
}

func TestProvisionMkdirFailureIsStructural(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	dst := filepath.Join(target, "var", "lib", "flatpak")
	f.fail["mkdir -p "+dst] = true

	if _, err := Provision(context.Background(), f.runner(), Opts{
		TargetDir: target,
		Apps:      []string{"org.mozilla.firefox"},
	}); err == nil {
		t.Fatal("expected structural error when mkdir fails, got nil")
	}
}

// Target-path resolution, ported from fisherman's layout detection.
func TestTargetFlatpakDirComposefsNative(t *testing.T) {
	target := t.TempDir()
	// ls <target>/state/deploy succeeds (default fake behavior).
	f := &fakeCmds{out: map[string]string{}, fail: map[string]bool{}}
	got := targetFlatpakDir(context.Background(), f.runner(), target)
	want := filepath.Join(target, "state", "os", "default", "var", "lib", "flatpak")
	if got != want {
		t.Errorf("composefs-native dir = %s, want %s", got, want)
	}
	if !f.called("ls", filepath.Join(target, "state", "deploy")) {
		t.Errorf("expected positive state/deploy probe via runner ls; calls: %v", f.calls)
	}
}

func TestTargetFlatpakDirOstree(t *testing.T) {
	target := t.TempDir()
	f := &fakeCmds{out: map[string]string{}, fail: map[string]bool{
		"ls " + filepath.Join(target, "state", "deploy"): true,
	}}
	got := targetFlatpakDir(context.Background(), f.runner(), target)
	want := filepath.Join(target, "ostree", "deploy", "default", "var", "lib", "flatpak")
	if got != want {
		t.Errorf("ostree dir = %s, want %s", got, want)
	}
}

func TestTargetFlatpakDirPlainVar(t *testing.T) {
	target := t.TempDir()
	f := plainVarFakes(target)
	got := targetFlatpakDir(context.Background(), f.runner(), target)
	want := filepath.Join(target, "var", "lib", "flatpak")
	if got != want {
		t.Errorf("plain-var dir = %s, want %s", got, want)
	}
}

func TestFlatpakAppName(t *testing.T) {
	cases := map[string]string{
		"org.mozilla.Firefox/x86_64/stable": "org.mozilla.Firefox",
		"org.gnome.Console":                 "org.gnome.Console",
	}
	for ref, want := range cases {
		if got := flatpakAppName(ref); got != want {
			t.Errorf("flatpakAppName(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestFlatpakListSkipsHeaderAndBlank(t *testing.T) {
	f := &fakeCmds{out: map[string]string{
		"flatpak list --system --columns=ref --app": "Ref\norg.mozilla.firefox/x86_64/stable\n\n",
	}, fail: map[string]bool{}}
	got := flatpakList(context.Background(), f.runner(), "--system", "--app")
	if !slices.Equal(got, []string{"org.mozilla.firefox/x86_64/stable"}) {
		t.Errorf("flatpakList = %v", got)
	}
}

func TestCoreSetReadsAndDedupes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, coreJSONPath)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"core":[{"id":"org.gnome.Console"},{"id":"org.mozilla.firefox"},{"id":"org.gnome.Console"}]}`
	if err := os.WriteFile(dir, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ids, ok, err := CoreSet(root)
	if err != nil || !ok {
		t.Fatalf("CoreSet ok=%v err=%v", ok, err)
	}
	if !slices.Equal(ids, []string{"org.gnome.Console", "org.mozilla.firefox"}) {
		t.Fatalf("CoreSet ids = %v (want deduped, in order)", ids)
	}
}

func TestCoreSetMissingIsNotAnError(t *testing.T) {
	ids, ok, err := CoreSet(t.TempDir())
	if err != nil || ok || ids != nil {
		t.Fatalf("CoreSet on empty root: ids=%v ok=%v err=%v", ids, ok, err)
	}
}

// TestInstallerCoreSetFallback covers the composefs case: the deployment
// publishes no readable core set, but the installer medium embeds one.
func TestInstallerCoreSetFallback(t *testing.T) {
	// Deployment root has no core.json (mimics composefs /usr).
	if _, ok, _ := CoreSet(t.TempDir()); ok {
		t.Fatal("expected no core set from an empty deployment root")
	}
	// The installer-embedded copy is readable.
	embedded := filepath.Join(t.TempDir(), "core-flatpaks.json")
	if err := os.WriteFile(embedded, []byte(`{"core":[{"id":"org.gnome.Loupe"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := InstallerCoreJSONPath
	InstallerCoreJSONPath = embedded
	t.Cleanup(func() { InstallerCoreJSONPath = prev })

	ids, ok, err := InstallerCoreSet()
	if err != nil || !ok {
		t.Fatalf("InstallerCoreSet ok=%v err=%v", ok, err)
	}
	if !slices.Equal(ids, []string{"org.gnome.Loupe"}) {
		t.Fatalf("InstallerCoreSet ids = %v", ids)
	}
}
