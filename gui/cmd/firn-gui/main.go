// Command firn-gui is firn's graphical installer (ADR-0021): a windowed
// wizard that builds the same recipe as the TUI and runs firn's install
// pipeline in-process. It needs root to install; started without it, it
// re-runs itself through pkexec, forwarding only what a GTK window needs to
// reach the desktop. --demo runs unprivileged and never touches a disk.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/frostyard/firn/gui/internal/installer"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "local"
)

// forwardedEnv are the variables the elevated window may inherit from the
// session that launched it. pkexec clears the environment; nothing else
// crosses.
var forwardedEnv = []string{
	"WAYLAND_DISPLAY", "DISPLAY", "XAUTHORITY", "GDK_BACKEND", "GDK_SCALE",
	"GDK_DPI_SCALE", "XCURSOR_THEME", "XCURSOR_SIZE", "LANG", "LC_ALL", "LANGUAGE",
}

// envFlags collects repeated --session-env KEY=VALUE flags.
type envFlags []string

func (e *envFlags) String() string     { return strings.Join(*e, ",") }
func (e *envFlags) Set(v string) error { *e = append(*e, v); return nil }

func main() {
	fs := flag.NewFlagSet("firn-gui", flag.ExitOnError)
	demo := fs.Bool("demo", false, "simulate the machine and the install; nothing is written to disk")
	secureBoot := fs.String("secure-boot", "auto", "override Secure Boot detection (auto|on|off)")
	tpm := fs.String("tpm", "auto", "override TPM detection (auto|on|off)")
	uefi := fs.String("uefi", "auto", "override UEFI detection (auto|on|off)")
	appearance := fs.String("appearance", "", "light or dark; empty follows the desktop")
	noElevate := fs.Bool("no-elevate", false, "fail instead of re-running through pkexec when not root")
	showVersion := fs.Bool("version", false, "print the version and exit")
	var sessionEnv envFlags
	fs.Var(&sessionEnv, "session-env", "a display variable forwarded through pkexec (set by firn-gui itself)")
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("firn-gui %s (commit %s, built %s by %s)\n", version, commit, date, builtBy)
		return
	}
	for _, kv := range sessionEnv {
		k, v, ok := strings.Cut(kv, "=")
		if ok && slices.Contains(forwardedEnv, k) {
			_ = os.Setenv(k, v)
		}
	}

	if !*demo && os.Geteuid() != 0 {
		if *noElevate {
			fmt.Fprintln(os.Stderr, "firn-gui: installing needs root: run it as root, without --no-elevate, or try --demo")
			os.Exit(1)
		}
		if *appearance == "" {
			*appearance = desktopAppearance()
		}
		err := elevate(os.Args[1:], *appearance)
		fmt.Fprintf(os.Stderr, "firn-gui: %v\n", err)
		os.Exit(1)
	}

	err := installer.Run(installer.Options{
		Demo:       *demo,
		SecureBoot: *secureBoot,
		TPM:        *tpm,
		UEFI:       *uefi,
		Appearance: *appearance,
		Version:    version,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "firn-gui: %v\n", err)
		os.Exit(1)
	}
}

// elevate replaces this process with pkexec running this executable again,
// passing the original arguments and the forwarded display variables. It
// returns only on failure.
func elevate(args []string, appearance string) error {
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return fmt.Errorf("installing needs root and pkexec is not available: run firn-gui as root, or try --demo")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	argv := []string{pkexec, self}
	argv = append(argv, args...)
	if appearance != "" && !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "appearance") }) {
		argv = append(argv, "--appearance="+appearance)
	}
	for _, kv := range sessionVars(os.Getenv) {
		argv = append(argv, "--session-env="+kv)
	}
	return syscall.Exec(pkexec, argv, os.Environ())
}

// sessionVars returns the forwarded variables that are set, with a relative
// WAYLAND_DISPLAY made absolute: root keeps its own XDG_RUNTIME_DIR, so the
// socket must be named by its full path.
func sessionVars(getenv func(string) string) []string {
	var out []string
	for _, k := range forwardedEnv {
		v := getenv(k)
		if v == "" {
			continue
		}
		if k == "WAYLAND_DISPLAY" && !filepath.IsAbs(v) {
			if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
				v = filepath.Join(dir, v)
			}
		}
		out = append(out, k+"="+v)
	}
	return out
}

// desktopAppearance asks GNOME's settings for the user's light or dark
// preference, which the root process cannot read for itself.
func desktopAppearance() string {
	out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	if err != nil {
		return ""
	}
	switch strings.Trim(strings.TrimSpace(string(out)), "'") {
	case "prefer-dark":
		return "dark"
	case "prefer-light", "default":
		return "light"
	}
	return ""
}
