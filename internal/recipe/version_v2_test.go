package recipe

import (
	"strings"
	"testing"
	"time"
)

const bootcV2 = `version = 2
[image]
family = "bootc"
ref = "ghcr.io/frostyard/snow:latest"
[target]
disk = "/dev/vda"
filesystem = "ext4"
[security]
encryption = "none"
[system]
hostname = "firn"
`

func TestV2BootcValid(t *testing.T) {
	l := mustParse(t, bootcV2)
	if got := Validate(l, Env{}); len(got) != 0 {
		t.Fatalf("issues: %v", got)
	}
	if got := Deprecations(l, Env{}); len(got) != 0 {
		t.Fatalf("deprecations: %v", got)
	}
}

// at returns a clock fixed at t.
func at(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestV1BootcDeprecationWindow(t *testing.T) {
	const (
		warningMessage  = "bootc recipe version 1 is deprecated and is rejected on and after 2026-12-30; validate the bootc fields, then change only version to 2"
		rejectedMessage = "bootc recipe version 1 is no longer accepted on and after 2026-12-30; validate the bootc fields, then change only version to 2; no conversion was performed"
	)
	cases := []struct {
		name     string
		now      time.Time
		rejected bool
	}{
		{"release day", time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), false},
		{"decision day", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), false},
		{"last second before end", time.Date(2026, 12, 29, 23, 59, 59, 0, time.UTC), false},
		{"end boundary", time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC), true},
		{"end day noon", time.Date(2026, 12, 30, 12, 0, 0, 0, time.UTC), true},
		{"much later", time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC), true},
		{"local time ahead of UTC", time.Date(2026, 12, 29, 20, 0, 0, 0, time.FixedZone("EST", -5*60*60)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := mustParse(t, strings.Replace(bootcV2, "version = 2", "version = 1", 1))
			env := Env{Now: at(tc.now)}
			issues := Validate(l, env)
			deps := Deprecations(l, env)
			if l.Recipe.Version != SchemaVersionV1Bootc {
				t.Fatalf("version was rewritten to %d; there is no auto-upgrade", l.Recipe.Version)
			}
			if tc.rejected {
				if len(issues) != 1 || issues[0].Code != CodeExpiredVersion || issues[0].Field != "version" || issues[0].Message != rejectedMessage {
					t.Fatalf("issues at %s: %v", tc.now, issues)
				}
				if deps != nil {
					t.Fatalf("deprecations at %s: %v", tc.now, deps)
				}
				return
			}
			if len(issues) != 0 {
				t.Fatalf("issues at %s: %v", tc.now, issues)
			}
			if len(deps) != 1 || deps[0].Field != "version" || deps[0].Code != CodeDeprecatedVersion || deps[0].Message != warningMessage || !strings.Contains(deps[0].Message, BootcV1EndDate) {
				t.Fatalf("deprecations at %s: %v", tc.now, deps)
			}
		})
	}
}

func TestV2BootcValidAfterEndDate(t *testing.T) {
	env := Env{Now: at(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))}
	l := mustParse(t, bootcV2)
	if got := Validate(l, env); len(got) != 0 {
		t.Fatalf("issues: %v", got)
	}
	if got := Deprecations(l, env); len(got) != 0 {
		t.Fatalf("deprecations: %v", got)
	}
}

func TestV1ABKeepsFamilyScopeAfterEndDate(t *testing.T) {
	env := Env{Now: at(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC))}
	l, err := Parse([]byte("version = 1\n[image]\nfamily = \"ab\"\nproduct = \"floe-ab\"\n"))
	if err != nil {
		t.Fatalf("legacy A/B must parse to report targeted validation issue: %v", err)
	}
	got := Validate(l, env)
	if len(got) != 1 || got[0].Code != CodeFamilyScope || got[0].Field != "image.family" {
		t.Fatalf("issues: %v", got)
	}
}

func TestBootcV1EndDateIsReleasePlusNinetyDays(t *testing.T) {
	want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 90).Format("2006-01-02")
	if BootcV1EndDate != want {
		t.Fatalf("BootcV1EndDate = %q, want %q (v0.6.0 released 2026-10-01 plus 90 days)", BootcV1EndDate, want)
	}
	if got := bootcV1End.Format("2006-01-02"); got != BootcV1EndDate || bootcV1End.Location() != time.UTC || !bootcV1End.Equal(bootcV1End.Truncate(24*time.Hour)) {
		t.Fatalf("bootcV1End = %s, want %s 00:00 UTC", bootcV1End, BootcV1EndDate)
	}
}

func TestValidateV1ABRejectedBeforeAssemble(t *testing.T) {
	l, err := Parse([]byte("version = 1\n[image]\nfamily = \"ab\"\nproduct = \"floe-ab\"\n"))
	if err != nil {
		t.Fatalf("legacy A/B must parse to report targeted validation issue: %v", err)
	}
	got := Validate(l, Env{})
	const message = "A/B version 1 is unsupported by this bootc-only Firn; no conversion was performed; use a pre-transition installer only where separately authorized"
	if len(got) != 1 || got[0].Field != "image.family" || got[0].Code != CodeFamilyScope || got[0].Message != message || !strings.Contains(got[0].Error(), "image.family: "+message) {
		t.Fatalf("issues: %v", got)
	}
}

func TestV2ABIsUnknownEnum(t *testing.T) {
	l := mustParse(t, strings.Replace(bootcV2, `family = "bootc"`, `family = "ab"`, 1))
	issues := Validate(l, Env{})
	if len(issues) != 1 || issues[0].Code != CodeEnum || issues[0].Field != "image.family" {
		t.Fatalf("issues: %v", issues)
	}
}

func TestOtherVersionsRejected(t *testing.T) {
	for _, version := range []string{"version = 0", "version = 3", ""} {
		t.Run(version, func(t *testing.T) {
			l := mustParse(t, strings.Replace(bootcV2, "version = 2", version, 1))
			issues := Validate(l, Env{})
			if len(issues) != 1 || issues[0].Code != CodeVersion || issues[0].Field != "version" {
				t.Fatalf("issues: %v", issues)
			}
		})
	}
}

func TestV2ABFieldsUnknownAtParse(t *testing.T) {
	for _, tc := range []struct{ marker, field, path string }{
		{`ref = "ghcr.io/frostyard/snow:latest"`, `product = "floe-ab"`, "image.product"},
		{`ref = "ghcr.io/frostyard/snow:latest"`, `origin = "https://example.test"`, "image.origin"},
		{`ref = "ghcr.io/frostyard/snow:latest"`, `release = "20260101000000"`, "image.release"},
		{`filesystem = "ext4"`, `var_filesystem = "ext4"`, "target.var_filesystem"},
		{`filesystem = "ext4"`, `var_subvolumes = true`, "target.var_subvolumes"},
		{`encryption = "none"`, `recovery_key_out = "/tmp/key"`, "security.recovery_key_out"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			src := strings.Replace(bootcV2, tc.marker, tc.marker+"\n"+tc.field, 1)
			_, err := Parse([]byte(src))
			if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("parse error: %v", err)
			}
		})
	}
}
