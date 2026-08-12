// Package flatpak provisions system flatpaks into the mounted target at
// install time, offline-first: flatpaks the installer medium already
// carries are copied in, the remainder is downloaded directly into the
// target, and anything unreachable is returned to the caller for the
// install summary — never silently dropped, never fatal
// (docs/adr/0006-install-time-offline-first-flatpaks.md).
//
// Ported from frostyard/fisherman (GPL-3.0-only),
// fisherman/internal/post/post.go (CopyFlatpaks).
package flatpak

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frostyard/firn/internal/runner"
)

// hostFlatpakDir is the installer medium's own system flatpak
// installation, seeded at ISO build time (ADR-0006: media builds seed
// the core set so the offline path is the common path, not the
// degraded one).
const hostFlatpakDir = "/var/lib/flatpak"

// tarPipeScript streams the medium's flatpak tree into the target,
// preserving hardlinks and ownership. Fisherman piped `tar cf -` into
// `tar xf -` through an in-process io.Pipe (post.go: tarC/tarX); firn's
// runner seam has no stdin/stdout plumbing, so the same pipe runs
// inside a single sh invocation. $1 is the source dir, $2 the
// destination; the pipeline's exit status is tar xf's, and a mid-stream
// create failure surfaces as a truncated-archive extract error.
const tarPipeScript = `tar cf - -C "$1" . | tar xf - -C "$2"`

// Opts configures Provision.
type Opts struct {
	// TargetDir is the mounted target root. The target's flatpak
	// installation is resolved beneath it (see targetFlatpakDir).
	TargetDir string
	// InstallationDir, when set, is the target flatpak installation
	// directory itself, bypassing resolution — the A/B path mounts the
	// var FILESYSTEM (runtime /var) rather than a full target root, so
	// its installation lives at <varMount>/lib/flatpak, which the
	// TargetDir heuristics cannot derive.
	InstallationDir string
	// Apps is the recipe's explicit list of flatpak app IDs to
	// provision, e.g. "org.mozilla.firefox".
	Apps []string
}

// flathubRepo is the remote added to the target installation so
// downloads have a source even on a bare target — same remote
// snosi-firstboot added on first boot (snosi-install lineage,
// snosi-firstboot lines 83-84); firn does it at install time per
// ADR-0006.
const flathubRepo = "https://dl.flathub.org/repo/flathub.flatpakrepo"

// Provision installs the requested flatpak apps into the mounted target:
//
//  1. Flatpaks already present in the medium's system installation are
//     carried over by copying /var/lib/flatpak into the target via a
//     tar pipe; wanted refs installed only per-user on the medium are
//     first promoted to the system installation so the copy picks them
//     up.
//  2. Wanted apps the medium does not carry are downloaded during the
//     install, directly into the target installation.
//  3. Apps that cannot be reached (offline install, download failure)
//     are returned in unreachable for the install summary — never
//     silently dropped, never fatal (ADR-0006 report-don't-fail).
//
// err is reserved for structural failures: missing target root, failure
// to create the target installation dir, or the tar copy of flatpaks
// that ARE present on the medium failing.
//
// Divergence from fisherman: CopyFlatpaks substituted a hardcoded
// fallback app set (post.go fallbackFlatpaks: firefox, Console,
// TextEditor) when the recipe listed none. Firn's recipe is explicit —
// an empty Apps list means nothing to provision, and Provision returns
// immediately without touching the target.
func Provision(ctx context.Context, r *runner.Runner, o Opts) (unreachable []string, err error) {
	if o.TargetDir == "" {
		return nil, fmt.Errorf("flatpak: target dir not set")
	}
	if _, statErr := os.Stat(o.TargetDir); statErr != nil {
		return nil, fmt.Errorf("flatpak: target dir: %w", statErr)
	}
	if len(o.Apps) == 0 {
		// No fallback set — see the divergence note above.
		return nil, nil
	}

	wantedSet := make(map[string]bool, len(o.Apps))
	for _, app := range o.Apps {
		wantedSet[app] = true
	}

	// Target /var/lib/flatpak: resolve the writable var path.
	dst := o.InstallationDir
	if dst == "" {
		dst = targetFlatpakDir(ctx, r, o.TargetDir)
	}
	if _, mkErr := r.Run(ctx, "mkdir", "-p", dst); mkErr != nil {
		return nil, fmt.Errorf("flatpak: mkdir %s: %w", dst, mkErr)
	}

	// 1. Collect all system-installed flatpak refs (apps + runtimes)
	//    on the medium, plus per-user app refs.
	sysAll := flatpakList(ctx, r, "--system", "")
	userApps := flatpakList(ctx, r, "--user", "--app")

	// 2. Figure out which user-only app refs are not in the system
	//    install, filtered to only wanted apps.
	sysSet := make(map[string]bool, len(sysAll))
	for _, ref := range sysAll {
		sysSet[ref] = true
	}
	var userOnly []string
	for _, ref := range userApps {
		if !sysSet[ref] && wantedSet[flatpakAppName(ref)] {
			userOnly = append(userOnly, ref)
		}
	}

	// 3. Install user-only app refs into the system repo so tar picks
	//    them up. Fisherman treated a failed promotion as a warning;
	//    here the miss surfaces through the download step below and,
	//    if that also fails, in unreachable.
	for _, ref := range userOnly {
		_, _ = r.Run(ctx, "flatpak", "install", "--system", "-y", "--noninteractive", ref)
	}

	// 4. Copy the medium's system flatpak directory to the target via
	//    the tar pipe. The whole tree is copied (not just wanted apps)
	//    so runtimes and dependencies come along, exactly as fisherman
	//    did. A medium with no flatpak data simply skips the copy —
	//    everything wanted then goes through the download step.
	if dirSize(ctx, r, hostFlatpakDir) > 0 {
		if _, tarErr := r.Run(ctx, "sh", "-c", tarPipeScript, "sh", hostFlatpakDir, dst); tarErr != nil {
			// Structural: these flatpaks ARE present on the medium;
			// failing to carry them over is a broken install, not an
			// unreachable app.
			return nil, fmt.Errorf("flatpak: tar copy %s -> %s: %w", hostFlatpakDir, dst, tarErr)
		}
	}

	// 5. Download the remainder directly into the target installation.
	//    Re-list the system apps after promotion (fisherman post.go:374
	//    re-lists for the same reason): whatever is in the system repo
	//    now is what the tar copy delivered.
	present := make(map[string]bool)
	for _, ref := range flatpakList(ctx, r, "--system", "--app") {
		present[flatpakAppName(ref)] = true
	}
	var needDownload []string
	for _, app := range o.Apps {
		if !present[app] {
			needDownload = append(needDownload, app)
		}
	}
	if len(needDownload) > 0 {
		// A bare target installation (nothing tar-copied, or a medium
		// with no flatpak data) has NO remotes, so downloads would all
		// fail as "unreachable" for the wrong reason. Add flathub
		// --if-not-exists first; if this itself fails (offline), the
		// downloads below fail individually and land in unreachable —
		// ADR-0006's report-don't-fail covers both.
		_, _ = r.Run(ctx, "env", "FLATPAK_SYSTEM_DIR="+dst,
			"flatpak", "remote-add", "--system", "--if-not-exists", "flathub", flathubRepo)
	}
	for _, app := range needDownload {
		// FLATPAK_SYSTEM_DIR points flatpak's system installation at
		// the mounted target, so the download lands in the target's
		// repo. env(1) carries the variable because the runner seam has
		// no environment injection.
		if _, dlErr := r.Run(ctx, "env", "FLATPAK_SYSTEM_DIR="+dst,
			"flatpak", "install", "--system", "-y", "--noninteractive", app); dlErr != nil {
			// ADR-0006: with no network the install still succeeds;
			// the app is REPORTED for the summary, never fatal.
			unreachable = append(unreachable, app)
		}
	}
	return unreachable, nil
}

// targetFlatpakDir resolves the writable flatpak installation inside
// the mounted target. Fisherman's priority (post.go CopyFlatpaks):
// explicit recipe override > composefs-native "state/os/default/var" >
// ostree/bootc "ostree/deploy/default/var". Firn's recipe carries no
// override, and firn also installs onto A/B targets whose mounted root
// has a plain /var — so the resolution here is composefs-native >
// ostree > plain var/lib/flatpak.
//
// Composefs detection is POSITIVE, via the composefs-native deploy base
// <target>/state/deploy/<hash>/. Fisherman's earlier heuristic keyed
// off "/ostree absent", but the composefs-native backend ALSO creates
// an /ostree directory — so that check mis-classified composefs-native
// installs as ostree, sending post-install writes down the ostree path
// (`ostree admin --print-current-dir`, which exits 1 on a
// freshly-installed --skip-finalize target, and whose glob fallback
// over /ostree/deploy/*/deploy/* finds nothing because the deployment
// is under /state/) — a fatal crash on composefs images.
//
// Divergence from fisherman: its legacy fallback signal ("no /ostree at
// all is composefs") is replaced by the plain-var branch, because on
// firn a target with neither marker is an A/B root with a real /var,
// not a composefs deployment.
func targetFlatpakDir(ctx context.Context, r *runner.Runner, target string) string {
	// Use ls via runner (not os.Stat), which runs in the host mount
	// namespace rather than any sandbox. composefs-native ⟺
	// state/deploy exists.
	if _, err := r.Run(ctx, "ls", filepath.Join(target, "state", "deploy")); err == nil {
		return filepath.Join(target, "state", "os", "default", "var", "lib", "flatpak")
	}
	if _, err := r.Run(ctx, "ls", filepath.Join(target, "ostree")); err == nil {
		return filepath.Join(target, "ostree", "deploy", "default", "var", "lib", "flatpak")
	}
	return filepath.Join(target, "var", "lib", "flatpak")
}

// flatpakAppName extracts a short display name from a flatpak ref like
// "org.mozilla.Firefox/x86_64/stable" → "org.mozilla.Firefox".
func flatpakAppName(ref string) string {
	if idx := strings.IndexByte(ref, '/'); idx > 0 {
		return ref[:idx]
	}
	return ref
}

// flatpakList returns installed flatpak refs for the given installation
// flag (--system or --user) and optional type filter (--app, --runtime,
// or ""). A probe failure (no flatpak on the medium) is an empty list,
// not an error — the download step covers the wanted apps.
func flatpakList(ctx context.Context, r *runner.Runner, installFlag, typeFilter string) []string {
	fargs := []string{"list", installFlag, "--columns=ref"}
	if typeFilter != "" {
		fargs = append(fargs, typeFilter)
	}
	out, err := r.Run(ctx, "flatpak", fargs...)
	if err != nil {
		return nil
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "Ref" {
			refs = append(refs, line)
		}
	}
	return refs
}

// dirSize returns the total size in bytes of a directory tree.
// Fast path: use du -sb which handles hardlinks correctly.
func dirSize(ctx context.Context, r *runner.Runner, path string) int64 {
	out, err := r.Run(ctx, "du", "-sb", path)
	if err != nil {
		return 0
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return 0
	}
	n, _ := strconv.ParseInt(fields[0], 10, 64)
	return n
}

// coreJSONPath is where snosi images publish their core flatpak set.
// The file is owned by the frostyard/first-setup repository and ships
// in its package — the single source of truth for the system flatpaks
// firn installs with core_flatpaks (snosi-firstboot line 36 read the
// same file on first boot before firn took the install-time role).
const coreJSONPath = "usr/share/org.frostyard.FirstSetup/snow_first_setup/core.json"

// InstallerCoreJSONPath is where the single-installer ISO embeds the
// same core flatpak list, read as a fallback when the deployment does
// not expose one. Composefs-native bootc images (every snosi desktop
// image) keep /usr in composefs objects that are not readable as plain
// files at install time, so CoreSet against the deployment root returns
// ok=false and core_flatpaks would install nothing. The ISO build
// (snosi firn-installer) copies first-setup's core.json here so the list
// is always readable. A var so tests can point it elsewhere.
var InstallerCoreJSONPath = "/usr/share/firn/core-flatpaks.json"

// CoreSet reads the image-defined core flatpak app IDs from a mounted
// image root (bootc deployment root or A/B erofs root). Images that
// publish no readable core set (e.g. server images without
// snow-first-setup, or composefs deployments whose /usr is not
// materialized at install time) return ok=false — the caller falls back
// to InstallerCoreSet, then reports it, per the recipe's core_flatpaks
// contract ("where the image family publishes one"). The list is
// deduplicated: it is human-maintained and has carried duplicates
// (snosi-firstboot's sort -u comment).
func CoreSet(imageRoot string) (ids []string, ok bool, err error) {
	return parseCoreJSON(filepath.Join(imageRoot, coreJSONPath))
}

// InstallerCoreSet reads the core flatpak list the installer medium
// embedded at InstallerCoreJSONPath, if present. Same result shape as
// CoreSet; a missing file returns ok=false.
func InstallerCoreSet() (ids []string, ok bool, err error) {
	return parseCoreJSON(InstallerCoreJSONPath)
}

// parseCoreJSON reads and deduplicates the core flatpak app IDs from a
// core.json at path. A missing file is not an error (ok=false).
func parseCoreJSON(path string) (ids []string, ok bool, err error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flatpak: reading core set: %w", readErr)
	}
	var parsed struct {
		Core []struct {
			ID string `json:"id"`
		} `json:"core"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, false, fmt.Errorf("flatpak: parsing core.json: %w", err)
	}
	seen := map[string]bool{}
	for _, e := range parsed.Core {
		if e.ID != "" && !seen[e.ID] {
			seen[e.ID] = true
			ids = append(ids, e.ID)
		}
	}
	return ids, true, nil
}
