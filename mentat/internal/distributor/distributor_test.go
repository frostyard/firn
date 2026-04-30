package distributor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/clix"
	"github.com/frostyard/firn/mentat/internal/distributor"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// noopLogger returns a logger that discards all output so tests stay quiet.
// We use the default slog.Logger (nil is accepted by Distribute/DistributeAll).
func makeRepo(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// validSkillMD is a minimal valid SKILL.md with all fields distributor needs.
const validSkillMD = `---
name: auth
description: Handles user authentication and session management.
---

## Purpose

The auth domain manages login, logout, and session state.

## Key Abstractions

- Session: active user session struct.
- Authenticator: interface for verifying credentials.
`

// ---------------------------------------------------------------------------
// stripFrontmatter-style assertions (via Distribute outputs)
// ---------------------------------------------------------------------------

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file to exist: %s — %v", path, err)
	}
}

func assertNoFrontmatter(t *testing.T, content, label string) {
	t.Helper()
	if strings.HasPrefix(strings.TrimSpace(content), "---") {
		// Check whether the opening --- is truly a frontmatter block
		// (i.e. there is a matching closing --- before substantial content).
		rest := strings.TrimPrefix(strings.TrimSpace(content), "---")
		rest = strings.TrimPrefix(rest, "\n")
		if strings.Contains(rest, "\n---") {
			t.Errorf("%s: content still contains YAML frontmatter", label)
		}
	}
}

// ---------------------------------------------------------------------------
// Distribute — single domain, all targets
// ---------------------------------------------------------------------------

func TestDistribute_WritesAllTargets(t *testing.T) {
	repo := makeRepo(t)
	cfg := distributor.DefaultConfig()

	err := distributor.Distribute(repo, "auth", validSkillMD, cfg, nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	cases := []struct {
		label string
		path  string
	}{
		{"pi", filepath.Join(repo, ".agents", "skills", "auth", "SKILL.md")},
		{"claude", filepath.Join(repo, ".claude", "commands", "auth.md")},
		{"codex", filepath.Join(repo, ".codex", "skills", "auth.md")},
		{"cursor", filepath.Join(repo, ".cursor", "rules", "auth.mdc")},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			assertFileExists(t, tc.path)
		})
	}
}

// ---------------------------------------------------------------------------
// Target: pi — content written as-is
// ---------------------------------------------------------------------------

func TestDistribute_Pi_ContentUnchanged(t *testing.T) {
	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	got := readFile(t, filepath.Join(repo, ".agents", "skills", "auth", "SKILL.md"))
	wantPrefix := "---\nname: auth"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("pi target: want content starting with %q, got %q", wantPrefix, got[:min(60, len(got))])
	}
}

// ---------------------------------------------------------------------------
// Target: Claude — no frontmatter
// ---------------------------------------------------------------------------

func TestDistribute_Claude_NoFrontmatter(t *testing.T) {
	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	got := readFile(t, filepath.Join(repo, ".claude", "commands", "auth.md"))
	assertNoFrontmatter(t, got, "claude")
	if !strings.Contains(got, "## Purpose") {
		t.Error("claude target: markdown body missing")
	}
}

// ---------------------------------------------------------------------------
// Target: Codex — no frontmatter
// ---------------------------------------------------------------------------

func TestDistribute_Codex_NoFrontmatter(t *testing.T) {
	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	got := readFile(t, filepath.Join(repo, ".codex", "skills", "auth.md"))
	assertNoFrontmatter(t, got, "codex")
	if !strings.Contains(got, "## Purpose") {
		t.Error("codex target: markdown body missing")
	}
}

// ---------------------------------------------------------------------------
// Target: Cursor — .mdc extension + description-only frontmatter
// ---------------------------------------------------------------------------

func TestDistribute_Cursor_ExtensionIsMDC(t *testing.T) {
	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	path := filepath.Join(repo, ".cursor", "rules", "auth.mdc")
	assertFileExists(t, path)
}

func TestDistribute_Cursor_FrontmatterHasDescription(t *testing.T) {
	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	got := readFile(t, filepath.Join(repo, ".cursor", "rules", "auth.mdc"))

	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("cursor target: expected frontmatter, got: %q", got[:min(60, len(got))])
	}
	if !strings.Contains(got, "description:") {
		t.Error("cursor target: frontmatter missing 'description' field")
	}
	// Should NOT contain 'name:' (cursor frontmatter has description only).
	lines := strings.SplitN(got, "\n---\n", 2)
	if len(lines) == 2 {
		frontmatter := lines[0][4:] // strip leading "---\n"
		if strings.Contains(frontmatter, "name:") {
			t.Error("cursor target: frontmatter should not contain 'name' field")
		}
	}
	// Body should still be present.
	if !strings.Contains(got, "## Purpose") {
		t.Error("cursor target: markdown body missing after frontmatter")
	}
}

func TestDistribute_Cursor_DescriptionMatchesOriginal(t *testing.T) {
	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	got := readFile(t, filepath.Join(repo, ".cursor", "rules", "auth.mdc"))
	if !strings.Contains(got, "Handles user authentication and session management.") {
		t.Error("cursor target: description from original frontmatter not preserved")
	}
}

// ---------------------------------------------------------------------------
// DistributeAll — AGENTS.md generation
// ---------------------------------------------------------------------------

func TestDistributeAll_AgentsMDCreated(t *testing.T) {
	repo := makeRepo(t)
	skills := []distributor.SkillContent{
		{Domain: "auth", Content: validSkillMD},
		{Domain: "billing", Content: strings.ReplaceAll(validSkillMD, "auth", "billing")},
	}

	err := distributor.DistributeAll(repo, skills, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("DistributeAll: %v", err)
	}

	agentsPath := filepath.Join(repo, "AGENTS.md")
	assertFileExists(t, agentsPath)
}

func TestDistributeAll_AgentsMD_HasH2Sections(t *testing.T) {
	repo := makeRepo(t)
	skills := []distributor.SkillContent{
		{Domain: "auth", Content: validSkillMD},
		{Domain: "billing", Content: strings.ReplaceAll(validSkillMD, "auth", "billing")},
	}

	err := distributor.DistributeAll(repo, skills, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("DistributeAll: %v", err)
	}

	got := readFile(t, filepath.Join(repo, "AGENTS.md"))
	if !strings.Contains(got, "## auth") {
		t.Error("AGENTS.md: missing ## auth section")
	}
	if !strings.Contains(got, "## billing") {
		t.Error("AGENTS.md: missing ## billing section")
	}
}

func TestDistributeAll_AgentsMD_NoFrontmatter(t *testing.T) {
	repo := makeRepo(t)
	skills := []distributor.SkillContent{
		{Domain: "auth", Content: validSkillMD},
	}

	err := distributor.DistributeAll(repo, skills, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("DistributeAll: %v", err)
	}

	got := readFile(t, filepath.Join(repo, "AGENTS.md"))
	// AGENTS.md must not contain raw YAML frontmatter blocks.
	if strings.Contains(got, "name: auth") {
		t.Error("AGENTS.md: contains raw YAML frontmatter field 'name: auth'")
	}
	if strings.Contains(got, "description: Handles") {
		t.Error("AGENTS.md: contains raw YAML frontmatter field 'description: Handles'")
	}
}

func TestDistributeAll_AgentsMD_SortedByDomain(t *testing.T) {
	repo := makeRepo(t)
	// Provide skills in reverse alphabetical order; output must be sorted.
	skills := []distributor.SkillContent{
		{Domain: "storage", Content: strings.ReplaceAll(validSkillMD, "auth", "storage")},
		{Domain: "auth", Content: validSkillMD},
		{Domain: "billing", Content: strings.ReplaceAll(validSkillMD, "auth", "billing")},
	}

	err := distributor.DistributeAll(repo, skills, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("DistributeAll: %v", err)
	}

	got := readFile(t, filepath.Join(repo, "AGENTS.md"))
	authIdx := strings.Index(got, "## auth")
	billingIdx := strings.Index(got, "## billing")
	storageIdx := strings.Index(got, "## storage")

	if !(authIdx < billingIdx && billingIdx < storageIdx) {
		t.Errorf("AGENTS.md sections not sorted: auth@%d billing@%d storage@%d",
			authIdx, billingIdx, storageIdx)
	}
}

// ---------------------------------------------------------------------------
// Disabled target
// ---------------------------------------------------------------------------

func TestDistribute_DisabledTargetSkipped(t *testing.T) {
	repo := makeRepo(t)
	cfg := distributor.DefaultConfig()
	// Disable the claude target.
	for i := range cfg.Targets {
		if cfg.Targets[i].Name == "claude" {
			cfg.Targets[i].Enabled = false
		}
	}

	err := distributor.Distribute(repo, "auth", validSkillMD, cfg, nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	claudePath := filepath.Join(repo, ".claude", "commands", "auth.md")
	if _, err := os.Stat(claudePath); !os.IsNotExist(err) {
		t.Errorf("disabled claude target: file should not exist, got stat err: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Dry-run
// ---------------------------------------------------------------------------

func TestDistribute_DryRun_NoWrites(t *testing.T) {
	original := clix.DryRun
	clix.DryRun = true
	t.Cleanup(func() { clix.DryRun = original })

	repo := makeRepo(t)
	err := distributor.Distribute(repo, "auth", validSkillMD, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute dry-run: %v", err)
	}

	paths := []string{
		filepath.Join(repo, ".agents", "skills", "auth", "SKILL.md"),
		filepath.Join(repo, ".claude", "commands", "auth.md"),
		filepath.Join(repo, ".codex", "skills", "auth.md"),
		filepath.Join(repo, ".cursor", "rules", "auth.mdc"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("dry-run: file should not exist: %s", p)
		}
	}
}

func TestDistributeAll_DryRun_NoAgentsMD(t *testing.T) {
	original := clix.DryRun
	clix.DryRun = true
	t.Cleanup(func() { clix.DryRun = original })

	repo := makeRepo(t)
	skills := []distributor.SkillContent{
		{Domain: "auth", Content: validSkillMD},
	}
	err := distributor.DistributeAll(repo, skills, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("DistributeAll dry-run: %v", err)
	}

	agentsPath := filepath.Join(repo, "AGENTS.md")
	if _, err := os.Stat(agentsPath); !os.IsNotExist(err) {
		t.Errorf("dry-run: AGENTS.md should not exist")
	}
}

// ---------------------------------------------------------------------------
// Content with no frontmatter
// ---------------------------------------------------------------------------

func TestDistribute_ContentWithoutFrontmatter(t *testing.T) {
	repo := makeRepo(t)
	noFM := "## Purpose\n\nDoes things.\n"

	err := distributor.Distribute(repo, "misc", noFM, distributor.DefaultConfig(), nil)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	// Claude target should contain the body.
	got := readFile(t, filepath.Join(repo, ".claude", "commands", "misc.md"))
	if !strings.Contains(got, "## Purpose") {
		t.Error("claude target: body missing when source has no frontmatter")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
