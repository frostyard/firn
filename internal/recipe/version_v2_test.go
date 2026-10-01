package recipe

import (
	"strings"
	"testing"
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
	if got := Deprecations(l); len(got) != 0 {
		t.Fatalf("deprecations: %v", got)
	}
}

func TestV1BootcDeprecatedButValid(t *testing.T) {
	l := mustParse(t, strings.Replace(bootcV2, "version = 2", "version = 1", 1))
	if got := Validate(l, Env{}); len(got) != 0 {
		t.Fatalf("issues: %v", got)
	}
	got := Deprecations(l)
	const message = "bootc recipe version 1 is deprecated; change version to 2 after validating the bootc fields before the published compatibility end date"
	if len(got) != 1 || got[0].Field != "version" || got[0].Code != CodeDeprecatedVersion || got[0].Message != message {
		t.Fatalf("deprecations: %v", got)
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
