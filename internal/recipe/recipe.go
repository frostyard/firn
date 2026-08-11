// Package recipe loads and validates firn's TOML recipe — the sole
// configuration input, per docs/specs/recipe-schema.md (version 1).
// Validation is fail-closed: unknown fields, unknown enum values, and
// fields belonging to the other image family are errors.
package recipe

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// SchemaVersion is the recipe schema version this package implements.
const SchemaVersion = 1

// Families.
const (
	FamilyBootc = "bootc"
	FamilyAB    = "ab"
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
type Image struct {
	Family string `toml:"family"`

	// bootc-only fields.
	Ref          string `toml:"ref"`
	TargetRef    string `toml:"target_ref"`
	CosignPubKey string `toml:"cosign_pub_key"`

	// ab-only fields.
	Product string `toml:"product"`
	Origin  string `toml:"origin"`
	Release string `toml:"release"`
}

// Target selects the disk and (where genuinely variable) the layout.
type Target struct {
	Disk string `toml:"disk"`

	// bootc-only fields.
	Filesystem      string `toml:"filesystem"`
	BtrfsSubvolumes bool   `toml:"btrfs_subvolumes"`
	Bootloader      string `toml:"bootloader"`

	// ab-only fields (ADR-0008).
	VarFilesystem string `toml:"var_filesystem"`
	VarSubvolumes bool   `toml:"var_subvolumes"`
}

// Security holds the always-explicit security choices (ADR-0004).
type Security struct {
	Encryption     string `toml:"encryption"`
	Passphrase     string `toml:"passphrase"`
	PassphraseFile string `toml:"passphrase_file"`

	// ab-only fields.
	RecoveryKeyOut  string `toml:"recovery_key_out"`
	Mok             string `toml:"mok"`
	MokPasswordFile string `toml:"mok_password_file"`
}

// System is the post-install configuration surface (ADR-0004).
type System struct {
	Hostname                 string   `toml:"hostname"`
	Locale                   string   `toml:"locale"`
	Timezone                 string   `toml:"timezone"`
	Keyboard                 string   `toml:"keyboard"`
	Flatpaks                 []string `toml:"flatpaks"`
	CoreFlatpaks             bool     `toml:"core_flatpaks"`
	RootSSHAuthorizedKey     string   `toml:"root_ssh_authorized_key"`
	RootSSHAuthorizedKeyFile string   `toml:"root_ssh_authorized_key_file"`
	User                     *User    `toml:"user"`
}

// User describes the first user; a nil User creates no user.
type User struct {
	Name                 string   `toml:"name"`
	Fullname             string   `toml:"fullname"`
	PasswordFile         string   `toml:"password_file"`
	PasswordHash         string   `toml:"password_hash"`
	Groups               []string `toml:"groups"`
	SSHAuthorizedKey     string   `toml:"ssh_authorized_key"`
	SSHAuthorizedKeyFile string   `toml:"ssh_authorized_key_file"`
}

// Loaded pairs a decoded Recipe with the decode metadata needed to
// distinguish "absent" from "zero value" during validation.
type Loaded struct {
	Recipe Recipe
	meta   toml.MetaData
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
