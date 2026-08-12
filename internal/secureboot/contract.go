// Ported from frostyard/fisherman (GPL-3.0-only), fisherman/internal/secure/contract.go
// (deliberately reduced — see the divergence note below).

package secureboot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ContractPath is where snosi secure bootc images ship the schema-1
// secure-install contract.
const ContractPath = "usr/lib/snosi/bootc-secure.json"

// Expected contract invariants. These are the properties firn's ESP staging
// and MOK enrollment actually depend on — the boot chain shape — not the full
// field set fisherman pins.
const (
	CapabilityLabel = "io.snosi.bootc.secureboot-capable"
	CapabilityValue = "true"

	shimDebian         = "debian"
	secondStageMOKBoot = "mok-signed-systemd-boot"
	mokManagerMokMgr   = "MokManager"
	defaultMOKCertPath = "/usr/lib/snosi/mok.crt"
)

// Contract is the reduced view of bootc-secure.json firn needs. It is
// intentionally NOT parsed with DisallowUnknownFields (unlike fisherman): the
// live contract already drifted past fisherman's pinned versions (1.16.7 vs a
// hard-pinned 1.16.3), and firn verifies only the boot-chain shape it relies
// on, so a benign additive field must not fail the install (ADR-0014).
type Contract struct {
	Schema         int    `json:"schema"`
	MokCertificate string `json:"mok_certificate"`
	Installer      struct {
		OCI struct {
			CapabilityLabel string `json:"capability_label"`
			CapabilityValue string `json:"capability_value"`
		} `json:"oci"`
		SecureBoot struct {
			Shim        string `json:"shim"`
			SecondStage string `json:"second_stage"`
			MokManager  string `json:"mok_manager"`
		} `json:"secure_boot"`
	} `json:"installer"`
}

// Load reads and fail-closed-validates the secure-install contract from an
// extracted image root (see ExtractSecureImageRoot). A missing, malformed, or
// wrong-shape contract on a secure install means the image is not the secure
// image the recipe asked for, which is an error, not a warning.
func Load(imageRoot string) (*Contract, error) {
	path := filepath.Join(imageRoot, ContractPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("secureboot: reading secure-install contract %s: %w", ContractPath, err)
	}
	var c Contract
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("secureboot: parsing secure-install contract: %w", err)
	}
	if c.Schema != 1 {
		return nil, fmt.Errorf("secureboot: unsupported contract schema %d (want 1)", c.Schema)
	}
	if c.Installer.OCI.CapabilityLabel != CapabilityLabel || c.Installer.OCI.CapabilityValue != CapabilityValue {
		return nil, fmt.Errorf("secureboot: image is not secureboot-capable (%s=%q); a secure install needs a secure image",
			CapabilityLabel, c.Installer.OCI.CapabilityValue)
	}
	sb := c.Installer.SecureBoot
	if sb.Shim != shimDebian || sb.SecondStage != secondStageMOKBoot || sb.MokManager != mokManagerMokMgr {
		return nil, fmt.Errorf("secureboot: unexpected secure_boot chain shape {shim:%q second_stage:%q mok_manager:%q}",
			sb.Shim, sb.SecondStage, sb.MokManager)
	}
	if c.MokCertificate == "" {
		c.MokCertificate = defaultMOKCertPath
	}
	return &c, nil
}
