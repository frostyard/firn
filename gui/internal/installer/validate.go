package installer

// Field validation asks the schema itself (ADR-0021): the current choices are
// assembled with every secret pointing at one private placeholder file, and
// recipe.Validate judges the result. The GUI keeps no copies of the schema's
// patterns, so its live errors cannot drift from what install accepts. The
// checks the schema cannot make (secrets entered and confirmed, a usable disk
// chosen) are the GUI's own.

import (
	"strings"

	"github.com/frostyard/firn/internal/recipe"
)

// Field identifiers for the GUI's own checks; schema issues use the
// recipe's dotted field names.
const (
	fieldDisk              = "gui.disk"
	fieldPassphrase        = "gui.passphrase"
	fieldPassphraseConfirm = "gui.passphrase_confirm"
	fieldMokPassword       = "gui.mok_password"
	fieldMokConfirm        = "gui.mok_confirm"
	fieldPassword          = "gui.password"
	fieldPasswordConfirm   = "gui.password_confirm"
	fieldCorePreview       = "gui.core_preview"
	fieldUEFI              = "gui.uefi"
)

// fieldError is one problem with one field. required marks an empty field
// that needs a value: the pages show those only after the user tries to
// continue, while a value that is present but wrong shows at once.
type fieldError struct {
	msg      string
	required bool
}

type fieldErrors map[string]fieldError

// validateChoices returns every field problem in c. placeholder is an
// existing 0600 file standing in for each secret; refused maps disk paths to
// the reason firn refuses them.
func validateChoices(c choices, env recipe.Env, placeholder string, refused map[string]string) fieldErrors {
	errs := fieldErrors{}
	add := func(field, msg string, required bool) {
		if _, ok := errs[field]; !ok {
			errs[field] = fieldError{msg: msg, required: required}
		}
	}

	r, err := assemble(c, env.SecureBoot, func(string, string) (string, error) { return placeholder, nil })
	if err != nil {
		add("recipe", err.Error(), false)
		return errs
	}
	data, err := recipe.Marshal(r)
	if err != nil {
		add("recipe", err.Error(), false)
		return errs
	}
	l, err := recipe.Parse(data)
	if err != nil {
		add("recipe", err.Error(), false)
		return errs
	}
	for _, is := range recipe.Validate(l, env) {
		required := is.Code == recipe.CodeRequired
		if required && is.Field == "target.disk" {
			continue // the GUI's own disk check says it better
		}
		msg := sentence(is.Message)
		if friendly, ok := requiredMessages[is.Field]; ok && required {
			msg = friendly
		}
		add(is.Field, msg, required)
	}

	switch {
	case c.disk == "":
		add(fieldDisk, "Choose the disk to install to.", true)
	case refused[c.disk] != "":
		add(fieldDisk, "firn cannot install to "+c.disk+": "+refused[c.disk]+".", false)
	}
	if needsPassphrase(c.encryption) {
		requireSecret(add, c.passphrase, c.passphraseConfirm, fieldPassphrase, fieldPassphraseConfirm, "an encryption passphrase", "Passphrases do not match.")
	}
	if env.SecureBoot && c.mok == "enroll" {
		requireSecret(add, c.mokPassword, c.mokConfirm, fieldMokPassword, fieldMokConfirm, "a MOK enrollment password", "Passwords do not match.")
	}
	if c.createUser {
		requireSecret(add, c.password, c.passwordConfirm, fieldPassword, fieldPasswordConfirm, "a password", "Passwords do not match.")
	}
	return errs
}

// requiredMessages word the schema's "required" issues for the field they
// appear under; the schema's own text names recipe tables.
var requiredMessages = map[string]string{
	"system.hostname":  "Enter a hostname.",
	"system.user.name": "Choose a username.",
}

func requireSecret(add func(string, string, bool), secret, confirm, field, confirmField, what, mismatch string) {
	switch {
	case strings.TrimSpace(secret) == "":
		add(field, "Enter "+what+".", true)
	case confirm == "":
		add(confirmField, "Type it again to confirm.", true)
	case confirm != secret:
		add(confirmField, mismatch, false)
	}
}

// pageFields lists, by prefix, the fields each page collects; an error in
// one of them keeps the user on that page. Anything unclaimed surfaces on
// the review page.
var pageFields = map[page][]string{
	pageWelcome:  {fieldUEFI},
	pageImage:    {"image.", "target.bootloader"},
	pageStorage:  {fieldDisk, "target.disk", "target.filesystem", "target.btrfs_subvolumes"},
	pageSecurity: {"security.", fieldPassphrase, fieldPassphraseConfirm, fieldMokPassword, fieldMokConfirm},
	pageRegion:   {"system.hostname", "system.locale", "system.timezone", "system.keyboard", "system.root_ssh_authorized_key"},
	pageAccount:  {"system.user.", fieldPassword, fieldPasswordConfirm},
	pageApps:     {"system.flatpaks", "system.core_flatpaks", fieldCorePreview},
}

// pageOf returns the page that collects field, or pageReview when none does.
func pageOf(field string) page {
	for p, prefixes := range pageFields {
		for _, prefix := range prefixes {
			if field == prefix || (strings.HasSuffix(prefix, ".") && strings.HasPrefix(field, prefix)) {
				return p
			}
		}
	}
	return pageReview
}

// onPage returns the errors that belong to p.
func (errs fieldErrors) onPage(p page) fieldErrors {
	out := fieldErrors{}
	for field, e := range errs {
		if pageOf(field) == p {
			out[field] = e
		}
	}
	return out
}

// sentence capitalizes a schema message and ends it with a period, for
// display under a field.
func sentence(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return msg
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, ")") {
		msg += "."
	}
	return msg
}
