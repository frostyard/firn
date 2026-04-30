// Package distributor copies generated SKILL.md content to the agent-specific
// directory layouts expected by different coding-agent toolchains.
//
// Supported targets:
//
//   - pi       — .agents/skills/{domain}/SKILL.md  (pi/Miles format, as-is)
//   - claude   — .claude/commands/{domain}.md       (no frontmatter)
//   - codex    — .codex/skills/{domain}.md          (no frontmatter)
//   - cursor   — .cursor/rules/{domain}.mdc         (description-only frontmatter)
//   - agents-md — AGENTS.md at repo root            (all domains, ## sections)
package distributor

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frostyard/clix"
)

// templateCursor marks a target that needs cursor-style frontmatter rewriting.
const templateCursor = "cursor"

// Target describes one agent toolchain's expected skill-file layout.
type Target struct {
	// Name is a short identifier, e.g. "pi", "claude", "codex", "cursor", "agents-md".
	Name string

	// Enabled controls whether this target is active.
	Enabled bool

	// Dir is the directory path relative to the repo root where individual
	// skill files are written. Empty for the "agents-md" target.
	Dir string

	// Ext is the file extension including the leading dot, e.g. ".md", ".mdc".
	Ext string

	// Template is an optional key that selects content transformation logic.
	// Currently only "cursor" is meaningful; empty means strip-frontmatter or as-is.
	Template string
}

// Config holds the set of distribution targets.
type Config struct {
	Targets []Target
}

// SkillContent pairs a domain name with its raw SKILL.md content.
type SkillContent struct {
	// Domain is the short domain name, e.g. "auth".
	Domain string

	// Content is the full raw SKILL.md text including any YAML frontmatter.
	Content string
}

// DefaultConfig returns a Config with all five standard targets enabled.
func DefaultConfig() Config {
	return Config{
		Targets: []Target{
			{Name: "pi", Enabled: true, Dir: ".agents/skills", Ext: ".md", Template: "pi"},
			{Name: "claude", Enabled: true, Dir: ".claude/commands", Ext: ".md"},
			{Name: "codex", Enabled: true, Dir: ".codex/skills", Ext: ".md"},
			{Name: "cursor", Enabled: true, Dir: ".cursor/rules", Ext: ".mdc", Template: templateCursor},
			{Name: "agents-md", Enabled: true},
		},
	}
}

// Distribute writes the SKILL.md content for a single domain to all enabled
// targets (except "agents-md", which requires the full set — use DistributeAll
// for that). Respects clix.DryRun: logs intent and returns without writing.
func Distribute(repoPath, domain, content string, cfg Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	for _, t := range cfg.Targets {
		if !t.Enabled {
			continue
		}
		if t.Name == "agents-md" {
			// agents-md is handled by DistributeAll once all skills are collected.
			continue
		}
		if err := writeTarget(repoPath, domain, content, t, log); err != nil {
			return fmt.Errorf("distributor: target %q domain %q: %w", t.Name, domain, err)
		}
	}
	return nil
}

// DistributeAll runs Distribute for every skill and then regenerates AGENTS.md
// from all distributed skills (sorted by domain name for deterministic output).
// Respects clix.DryRun.
func DistributeAll(repoPath string, skills []SkillContent, cfg Config, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}

	// Sort by domain name for deterministic output.
	sorted := make([]SkillContent, len(skills))
	copy(sorted, skills)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Domain < sorted[j].Domain
	})

	for _, sc := range sorted {
		if err := Distribute(repoPath, sc.Domain, sc.Content, cfg, log); err != nil {
			return fmt.Errorf("distributor: distributing domain %q: %w", sc.Domain, err)
		}
	}

	// Regenerate AGENTS.md if the target is enabled.
	for _, t := range cfg.Targets {
		if t.Enabled && t.Name == "agents-md" {
			if err := writeAgentsMD(repoPath, sorted, log); err != nil {
				return fmt.Errorf("distributor: writing AGENTS.md: %w", err)
			}
			break
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// writeTarget writes content for one (target, domain) pair. It applies the
// appropriate content transformation based on t.Template.
func writeTarget(repoPath, domain, content string, t Target, log *slog.Logger) error {
	var transformed string
	var err error

	switch t.Template {
	case "pi":
		// pi/Miles: write as-is, but inside a {domain}/ subdirectory named SKILL.md.
		transformed = content
	case templateCursor:
		transformed, err = transformCursor(domain, content)
		if err != nil {
			return fmt.Errorf("cursor transform: %w", err)
		}
	default:
		// claude, codex: strip YAML frontmatter, plain markdown body only.
		transformed = stripFrontmatter(content)
	}

	// Determine the output path.
	var destPath string
	if t.Template == "pi" {
		// pi target keeps the {domain}/SKILL.md structure.
		destPath = filepath.Join(repoPath, t.Dir, domain, "SKILL.md")
	} else {
		filename := domain + t.Ext
		destPath = filepath.Join(repoPath, t.Dir, filename)
	}

	if clix.DryRun {
		log.Info("distributor: dry-run — would write", "target", t.Name, "path", destPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("creating dir %s: %w", filepath.Dir(destPath), err)
	}

	if err := os.WriteFile(destPath, []byte(transformed), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}

	log.Info("distributor: wrote", "target", t.Name, "domain", domain, "path", destPath)
	return nil
}

// writeAgentsMD builds and writes (or dry-run logs) the combined AGENTS.md.
func writeAgentsMD(repoPath string, skills []SkillContent, log *slog.Logger) error {
	destPath := filepath.Join(repoPath, "AGENTS.md")

	if clix.DryRun {
		log.Info("distributor: dry-run — would write AGENTS.md", "path", destPath, "domains", len(skills))
		return nil
	}

	var sb strings.Builder
	for _, sc := range skills {
		body := stripFrontmatter(sc.Content)
		sb.WriteString("## ")
		sb.WriteString(sc.Domain)
		sb.WriteString("\n\n")
		sb.WriteString(strings.TrimSpace(body))
		sb.WriteString("\n\n")
	}

	if err := os.WriteFile(destPath, []byte(strings.TrimRight(sb.String(), "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}

	log.Info("distributor: wrote AGENTS.md", "path", destPath, "domains", len(skills))
	return nil
}

// ---------------------------------------------------------------------------
// Content transformations
// ---------------------------------------------------------------------------

// stripFrontmatter removes the leading YAML frontmatter block (between the
// first pair of "---" delimiters) and returns the remaining markdown body,
// trimmed of leading/trailing whitespace, with a final newline appended.
func stripFrontmatter(content string) string {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "---") {
		return s + "\n"
	}
	// Skip the opening "---".
	rest := s[3:]
	// Consume an optional newline after "---".
	rest = strings.TrimPrefix(rest, "\n")
	// Find the closing "---".
	end := strings.Index(rest, "\n---")
	if end == -1 {
		// Malformed or no closing delimiter — return as-is.
		return s + "\n"
	}
	body := strings.TrimSpace(rest[end+4:]) // +4 skips "\n---"
	return body + "\n"
}

// extractDescription parses the "description" field from a YAML frontmatter
// block. Returns an empty string if the field is absent or the block is
// malformed.
func extractDescription(content string) string {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "---") {
		return ""
	}
	rest := strings.TrimPrefix(s[3:], "\n")
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return ""
	}
	block := rest[:end]
	for _, line := range strings.Split(block, "\n") {
		if after, ok := strings.CutPrefix(line, "description:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// transformCursor rewrites frontmatter for the Cursor .mdc format:
//
//	---
//	description: <original description>
//	---
//
// The markdown body is preserved as-is.
func transformCursor(domain, content string) (string, error) {
	desc := extractDescription(content)
	if desc == "" {
		desc = domain + " domain skills"
	}
	body := stripFrontmatter(content)
	result := fmt.Sprintf("---\ndescription: %s\n---\n\n%s", desc, strings.TrimSpace(body)+"\n")
	return result, nil
}
