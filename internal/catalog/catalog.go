// Package catalog is the image catalog the interactive frontends offer, and
// the supplementary user groups they propose. The TUI and the graphical
// installer share it (ADR-0021) so both offer the same images, trust tuples
// and groups. The built-in list covers the bootc images; an override file
// replaces it wholesale. The override mechanism follows fisherman's precedent
// of images.json at /etc/tuna-installer/images.json (frostyard/fisherman,
// GPL-3.0-only; see NOTICE).
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/frostyard/firn/internal/recipe"
)

// OverridePath, when present, replaces the built-in catalog.
const OverridePath = "/etc/firn/catalog.json"

// BuiltinCosignPubKey is where the single installer ISO ships the snosi
// image-signing public key. Built-in bootc entries carry it into the
// generated recipe so interactive installs take the same explicit trust path
// as headless recipes.
const BuiltinCosignPubKey = "/usr/lib/snosi/cosign.pub"

// Entry is one installable image the wizards offer. Ref is set for bootc
// entries.
type Entry struct {
	Family       string `json:"family"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Ref          string `json:"ref,omitempty"`
	CosignPubKey string `json:"cosign_pub_key,omitempty"`
	// DefaultGroups preselects the wizards' group choices for this entry
	// (the user can still change the selection; join-where-exists
	// semantics at install time are unchanged). Empty means the generic
	// fallback ({"sudo"}). Every name listed here must also be one of
	// Groups -- the TUI's huh MultiSelect drops selected values it has no
	// option for.
	DefaultGroups []string `json:"default_groups,omitempty"`
}

// Default group sets for the builtin entries, mirrored by snosi's shipped
// /etc/firn/catalog.json (frostyard/snosi#789): a fresh desktop user
// otherwise lands in only [user, sudo] -- fine for the logind-managed seat,
// but printer admin (lpadmin), log reading (adm), and non-seat device
// access (video/input/render/plugdev) all break later. audio and dialout
// are deliberately NOT preselected: audio group membership is discouraged
// under logind, dialout is niche -- both stay offered for opting in.
var (
	desktopDefaultGroups = []string{"sudo", "adm", "video", "input", "render", "plugdev", "netdev", "lpadmin", "scanner"}
	serverDefaultGroups  = []string{"sudo", "adm", "netdev"}
)

// Builtin is the default snosi image catalog. Descriptions match the snosi
// README's image table.
func Builtin() []Entry {
	return []Entry{
		{Family: recipe.FamilyBootc, Name: "snow", Description: "GNOME desktop with backports kernel", Ref: "ghcr.io/frostyard/snow:latest", CosignPubKey: BuiltinCosignPubKey, DefaultGroups: desktopDefaultGroups},
		{Family: recipe.FamilyBootc, Name: "snowfield", Description: "GNOME desktop with linux-surface kernel for Surface devices", Ref: "ghcr.io/frostyard/snowfield:latest", CosignPubKey: BuiltinCosignPubKey, DefaultGroups: desktopDefaultGroups},
		{Family: recipe.FamilyBootc, Name: "floe", Description: "Headless server with podman and backports kernel", Ref: "ghcr.io/frostyard/floe:latest", CosignPubKey: BuiltinCosignPubKey, DefaultGroups: serverDefaultGroups},
	}
}

// Load returns the image catalog: the override file when present and valid,
// the built-in list otherwise. A non-nil warn means the override existed but
// was unusable — the built-ins are returned and the caller should surface the
// warning loudly rather than install from a silently wrong list.
func Load() (entries []Entry, warn error) {
	return LoadFrom(OverridePath)
}

// LoadFrom implements Load against an explicit path, for tests.
func LoadFrom(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Builtin(), nil
	}
	if err != nil {
		return Builtin(), fmt.Errorf("catalog override %s: %w (using built-in catalog)", path, err)
	}
	var entries []Entry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return Builtin(), fmt.Errorf("catalog override %s: %w (using built-in catalog)", path, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Builtin(), fmt.Errorf("catalog override %s: trailing JSON (using built-in catalog)", path)
	}
	if err := Check(entries); err != nil {
		return Builtin(), fmt.Errorf("catalog override %s: %w (using built-in catalog)", path, err)
	}
	return entries, nil
}

// Check fail-closed-validates catalog entries: a wizard must not offer an
// entry that cannot produce a valid recipe.
func Check(entries []Entry) error {
	if len(entries) == 0 {
		return errors.New("catalog is empty")
	}
	for i, e := range entries {
		if e.Name == "" {
			return fmt.Errorf("entry %d: name is required", i)
		}
		if e.Family != recipe.FamilyBootc {
			return fmt.Errorf("entry %q: family must be %q, got %q", e.Name, recipe.FamilyBootc, e.Family)
		}
		img := recipe.Image{
			Family: e.Family, Ref: e.Ref, CosignPubKey: e.CosignPubKey,
		}
		if issues := recipe.ValidateImageSelection(img); len(issues) > 0 {
			return fmt.Errorf("entry %q: %s", e.Name, issues[0])
		}
	}
	return nil
}

// Group is one supplementary group the wizards offer for the first user.
type Group struct {
	Name string
	// Description is a short gloss shown beside the name; empty when the
	// name says enough.
	Description string
}

// Groups is the single source of the wizards' offered group set: common
// groups plus the ones snosi's sysexts provide. Every group a catalog
// entry's default_groups may preselect MUST be listed here (pinned by
// TestGroupsCoverBuiltinDefaults). Groups missing from the chosen image are
// skipped at install time.
func Groups() []Group {
	return []Group{
		{"sudo", "administrator"},
		{"docker", "run containers without sudo"},
		{"incus-admin", "manage incus VMs/containers"},
		{"lpadmin", "manage printers"},
		{"scanner", "use scanners"},
		{"adm", "read system logs"},
		{"audio", ""},
		{"video", ""},
		{"input", "raw input devices"},
		{"render", "GPU compute/render nodes"},
		{"dialout", "serial devices"},
		{"plugdev", "removable devices"},
		{"netdev", "network configuration"},
	}
}
