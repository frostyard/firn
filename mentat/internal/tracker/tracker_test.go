package tracker_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/frostyard/firn/mentat/internal/classifier"
	"github.com/frostyard/firn/mentat/internal/tracker"
)

// makeRepo creates a temporary directory that looks like a minimal repository
// with a domain directory and, optionally, a SKILL.md for that domain.
func makeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// addSourceFile writes a source file under repoPath/domainPath/name with the given content.
func addSourceFile(t *testing.T, repoPath, domainPath, name, content string) {
	t.Helper()
	full := filepath.Join(repoPath, domainPath, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}
}

// addSkillFile writes a placeholder SKILL.md for the domain.
func addSkillFile(t *testing.T, repoPath, domainName string) {
	t.Helper()
	path := filepath.Join(repoPath, ".agents", "skills", domainName, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("# skill\n"), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
}

// domain returns a minimal DomainResult for tests.
func domain(name, path string) classifier.DomainResult {
	return classifier.DomainResult{Name: name, Path: path}
}

// ---------------------------------------------------------------------------
// IsStale
// ---------------------------------------------------------------------------

func TestIsStale_NewDomain_NoState(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "auth", "auth.go", "package auth\n")
	addSkillFile(t, repoPath, "auth")

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	stale, err := tr.IsStale(repoPath, domain("auth", "auth"), state)
	if err != nil {
		t.Fatalf("IsStale error: %v", err)
	}
	if !stale {
		t.Error("expected stale=true for domain with no state entry")
	}
}

func TestIsStale_NoSkillMD(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "auth", "auth.go", "package auth\n")
	// Note: no SKILL.md written

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	stale, err := tr.IsStale(repoPath, domain("auth", "auth"), state)
	if err != nil {
		t.Fatalf("IsStale error: %v", err)
	}
	if !stale {
		t.Error("expected stale=true when SKILL.md does not exist")
	}
}

func TestIsStale_UnchangedFiles(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "auth", "auth.go", "package auth\n")
	addSkillFile(t, repoPath, "auth")

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	d := domain("auth", "auth")

	// Simulate a prior generation by recording the current hash.
	if err := tr.RecordGeneration(repoPath, d, state); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	stale, err := tr.IsStale(repoPath, d, state)
	if err != nil {
		t.Fatalf("IsStale error: %v", err)
	}
	if stale {
		t.Error("expected stale=false when source files are unchanged")
	}
}

func TestIsStale_ChangedFile(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "auth", "auth.go", "package auth\n")
	addSkillFile(t, repoPath, "auth")

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	d := domain("auth", "auth")

	// Record state with original content.
	if err := tr.RecordGeneration(repoPath, d, state); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	// Now change the source file.
	addSourceFile(t, repoPath, "auth", "auth.go", "package auth\n\nfunc New() {}\n")

	stale, err := tr.IsStale(repoPath, d, state)
	if err != nil {
		t.Fatalf("IsStale error: %v", err)
	}
	if !stale {
		t.Error("expected stale=true after source file changed")
	}
}

// ---------------------------------------------------------------------------
// RecordGeneration
// ---------------------------------------------------------------------------

func TestRecordGeneration_UpdatesHash(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "billing", "billing.go", "package billing\n")

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	d := domain("billing", "billing")

	if err := tr.RecordGeneration(repoPath, d, state); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	s, ok := state["billing"]
	if !ok {
		t.Fatal("expected state entry for billing")
	}
	if s.ContentHash == "" {
		t.Error("expected non-empty ContentHash")
	}
	if s.LastGenAt.IsZero() {
		t.Error("expected non-zero LastGenAt")
	}
	if s.Domain != "billing" {
		t.Errorf("expected Domain=billing, got %q", s.Domain)
	}
	if s.Path != "billing" {
		t.Errorf("expected Path=billing, got %q", s.Path)
	}
}

func TestRecordGeneration_HashChangesWithContent(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "billing", "billing.go", "package billing\n")

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)
	d := domain("billing", "billing")

	if err := tr.RecordGeneration(repoPath, d, state); err != nil {
		t.Fatalf("first RecordGeneration: %v", err)
	}
	hash1 := state["billing"].ContentHash

	// Modify the source file and record again.
	addSourceFile(t, repoPath, "billing", "billing.go", "package billing\n\nfunc Pay() {}\n")
	if err := tr.RecordGeneration(repoPath, d, state); err != nil {
		t.Fatalf("second RecordGeneration: %v", err)
	}
	hash2 := state["billing"].ContentHash

	if hash1 == hash2 {
		t.Error("expected hash to change after source file modification")
	}
}

// ---------------------------------------------------------------------------
// Load / Save
// ---------------------------------------------------------------------------

func TestLoad_MissingFile(t *testing.T) {
	repoPath := makeRepo(t)
	tr := tracker.NewTracker(repoPath)

	state, err := tr.Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(state) != 0 {
		t.Errorf("expected empty state for missing file, got %d entries", len(state))
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	repoPath := makeRepo(t)
	addSourceFile(t, repoPath, "core", "core.go", "package core\n")

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	d := domain("core", "core")
	if err := tr.RecordGeneration(repoPath, d, state); err != nil {
		t.Fatalf("RecordGeneration: %v", err)
	}

	// Save then reload.
	if err := tr.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := tr.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := loaded["core"]
	if !ok {
		t.Fatal("expected 'core' entry after round-trip")
	}
	want := state["core"]
	if got.ContentHash != want.ContentHash {
		t.Errorf("ContentHash: got %q, want %q", got.ContentHash, want.ContentHash)
	}
	if !got.LastGenAt.Equal(want.LastGenAt) {
		t.Errorf("LastGenAt: got %v, want %v", got.LastGenAt, want.LastGenAt)
	}
	if got.Domain != want.Domain {
		t.Errorf("Domain: got %q, want %q", got.Domain, want.Domain)
	}
	if got.Path != want.Path {
		t.Errorf("Path: got %q, want %q", got.Path, want.Path)
	}
}

func TestSaveLoad_MultipleEntries(t *testing.T) {
	repoPath := makeRepo(t)

	domains := []struct{ name, path, file, content string }{
		{"auth", "auth", "auth.go", "package auth\n"},
		{"billing", "billing", "billing.go", "package billing\n"},
		{"core", "core", "core.go", "package core\n"},
	}

	tr := tracker.NewTracker(repoPath)
	state := make(map[string]tracker.DomainState)

	for _, dd := range domains {
		addSourceFile(t, repoPath, dd.path, dd.file, dd.content)
		if err := tr.RecordGeneration(repoPath, domain(dd.name, dd.path), state); err != nil {
			t.Fatalf("RecordGeneration %s: %v", dd.name, err)
		}
	}

	if err := tr.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := tr.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded) != len(domains) {
		t.Errorf("expected %d entries, got %d", len(domains), len(loaded))
	}
	for _, dd := range domains {
		if _, ok := loaded[dd.name]; !ok {
			t.Errorf("missing entry %q after round-trip", dd.name)
		}
	}
}
