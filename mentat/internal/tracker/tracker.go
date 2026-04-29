// Package tracker persists per-domain generation state and determines whether
// a domain's source files have changed since the last SKILL.md write. It uses
// sha256 hashes of source file contents for staleness detection, making it
// independent of file-system timestamps and safe across machines and git
// worktrees.
package tracker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/frostyard/firn/mentat/internal/classifier"
)

// sourceExtensions is the set of file extensions treated as source files when
// computing the content hash. Mirrors scanner.DefaultConfig().Extensions.
var sourceExtensions = map[string]struct{}{
	".go": {}, ".ts": {}, ".js": {}, ".py": {}, ".sh": {},
	".rs": {}, ".rb": {}, ".java": {}, ".kt": {}, ".swift": {},
}

// defaultOutputDir is where SKILL.md files live relative to repoPath.
const defaultOutputDir = ".agents/skills"

// defaultStateFile is the path of the state file relative to repoPath.
const defaultStateFile = ".agents/mentat-state.json"

// DomainState captures the snapshot of a domain at its last successful generation.
type DomainState struct {
	// Domain is the short domain name (e.g. "auth").
	Domain string `json:"domain"`

	// Path is the domain's relative path from the repository root.
	Path string `json:"path"`

	// LastGenAt is the UTC time when the SKILL.md was last written.
	LastGenAt time.Time `json:"last_gen_at"`

	// ContentHash is the sha256 hex digest of all source file contents inside
	// the domain directory at the time of the last generation.
	ContentHash string `json:"content_hash"`
}

// Tracker loads and saves domain generation state to a JSON file.
// All fields are optional; use NewTracker to get sensible defaults.
type Tracker struct {
	// StateFile is the absolute (or working-directory-relative) path of the
	// state file. When empty, StateFile is derived from repoPath at call time.
	StateFile string
}

// NewTracker returns a Tracker whose state file is
// {repoPath}/.agents/mentat-state.json.
func NewTracker(repoPath string) *Tracker {
	return &Tracker{
		StateFile: filepath.Join(repoPath, defaultStateFile),
	}
}

// Load reads the state file and returns the persisted domain states.
// If the file does not exist, Load returns an empty map and no error — this
// is a first-run scenario, not an error.
func (t *Tracker) Load() (map[string]DomainState, error) {
	data, err := os.ReadFile(t.StateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]DomainState), nil
		}
		return nil, fmt.Errorf("tracker: reading state file %s: %w", t.StateFile, err)
	}

	var states []DomainState
	if err := json.Unmarshal(data, &states); err != nil {
		return nil, fmt.Errorf("tracker: parsing state file %s: %w", t.StateFile, err)
	}

	m := make(map[string]DomainState, len(states))
	for _, s := range states {
		m[s.Domain] = s
	}
	return m, nil
}

// Save writes the state map to the state file, creating parent directories as
// needed.
func (t *Tracker) Save(state map[string]DomainState) error {
	// Stable ordering: sort by domain name.
	entries := make([]DomainState, 0, len(state))
	for _, s := range state {
		entries = append(entries, s)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Domain < entries[j].Domain
	})

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("tracker: marshalling state: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(t.StateFile), 0o755); err != nil {
		return fmt.Errorf("tracker: creating state dir: %w", err)
	}

	if err := os.WriteFile(t.StateFile, data, 0o644); err != nil {
		return fmt.Errorf("tracker: writing state file %s: %w", t.StateFile, err)
	}
	return nil
}

// IsStale reports whether the domain's source files have changed since the
// last generation, or whether no SKILL.md exists yet.
//
// Returns true (stale) when:
//   - the domain has no entry in state, OR
//   - the SKILL.md output file does not exist, OR
//   - the current content hash differs from the stored hash.
func (t *Tracker) IsStale(repoPath string, domain classifier.DomainResult, state map[string]DomainState) (bool, error) {
	// Check whether the SKILL.md already exists on disk.
	skillPath := filepath.Join(repoPath, defaultOutputDir, domain.Name, "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, fmt.Errorf("tracker: stat SKILL.md for domain %q: %w", domain.Name, err)
	}

	// If there is no state entry for this domain it is stale.
	prev, ok := state[domain.Name]
	if !ok {
		return true, nil
	}

	// Hash the current source files.
	hash, err := hashDomain(repoPath, domain.Path)
	if err != nil {
		return false, fmt.Errorf("tracker: hashing domain %q: %w", domain.Name, err)
	}

	return hash != prev.ContentHash, nil
}

// RecordGeneration updates state for domain after a successful SKILL.md write.
// It computes the current content hash and stores it with the current UTC time.
func (t *Tracker) RecordGeneration(repoPath string, domain classifier.DomainResult, state map[string]DomainState) error {
	hash, err := hashDomain(repoPath, domain.Path)
	if err != nil {
		return fmt.Errorf("tracker: recording generation for domain %q: %w", domain.Name, err)
	}

	state[domain.Name] = DomainState{
		Domain:      domain.Name,
		Path:        domain.Path,
		LastGenAt:   time.Now().UTC(),
		ContentHash: hash,
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// hashDomain computes the sha256 of all source file contents directly inside
// domainRelPath (non-recursive, matching the scanner's single-directory scan).
// Files are processed in sorted order for determinism.
func hashDomain(repoPath, domainRelPath string) (string, error) {
	dir := filepath.Join(repoPath, domainRelPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// An empty or missing domain directory hashes to the empty digest.
			return emptyHash(), nil
		}
		return "", fmt.Errorf("reading domain dir %s: %w", dir, err)
	}

	// Collect and sort source file names.
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := sourceExtensions[ext]; ok {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := hashFile(h, path); err != nil {
			return "", fmt.Errorf("hashing file %s: %w", path, err)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashFile writes the filename and content of path into h so that renames are
// also detected.
func hashFile(h io.Writer, path string) error {
	// Include the base filename so that a rename changes the hash even if
	// content is identical.
	if _, err := io.WriteString(h, filepath.Base(path)+"\x00"); err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return nil
}

// emptyHash returns the sha256 hex digest of an empty input.
func emptyHash() string {
	h := sha256.Sum256(nil)
	return hex.EncodeToString(h[:])
}
