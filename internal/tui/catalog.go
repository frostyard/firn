package tui

// The image catalog offered by the wizard. The built-in list covers the
// bootc images;
// an override file replaces it wholesale. The override mechanism follows
// fisherman's precedent of images.json at /etc/tuna-installer/images.json
// (frostyard/fisherman, GPL-3.0-only; see NOTICE).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/frostyard/firn/internal/recipe"
)

// catalogOverridePath, when present, replaces the built-in catalog.
const catalogOverridePath = "/etc/firn/catalog.json"

// The single installer ISO ships the snosi image-signing public key here.
// Built-in bootc entries carry it into the generated recipe so interactive
// installs take the same explicit trust path as headless recipes.
const builtinCosignPubKey = "/usr/lib/snosi/cosign.pub"

// CatalogEntry is one installable image the wizard offers. Ref is set
// for bootc entries.
type CatalogEntry struct {
	Family       string `json:"family"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Ref          string `json:"ref,omitempty"`
	CosignPubKey string `json:"cosign_pub_key,omitempty"`
	// DefaultGroups preselects the wizard's group multi-select for this
	// entry (the user can still change the selection; join-where-exists
	// semantics at install time are unchanged). Empty means the generic
	// fallback ({"sudo"}). Every name listed here must also be an offered
	// option in the wizard's group form -- huh's MultiSelect drops
	// selected values it has no option for.
	DefaultGroups []string `json:"default_groups,omitempty"`
}

// builtinCatalog is the default snosi image catalog. Descriptions match
// the snosi README's image table.
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

func builtinCatalog() []CatalogEntry {
	return []CatalogEntry{
		{Family: recipe.FamilyBootc, Name: "snow", Description: "GNOME desktop with backports kernel", Ref: "ghcr.io/frostyard/snow:latest", CosignPubKey: builtinCosignPubKey, DefaultGroups: desktopDefaultGroups},
		{Family: recipe.FamilyBootc, Name: "snowfield", Description: "GNOME desktop with linux-surface kernel for Surface devices", Ref: "ghcr.io/frostyard/snowfield:latest", CosignPubKey: builtinCosignPubKey, DefaultGroups: desktopDefaultGroups},
		{Family: recipe.FamilyBootc, Name: "floe", Description: "Headless server with podman and backports kernel", Ref: "ghcr.io/frostyard/floe:latest", CosignPubKey: builtinCosignPubKey, DefaultGroups: serverDefaultGroups},
	}
}

// LoadCatalog returns the image catalog: the override file when present
// and valid, the built-in list otherwise. A non-nil warn means the
// override existed but was unusable — the built-ins are returned and the
// caller should surface the warning loudly rather than install from a
// silently wrong list.
func LoadCatalog() (entries []CatalogEntry, warn error) {
	return loadCatalogFrom(catalogOverridePath)
}

// loadCatalogFrom implements LoadCatalog against an explicit path, for
// tests.
func loadCatalogFrom(path string) ([]CatalogEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return builtinCatalog(), nil
	}
	if err != nil {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: %w (using built-in catalog)", path, err)
	}
	var entries []CatalogEntry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: %w (using built-in catalog)", path, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: trailing JSON (using built-in catalog)", path)
	}
	if err := checkCatalog(entries); err != nil {
		return builtinCatalog(), fmt.Errorf("tui: catalog override %s: %w (using built-in catalog)", path, err)
	}
	return entries, nil
}

// checkCatalog fail-closed-validates override entries: the wizard must
// not offer an entry that cannot produce a valid recipe.
func checkCatalog(entries []CatalogEntry) error {
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

// formatCatalogOption renders one catalog entry as a picker line,
// including its one-line description.
func formatCatalogOption(e CatalogEntry) string {
	return strings.TrimSpace(fmt.Sprintf("%-14s (bootc image) %s", e.Name, e.Description))
}
