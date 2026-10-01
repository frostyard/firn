// Package recipe loads and validates firn's TOML recipe — the sole
// configuration input, per docs/specs/recipe-schema.md (version 2).
// Validation is fail-closed: unknown fields and enum values are errors.
package recipe

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// SchemaVersion is the recipe schema version this package implements.
const SchemaVersion = 2

// SchemaVersionV1Bootc remains accepted during the bootc migration window.
const SchemaVersionV1Bootc = 1

// Families.
const (
	FamilyBootc = "bootc"
)

// Recipe is the top-level schema. Field presence (as opposed to zero
// values) is tracked via toml.MetaData in Loaded.
type Recipe struct {
	Version  int      `toml:"version"`
	Image    Image    `toml:"image"`
	Target   Target   `toml:"target"`
	Security Security `toml:"security"`
	System   System   `toml:"system"`
}

// Image selects what to install. family is never inferred (ADR-0005).
//
// Optional leaves carry omitempty so that wizard-written recipes round-trip.
type Image struct {
	Family string `toml:"family,omitempty"`

	Ref          string `toml:"ref,omitempty"`
	TargetRef    string `toml:"target_ref,omitempty"`
	CosignPubKey string `toml:"cosign_pub_key,omitempty"`
}

// Target selects the disk and (where genuinely variable) the layout.
type Target struct {
	Disk string `toml:"disk,omitempty"`

	Filesystem      string `toml:"filesystem,omitempty"`
	BtrfsSubvolumes bool   `toml:"btrfs_subvolumes,omitempty"`
	Bootloader      string `toml:"bootloader,omitempty"`
}

// Security holds the always-explicit security choices (ADR-0004).
type Security struct {
	Encryption     string `toml:"encryption,omitempty"`
	Passphrase     string `toml:"passphrase,omitempty"`
	PassphraseFile string `toml:"passphrase_file,omitempty"`

	Mok             string `toml:"mok,omitempty"`
	MokPasswordFile string `toml:"mok_password_file,omitempty"`
}

// System is the post-install configuration surface (ADR-0004).
type System struct {
	Hostname                 string   `toml:"hostname,omitempty"`
	Locale                   string   `toml:"locale,omitempty"`
	Timezone                 string   `toml:"timezone,omitempty"`
	Keyboard                 string   `toml:"keyboard,omitempty"`
	Flatpaks                 []string `toml:"flatpaks,omitempty"`
	CoreFlatpaks             bool     `toml:"core_flatpaks,omitempty"`
	RootSSHAuthorizedKey     string   `toml:"root_ssh_authorized_key,omitempty"`
	RootSSHAuthorizedKeyFile string   `toml:"root_ssh_authorized_key_file,omitempty"`
	User                     *User    `toml:"user,omitempty"`
}

// User describes the first user; a nil User creates no user.
type User struct {
	Name                 string   `toml:"name,omitempty"`
	Fullname             string   `toml:"fullname,omitempty"`
	PasswordFile         string   `toml:"password_file,omitempty"`
	PasswordHash         string   `toml:"password_hash,omitempty"`
	Groups               []string `toml:"groups,omitempty"`
	SSHAuthorizedKey     string   `toml:"ssh_authorized_key,omitempty"`
	SSHAuthorizedKeyFile string   `toml:"ssh_authorized_key_file,omitempty"`
}

// Loaded pairs a decoded Recipe with the decode metadata needed to
// distinguish "absent" from "zero value" during validation.
type Loaded struct {
	Recipe   Recipe
	meta     toml.MetaData
	legacyAB bool
}

// Marshal encodes a recipe using the canonical schema representation. The
// omitempty tags on optional fields avoid emitting absent leaves.
func Marshal(r *Recipe) ([]byte, error) {
	return toml.Marshal(*r)
}

// IsSet reports whether the given dotted key path appeared in the file.
func (l *Loaded) IsSet(key ...string) bool { return l.meta.IsDefined(key...) }

// Load reads and decodes a recipe file. Decode errors and unknown
// fields are reported here (spec rule 1); semantic checks live in
// Validate.
func Load(path string) (*Loaded, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("recipe: %w", err)
	}
	return Parse(data)
}

// Parse decodes recipe bytes. See Load.
func Parse(data []byte) (*Loaded, error) {
	// Recognize only the explicit v1 A/B discriminator before strict decode,
	// so Validate can give a targeted rejection without accepting its old keys.
	var discriminator struct {
		Version int
		Image   struct{ Family string }
	}
	if _, err := toml.Decode(string(data), &discriminator); err != nil {
		return nil, fmt.Errorf("recipe: %w", err)
	}
	if discriminator.Version == SchemaVersionV1Bootc && discriminator.Image.Family == "ab" {
		return &Loaded{Recipe: Recipe{Version: discriminator.Version, Image: Image{Family: discriminator.Image.Family}}, legacyAB: true}, nil
	}
	var r Recipe
	meta, err := toml.Decode(string(data), &r)
	if err != nil {
		return nil, fmt.Errorf("recipe: %w", err)
	}
	if undec := meta.Undecoded(); len(undec) > 0 {
		return nil, fmt.Errorf("recipe: unknown field %q (fail-closed: unknown fields are errors)", undec[0].String())
	}
	return &Loaded{Recipe: r, meta: meta}, nil
}
