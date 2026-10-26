package installer

import (
	"testing"

	"github.com/frostyard/firn/internal/recipe"
)

func placeholder(t *testing.T) string {
	t.Helper()
	p, err := writeSecretFile(t.TempDir(), "ph", "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompleteChoicesHaveNoErrors(t *testing.T) {
	errs := validateChoices(completeChoices(t), recipe.Env{}, placeholder(t), nil)
	if len(errs) != 0 {
		t.Fatalf("complete choices reported %v", errs)
	}
}

// The schema is the field validator: its issues land on the field and page
// that collect the value.
func TestSchemaIssuesMapToFields(t *testing.T) {
	ph := placeholder(t)
	c := completeChoices(t)
	c.hostname = "bad_host!"
	c.locale = "english"
	c.username = "Ada"
	c.rootSSHKey = "not a key"
	errs := validateChoices(c, recipe.Env{}, ph, nil)
	for field, want := range map[string]page{
		"system.hostname":                pageRegion,
		"system.locale":                  pageRegion,
		"system.root_ssh_authorized_key": pageRegion,
		"system.user.name":               pageAccount,
	} {
		e, ok := errs[field]
		if !ok {
			t.Errorf("no error for %s in %v", field, errs)
			continue
		}
		if e.required {
			t.Errorf("%s: a wrong value must show at once, not wait for an attempt", field)
		}
		if got := pageOf(field); got != want {
			t.Errorf("pageOf(%s) = %v, want %v", field, got, want)
		}
	}
	if !errs.onPage(pageImage).empty() || !errs.onPage(pageStorage).empty() {
		t.Errorf("unrelated pages got errors: %v", errs)
	}
}

func (errs fieldErrors) empty() bool { return len(errs) == 0 }

func TestGUIChecks(t *testing.T) {
	ph := placeholder(t)
	c := completeChoices(t)
	c.disk = "/dev/sdb"
	c.encryption = "luks-passphrase"
	c.passphrase, c.passphraseConfirm = "one", "two"
	c.password = ""
	errs := validateChoices(c, recipe.Env{SecureBoot: true}, ph, map[string]string{"/dev/sdb": "it is mounted"})
	if e := errs[fieldDisk]; e.msg == "" || e.required {
		t.Errorf("refused disk: %+v", e)
	}
	if e := errs[fieldPassphraseConfirm]; e.msg != "Passphrases do not match." {
		t.Errorf("mismatch: %+v", e)
	}
	if e := errs[fieldMokPassword]; !e.required {
		t.Errorf("missing MOK password under Secure Boot: %+v", e)
	}
	if e := errs[fieldPassword]; !e.required {
		t.Errorf("missing user password: %+v", e)
	}
	if _, ok := errs["security.mok_password_file"]; ok {
		t.Error("the placeholder file must satisfy the schema's secret-file check")
	}
}

func TestTPMModesNeedATPM(t *testing.T) {
	c := completeChoices(t)
	c.encryption = "tpm2-luks"
	errs := validateChoices(c, recipe.Env{TPM: false}, placeholder(t), nil)
	if _, ok := errs["security.encryption"]; !ok || pageOf("security.encryption") != pageSecurity {
		t.Errorf("tpm2-luks without a TPM must fail on the security page: %v", errs)
	}
}

func TestSentence(t *testing.T) {
	if got := sentence("must be RFC 1123 host label(s), max 253 chars"); got != "Must be RFC 1123 host label(s), max 253 chars." {
		t.Errorf("sentence = %q", got)
	}
}
