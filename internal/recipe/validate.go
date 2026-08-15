package recipe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/frostyard/firn/internal/bootcimg"
	"github.com/frostyard/firn/internal/trust"
)

// Env carries the install-machine facts validation depends on (spec
// rule 3). Callers probe or override these; validation never probes.
type Env struct {
	SecureBoot bool
	TPM        bool
	// ZoneinfoDir is consulted for timezone existence when non-empty
	// and present; "" skips the existence check (format still applies).
	ZoneinfoDir string
}

// Issue is one validation failure. Code is a stable identifier (one
// per spec rule family) so tests and callers can assert on the exact
// violation; Field is the dotted recipe path.
type Issue struct {
	Code    string
	Field   string
	Message string
}

func (i Issue) Error() string { return fmt.Sprintf("%s: %s (%s)", i.Field, i.Message, i.Code) }

// Issue codes.
const (
	CodeVersion     = "bad-version"
	CodeRequired    = "required"
	CodeEnum        = "enum"
	CodeFamilyScope = "family-scope"
	CodeMutex       = "mutex"
	CodeSecretFile  = "secret-file"
	CodeFile        = "file"
	CodeDisk        = "disk"
	CodeHostname    = "hostname"
	CodeLocale      = "locale"
	CodeTimezone    = "timezone"
	CodeKeyboard    = "keyboard"
	CodeUsername    = "username"
	CodeFullname    = "fullname"
	CodeGroup       = "group"
	CodeRelease     = "release"
	CodeProduct     = "product"
	CodeOrigin      = "origin"
	CodeSSHKey      = "ssh-key"
	CodeHash        = "password-hash"
	CodePassphrase  = "passphrase"
	CodeMok         = "mok"
	CodeTPM         = "tpm"
	CodeSubvolumes  = "subvolumes"
)

var (
	hostnameLabelRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	localeRe        = regexp.MustCompile(`^[a-z]{2,3}_[A-Z]{2}(\.[A-Za-z0-9-]+)?$`)
	timezoneRe      = regexp.MustCompile(`^[A-Za-z_+-]+(/[A-Za-z0-9_+-]+)*$`)
	keyboardRe      = regexp.MustCompile(`^[a-z0-9_-]+(:[a-z0-9_-]+(:[a-z0-9_-]+)?)?$`)
	usernameRe      = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	releaseRe       = regexp.MustCompile(`^[0-9]{14}$`)
	sshKeyRe        = regexp.MustCompile(`^(ssh-ed25519|ssh-rsa|ecdsa-sha2-[a-z0-9-]+|sk-[a-z0-9-]+@openssh\.com|sk-ssh-ed25519@openssh\.com)[ \t]+[A-Za-z0-9+/]+={0,3}(?:[ \t]+[^\r\n]*)?$`)
)

// bootc-only and ab-only key paths, for family scoping (spec rule 1).
var bootcOnlyKeys = [][]string{
	{"image", "ref"}, {"image", "target_ref"}, {"image", "cosign_pub_key"},
	{"target", "filesystem"}, {"target", "btrfs_subvolumes"}, {"target", "bootloader"},
}

var abOnlyKeys = [][]string{
	{"image", "product"}, {"image", "origin"}, {"image", "release"},
	{"target", "var_filesystem"}, {"target", "var_subvolumes"},
	{"security", "recovery_key_out"},
	// security.mok / mok_password_file are NOT ab-only: bootc uses them for
	// its Secure Boot secure-install path too (ADR-0014).
}

// Validate checks a loaded recipe against the schema spec and the
// install-machine environment. It returns every violation found (spec
// asks for a distinct error per rule), or nil when the recipe is valid.
func Validate(l *Loaded, env Env) []Issue {
	var issues []Issue
	add := func(code, field, format string, args ...any) {
		issues = append(issues, Issue{Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
	}
	r := &l.Recipe

	if r.Version != SchemaVersion {
		add(CodeVersion, "version", "schema version %d is not supported (this firn implements version %d)", r.Version, SchemaVersion)
	}

	family := r.Image.Family
	switch family {
	case FamilyBootc, FamilyAB:
	case "":
		add(CodeRequired, "image.family", "family is required and never inferred")
	default:
		add(CodeEnum, "image.family", "must be %q or %q, got %q", FamilyBootc, FamilyAB, family)
	}

	// Family scoping: fields of the other family are errors, not noise.
	if family == FamilyBootc {
		for _, k := range abOnlyKeys {
			if l.IsSet(k...) {
				add(CodeFamilyScope, strings.Join(k, "."), "field applies only to family %q", FamilyAB)
			}
		}
	}
	if family == FamilyAB {
		for _, k := range bootcOnlyKeys {
			if l.IsSet(k...) {
				add(CodeFamilyScope, strings.Join(k, "."), "field applies only to family %q", FamilyBootc)
			}
		}
	}

	validateImage(l, family, add)
	validateTarget(l, family, add)
	validateSecurity(l, env, family, add)
	validateSystem(l, env, add)

	return issues
}

// ValidateImageSelection applies the canonical, machine-independent image
// constraints to a prospective image selection. The TUI catalog uses this
// before displaying an entry; full recipe validation additionally checks
// field presence/scoping and files on the install machine.
func ValidateImageSelection(img Image) []Issue {
	var issues []Issue
	add := func(code, field, format string, args ...any) {
		issues = append(issues, Issue{Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
	}
	validateImageSelection(&img, add)
	return issues
}

func validateImage(l *Loaded, family string, add func(code, field, format string, args ...any)) {
	img := &l.Recipe.Image
	validateImageSelection(img, add)
	if family == FamilyBootc && img.CosignPubKey != "" {
		checkFile(img.CosignPubKey, "image.cosign_pub_key", false, add)
	}
}

func validateImageSelection(img *Image, add func(code, field, format string, args ...any)) {
	family := img.Family
	switch family {
	case FamilyBootc:
		if img.Ref == "" {
			add(CodeRequired, "image.ref", "OCI image reference is required for family %q", FamilyBootc)
		} else if strings.ContainsAny(img.Ref, " \t") {
			add(CodeEnum, "image.ref", "not a valid OCI reference")
		}
		if img.TargetRef != "" && strings.ContainsAny(img.TargetRef, " \t\r\n") {
			add(CodeEnum, "image.target_ref", "not a valid OCI reference")
		}
		if img.CosignPubKey != "" {
			if !bootcimg.IsRegistryRef(img.Ref) {
				add(CodeEnum, "image.ref", "cosign verification requires a registry image reference")
			}
		}
	case FamilyAB:
		if img.Product == "" {
			add(CodeRequired, "image.product", "A/B channel is required for family %q", FamilyAB)
		} else if err := trust.ValidateChannel(img.Product); err != nil {
			add(CodeProduct, "image.product", "%v", err)
		}
		if img.Origin != "" && !strings.HasPrefix(img.Origin, "https://") && !strings.HasPrefix(img.Origin, "http://") {
			add(CodeOrigin, "image.origin", "must be an http(s) URL")
		}
		if img.Release != "" && !releaseRe.MatchString(img.Release) {
			add(CodeRelease, "image.release", "must be a 14-digit release version")
		}
	}
}

func validateTarget(l *Loaded, family string, add func(code, field, format string, args ...any)) {
	t := &l.Recipe.Target
	if t.Disk == "" {
		add(CodeRequired, "target.disk", "target disk is required")
	} else if !strings.HasPrefix(t.Disk, "/dev/") {
		add(CodeDisk, "target.disk", "must be a whole-disk block device path under /dev/")
	}

	switch family {
	case FamilyBootc:
		switch t.Filesystem {
		case "btrfs", "xfs", "ext4":
		case "":
			add(CodeRequired, "target.filesystem", "root filesystem is required for family %q", FamilyBootc)
		default:
			add(CodeEnum, "target.filesystem", "must be btrfs, xfs, or ext4; got %q", t.Filesystem)
		}
		if t.BtrfsSubvolumes && t.Filesystem != "btrfs" {
			add(CodeSubvolumes, "target.btrfs_subvolumes", "requires filesystem = %q", "btrfs")
		}
		switch t.Bootloader {
		case "", "systemd", "grub2":
		default:
			add(CodeEnum, "target.bootloader", "must be systemd or grub2; got %q", t.Bootloader)
		}
	case FamilyAB:
		switch t.VarFilesystem {
		case "", "ext4", "btrfs":
		default:
			add(CodeEnum, "target.var_filesystem", "must be ext4 or btrfs; got %q", t.VarFilesystem)
		}
		if t.VarSubvolumes && t.VarFilesystem != "btrfs" {
			add(CodeSubvolumes, "target.var_subvolumes", "requires var_filesystem = %q", "btrfs")
		}
	}
}

func validateSecurity(l *Loaded, env Env, family string, add func(code, field, format string, args ...any)) {
	s := &l.Recipe.Security

	wantsPassphrase := false
	switch family {
	case FamilyBootc:
		switch s.Encryption {
		case "none", "tpm2-luks":
		case "luks-passphrase", "tpm2-luks-passphrase":
			wantsPassphrase = true
		case "":
			add(CodeRequired, "security.encryption", "encryption is always explicit (ADR-0004); it is never defaulted")
		default:
			add(CodeEnum, "security.encryption", "must be none, luks-passphrase, tpm2-luks, or tpm2-luks-passphrase for family %q; got %q", FamilyBootc, s.Encryption)
		}
	case FamilyAB:
		switch s.Encryption {
		case "none", "luks", "tpm2-luks":
		case "":
			add(CodeRequired, "security.encryption", "encryption is always explicit (ADR-0004); it is never defaulted")
		default:
			add(CodeEnum, "security.encryption", "must be none, luks, or tpm2-luks for family %q; got %q", FamilyAB, s.Encryption)
		}
	}

	if strings.HasPrefix(s.Encryption, "tpm2-") && !env.TPM {
		add(CodeTPM, "security.encryption", "%q requires a TPM and this machine has none (no silent fallback)", s.Encryption)
	}

	hasInline, hasFile := s.Passphrase != "", s.PassphraseFile != ""
	switch {
	case family == FamilyAB && (hasInline || hasFile):
		add(CodePassphrase, "security.passphrase", "A/B /var encryption uses a generated recovery key; passphrase fields do not apply")
	case wantsPassphrase && hasInline == hasFile: // both or neither
		add(CodePassphrase, "security.passphrase", "exactly one of passphrase or passphrase_file is required for %q", s.Encryption)
	case !wantsPassphrase && family == FamilyBootc && (hasInline || hasFile):
		add(CodePassphrase, "security.passphrase", "passphrase fields do not apply to encryption %q", s.Encryption)
	}
	if hasFile && family == FamilyBootc {
		checkFile(s.PassphraseFile, "security.passphrase_file", true, add)
	}
	if family == FamilyAB && l.IsSet("security", "recovery_key_out") {
		switch {
		case s.RecoveryKeyOut == "":
			add(CodeFile, "security.recovery_key_out", "path must not be empty when set")
		case s.Encryption == "none":
			add(CodeFile, "security.recovery_key_out", "does not apply when encryption = %q", "none")
		}
	}

	// MOK enrollment applies to BOTH families under Secure Boot: A/B stages it
	// via mokutil for shim's MokManager, and bootc does the same for its
	// secure-install schema-1 path (ADR-0014). The rules are family-independent
	// — they read only env.SecureBoot and the mok fields — so both families
	// share them.
	if family == FamilyAB || family == FamilyBootc {
		if env.SecureBoot {
			switch s.Mok {
			case "enroll":
				if s.MokPasswordFile == "" {
					add(CodeRequired, "security.mok_password_file", "required when mok = %q", "enroll")
				} else {
					checkFile(s.MokPasswordFile, "security.mok_password_file", true, add)
				}
			case "skip":
				if s.MokPasswordFile != "" {
					add(CodeMok, "security.mok_password_file", "does not apply when mok = %q", "skip")
				}
			case "":
				add(CodeRequired, "security.mok", "Secure Boot is active: mok must be %q or %q (explicit, never defaulted)", "enroll", "skip")
			default:
				add(CodeEnum, "security.mok", "must be enroll or skip; got %q", s.Mok)
			}
		} else if l.IsSet("security", "mok") || l.IsSet("security", "mok_password_file") {
			add(CodeMok, "security.mok", "Secure Boot is not active on this machine; mok fields do not apply")
		}
	}
}

func validateSystem(l *Loaded, env Env, add func(code, field, format string, args ...any)) {
	sys := &l.Recipe.System

	if sys.Hostname == "" {
		add(CodeRequired, "system.hostname", "hostname is required")
	} else if !validHostname(sys.Hostname) {
		add(CodeHostname, "system.hostname", "must be RFC 1123 host label(s), max 253 chars")
	}
	if sys.Locale != "" && !localeRe.MatchString(sys.Locale) {
		add(CodeLocale, "system.locale", "must look like ll_CC or ll_CC.ENC (e.g. en_US.UTF-8)")
	}
	if sys.Timezone != "" {
		if !timezoneRe.MatchString(sys.Timezone) || strings.HasPrefix(sys.Timezone, "/") {
			add(CodeTimezone, "system.timezone", "must be an IANA zone name (e.g. America/Chicago)")
		} else if env.ZoneinfoDir != "" {
			if _, err := os.Stat(filepath.Join(env.ZoneinfoDir, sys.Timezone)); err != nil {
				add(CodeTimezone, "system.timezone", "%q not found in zoneinfo", sys.Timezone)
			}
		}
	}
	if sys.Keyboard != "" && !keyboardRe.MatchString(sys.Keyboard) {
		add(CodeKeyboard, "system.keyboard", "must be LAYOUT[:VARIANT[:MODEL]]")
	}

	checkSSHKey(sys.RootSSHAuthorizedKey, sys.RootSSHAuthorizedKeyFile, "system.root_ssh_authorized_key", add)

	if l.IsSet("system", "user") {
		u := sys.User
		if u == nil || u.Name == "" {
			add(CodeRequired, "system.user.name", "user table requires a name")
		} else if !usernameRe.MatchString(u.Name) || len(u.Name) > 32 {
			add(CodeUsername, "system.user.name", "must match [a-z_][a-z0-9_-]* and be at most 32 chars")
		}
		if u != nil {
			if err := ValidateFullname(u.Fullname); err != nil {
				add(CodeFullname, "system.user.fullname", "%v", err)
			}
			hasFile, hasHash := u.PasswordFile != "", u.PasswordHash != ""
			if hasFile == hasHash { // both or neither
				add(CodeMutex, "system.user.password_file", "exactly one of password_file or password_hash is required")
			}
			if hasFile {
				checkFile(u.PasswordFile, "system.user.password_file", true, add)
			}
			if hasHash && !strings.HasPrefix(u.PasswordHash, "$") {
				add(CodeHash, "system.user.password_hash", "must be a crypt(5) hash beginning with $")
			}
			for _, g := range u.Groups {
				if !usernameRe.MatchString(g) {
					add(CodeGroup, "system.user.groups", "invalid group name %q", g)
				}
			}
			checkSSHKey(u.SSHAuthorizedKey, u.SSHAuthorizedKeyFile, "system.user.ssh_authorized_key", add)
		}
	}
}

func checkSSHKey(inline, file, field string, add func(code, field, format string, args ...any)) {
	if inline != "" && file != "" {
		add(CodeMutex, field, "inline and _file variants are mutually exclusive")
		return
	}
	if inline != "" {
		if err := ValidateSSHAuthorizedKeys(inline); err != nil {
			add(CodeSSHKey, field, "%v", err)
		}
	}
	if file != "" {
		fileField := field + "_file"
		checkFile(file, fileField, false, add)
		fi, err := os.Stat(file)
		if err != nil || !fi.Mode().IsRegular() {
			return
		}
		data, err := os.ReadFile(file)
		if err != nil {
			add(CodeFile, fileField, "cannot read %q: %v", file, err)
			return
		}
		if err := ValidateSSHAuthorizedKeys(string(data)); err != nil {
			add(CodeSSHKey, fileField, "%v", err)
		}
	}
}

// ValidateSSHAuthorizedKeys validates one or more newline-separated OpenSSH
// public-key records. Unlike snosi-install, which copied a caller-provided
// file verbatim, firn fails closed at its recipe boundary: blank lines,
// authorized_keys options, and non-key content are rejected. A trailing
// newline is permitted.
func ValidateSSHAuthorizedKeys(keys string) error {
	// Permit the conventional single final newline without erasing leading
	// or repeated blank records, which must still fail per the schema.
	keys = strings.TrimSuffix(keys, "\n")
	if strings.TrimSpace(keys) == "" {
		return errors.New("must contain at least one OpenSSH public key line")
	}
	for i, line := range strings.Split(keys, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !sshKeyRe.MatchString(line) {
			return fmt.Errorf("line %d must be an OpenSSH public key (ssh-ed25519 AAAA...)", i+1)
		}
	}
	return nil
}

// checkFile enforces spec rule 2: *_file fields reference existing
// regular files; secret-valued ones must additionally not be
// world-readable.
func checkFile(path, field string, secret bool, add func(code, field, format string, args ...any)) {
	code := CodeFile
	if secret {
		code = CodeSecretFile
	}
	fi, err := os.Stat(path)
	if err != nil {
		add(code, field, "cannot read %q: file must exist at validation time", path)
		return
	}
	if !fi.Mode().IsRegular() {
		add(code, field, "%q is not a regular file", path)
		return
	}
	if secret && fi.Mode().Perm()&0o004 != 0 {
		add(code, field, "%q is world-readable; secret files must not be", path)
	}
}

func validHostname(h string) bool {
	if len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if len(label) == 0 || len(label) > 63 || !hostnameLabelRe.MatchString(label) {
			return false
		}
	}
	return true
}
