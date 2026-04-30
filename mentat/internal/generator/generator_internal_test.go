// Package generator (internal tests) exercises unexported helpers directly.
package generator

import (
	"strings"
	"testing"

	"github.com/frostyard/firn/mentat/internal/classifier"
)

// ---------------------------------------------------------------------------
// buildPrompt tests
// ---------------------------------------------------------------------------

var testDomain = classifier.DomainResult{
	Name:        "storage",
	Path:        "internal/storage",
	Description: "Persistent key-value store backed by BoltDB.",
	FileCount:   6,
	Languages:   []string{"Go"},
}

func TestBuildPrompt_ContainsDomainContext(t *testing.T) {
	prompt := buildPrompt(testDomain, "")

	checks := []struct {
		label string
		want  string
	}{
		{"domain name", "storage"},
		{"domain path", "internal/storage"},
		{"language", "Go"},
		{"file count", "6"},
		{"description", "Persistent key-value store"},
	}
	for _, tc := range checks {
		if !strings.Contains(prompt, tc.want) {
			t.Errorf("prompt missing %s (%q); prompt snippet: %q", tc.label, tc.want, prompt[:min(200, len(prompt))])
		}
	}
}

func TestBuildPrompt_ContainsFrontmatterRequirement(t *testing.T) {
	prompt := buildPrompt(testDomain, "")

	// Must include the domain name inside the frontmatter template.
	wantFrontmatter := "name: storage"
	if !strings.Contains(prompt, wantFrontmatter) {
		t.Errorf("prompt does not contain frontmatter template %q", wantFrontmatter)
	}
}

func TestBuildPrompt_ContainsExample(t *testing.T) {
	prompt := buildPrompt(testDomain, "")

	// The one-shot example is the queue domain — it must appear.
	if !strings.Contains(prompt, "queue") {
		t.Error("prompt is missing the one-shot example (expected 'queue' domain reference)")
	}
	// The example should not be the target domain.
	if strings.Contains(prompt, "do not copy it verbatim") == false {
		t.Error("prompt is missing the 'do not copy it verbatim' instruction")
	}
}

func TestBuildPrompt_NoSectionHeaders(t *testing.T) {
	prompt := buildPrompt(testDomain, "")

	// The new prompt must NOT enumerate rigid section names as instructions.
	prescriptiveSections := []string{
		"## Key Abstractions",
		"## Common Patterns",
		"## Things to Know Before Modifying",
	}
	for _, sec := range prescriptiveSections {
		if strings.Contains(prompt, sec) {
			t.Errorf("prompt contains prescriptive section header %q — trust the model to choose structure", sec)
		}
	}
}

func TestBuildPrompt_IncludesFileSample(t *testing.T) {
	sample := "### storage.go\npackage storage\n"
	prompt := buildPrompt(testDomain, sample)

	if !strings.Contains(prompt, sample) {
		t.Error("prompt does not include the file sample")
	}
}

func TestBuildPrompt_EmptySample_NoGarbage(t *testing.T) {
	prompt := buildPrompt(testDomain, "")

	// An empty sample must not leave "Source sample" heading in the prompt.
	if strings.Contains(prompt, "Source sample") {
		t.Error("prompt contains 'Source sample' section even when sample is empty")
	}
}

func TestBuildPrompt_NoMarkdownFences_InInstructions(t *testing.T) {
	prompt := buildPrompt(testDomain, "")

	// The instruction portion must not ask the model to wrap output in fences —
	// the prompt explicitly requests no fences.
	if !strings.Contains(prompt, "no markdown fences") {
		t.Error("prompt is missing the 'no markdown fences' instruction")
	}
}

func TestBuildPrompt_MultipleLanguages(t *testing.T) {
	d := testDomain
	d.Languages = []string{"Go", "TypeScript", "Bash"}
	prompt := buildPrompt(d, "")

	if !strings.Contains(prompt, "Go, TypeScript, Bash") {
		t.Errorf("prompt does not list all languages; got snippet: %q", prompt[:min(300, len(prompt))])
	}
}

func TestBuildPrompt_EmptyLanguages_FallsBackToUnknown(t *testing.T) {
	d := testDomain
	d.Languages = nil
	prompt := buildPrompt(d, "")

	if !strings.Contains(prompt, "unknown") {
		t.Error("prompt should fall back to 'unknown' when no languages are provided")
	}
}

// ---------------------------------------------------------------------------
// normaliseContent tests
// ---------------------------------------------------------------------------

func TestNormaliseContent_PlainContent_Unchanged(t *testing.T) {
	input := "---\nname: foo\ndescription: bar\n---\n\n## Overview\n\nSome content.\n"
	got := normaliseContent(input)
	// TrimSpace + "\n" is the only transformation for clean input.
	want := strings.TrimSpace(input) + "\n"
	if got != want {
		t.Errorf("normaliseContent modified clean content:\nwant: %q\n got: %q", want, got)
	}
}

func TestNormaliseContent_StripsCodeFences(t *testing.T) {
	inner := "---\nname: foo\ndescription: bar\n---\n\n## Overview\n\nContent here."
	fenced := "```markdown\n" + inner + "\n```"
	got := normaliseContent(fenced)

	if strings.Contains(got, "```") {
		t.Errorf("normaliseContent did not strip code fences; got: %q", got[:min(100, len(got))])
	}
	if !strings.Contains(got, "---\nname: foo") {
		t.Errorf("normaliseContent removed content when stripping fences; got: %q", got[:min(200, len(got))])
	}
}

func TestNormaliseContent_StripsTrailingStatsLines(t *testing.T) {
	content := "---\nname: foo\ndescription: bar\n---\n\n## Overview\n\nSome content."
	withStats := content + "\nChanges +2 -1\nRequests 3 Premium (5s)\n"
	got := normaliseContent(withStats)

	if strings.Contains(got, "Changes") {
		t.Errorf("normaliseContent did not strip 'Changes' stats line; got: %q", got)
	}
	if strings.Contains(got, "Requests") {
		t.Errorf("normaliseContent did not strip 'Requests' stats line; got: %q", got)
	}
	if !strings.Contains(got, "Some content") {
		t.Error("normaliseContent removed real content while stripping stats")
	}
}

func TestNormaliseContent_EndsWithSingleNewline(t *testing.T) {
	cases := []string{
		"---\nname: x\ndescription: y\n---\n\nContent.",
		"---\nname: x\ndescription: y\n---\n\nContent.\n\n\n",
		"```\n---\nname: x\ndescription: y\n---\n\nContent.\n```",
	}
	for _, input := range cases {
		got := normaliseContent(input)
		if !strings.HasSuffix(got, "\n") {
			t.Errorf("output does not end with newline; got: %q", got)
		}
		if strings.HasSuffix(got, "\n\n") {
			t.Errorf("output ends with multiple newlines; got: %q", got)
		}
	}
}

func TestNormaliseContent_DoesNotCutMidContentOnDashDash(t *testing.T) {
	// Regression: old normaliser had a "\n---\n" heuristic that could truncate
	// valid content mid-document when a horizontal rule appeared in the body.
	content := "---\nname: foo\ndescription: bar\n---\n\n## Section\n\nSome text.\n\n---\n\nMore text after HR."
	got := normaliseContent(content)

	if !strings.Contains(got, "More text after HR") {
		t.Errorf("normaliseContent cut content at mid-document HR; got: %q", got)
	}
}

func TestNormaliseContent_EmptyInput(t *testing.T) {
	got := normaliseContent("")
	// Empty input should not panic; returns a single newline.
	if got != "\n" {
		t.Errorf("normaliseContent(\"\") = %q, want %q", got, "\n")
	}
}
