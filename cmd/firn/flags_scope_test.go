package firn

import "testing"

// TestDetectionOverrideFlagScope pins the command surface for the
// detection-override flags so README.md's Usage section cannot silently
// drift from it again. The scoping is intentional: bare `firn` always
// autodetects (root.go NewRootCmd registers no flags), `firn validate`
// does not consume the UEFI result (validate.go registers with
// withUEFI=false), and only `firn install` exposes all three overrides.
func TestDetectionOverrideFlagScope(t *testing.T) {
	install := newInstallCmd()
	for _, name := range []string{"secure-boot", "tpm", "uefi"} {
		if install.Flags().Lookup(name) == nil {
			t.Errorf("firn install: expected --%s flag, got none", name)
		}
	}

	validate := newValidateCmd()
	for _, name := range []string{"secure-boot", "tpm"} {
		if validate.Flags().Lookup(name) == nil {
			t.Errorf("firn validate: expected --%s flag, got none", name)
		}
	}
	if validate.Flags().Lookup("uefi") != nil {
		t.Error("firn validate: --uefi must not be registered (validate does not consume UEFI state)")
	}

	root := NewRootCmd()
	for _, name := range []string{"secure-boot", "tpm", "uefi"} {
		if root.Flags().Lookup(name) != nil {
			t.Errorf("bare firn: --%s must not be registered (the wizard autodetects)", name)
		}
	}
}
