// Package scanner walks a repository tree and returns domain candidates
// for downstream LLM classification. It applies configurable skip lists,
// container-directory descent, file-count thresholds, and depth limits to
// produce a lightweight, language-agnostic candidate list.
package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Config controls scanner behaviour. All fields are optional; use
// DefaultConfig() to get sensible starting values.
type Config struct {
	// SkipDirs lists directory names to skip entirely during the walk.
	// Matched against the bare name (not the full path).
	SkipDirs []string

	// ContainerDirs lists structural directory names whose children are
	// evaluated as candidates rather than the container itself.
	// Example: "internal/auth" produces candidate "internal/auth", not "internal".
	ContainerDirs []string

	// MinFiles is the minimum number of recognised source files a directory
	// must contain to be returned as a candidate. Default: 2.
	MinFiles int

	// Extensions is the set of file extensions (including the leading dot)
	// recognised as source files.
	// Default: .go .ts .js .py .sh .rs .rb .java .kt .swift
	Extensions []string

	// MaxDepth is the maximum directory depth to descend into, relative to
	// the repository root. Default: 3.
	MaxDepth int

	// Logger is used for structured debug/warn output. If nil, slog.Default()
	// is used.
	Logger *slog.Logger
}

// DefaultConfig returns a Config populated with sensible defaults that work
// well for typical Go, TypeScript, Python, and shell repositories.
func DefaultConfig() Config {
	return Config{
		SkipDirs: []string{
			".git", "vendor", "node_modules", "dist", "build",
			"bin", ".cache", "testdata", "fixtures",
		},
		ContainerDirs: []string{
			"src", "internal", "cmd", "pkg", "lib", "app", "core",
		},
		MinFiles:   2,
		Extensions: []string{".go", ".ts", ".js", ".py", ".sh", ".rs", ".rb", ".java", ".kt", ".swift"},
		MaxDepth:   3,
	}
}

// Candidate represents a directory that is a potential logical domain.
type Candidate struct {
	// Path is the slash-separated path relative to the repository root.
	Path string `json:"path"`

	// FileCount is the number of recognised source files directly inside
	// this directory (non-recursive).
	FileCount int `json:"file_count"`

	// Languages holds the distinct languages detected from file extensions,
	// sorted alphabetically.
	Languages []string `json:"languages"`
}

// Scan walks repoPath and returns domain candidates according to cfg.
// It respects ctx for cancellation on large repositories.
func Scan(ctx context.Context, repoPath string, cfg Config) ([]Candidate, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	skipSet := toSet(cfg.SkipDirs)
	containerSet := toSet(cfg.ContainerDirs)
	extSet := toSet(cfg.Extensions)

	var candidates []Candidate

	if err := walkDir(ctx, repoPath, repoPath, 0, cfg.MaxDepth, skipSet, containerSet, extSet, cfg.MinFiles, log, &candidates); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", repoPath, err)
	}

	log.Info("scan complete", "repo", repoPath, "candidates", len(candidates))
	return candidates, nil
}

// walkDir is the recursive implementation of Scan.
func walkDir(
	ctx context.Context,
	root, dir string,
	depth, maxDepth int,
	skipSet, containerSet, extSet map[string]struct{},
	minFiles int,
	log *slog.Logger,
	candidates *[]Candidate,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if depth > maxDepth {
		log.Debug("depth limit reached", "dir", dir, "depth", depth)
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading dir %s: %w", dir, err)
	}

	var fileCount int
	langSet := map[string]struct{}{}
	var subdirs []string

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}

		if e.IsDir() {
			if _, skip := skipSet[e.Name()]; skip {
				log.Debug("skipping dir", "name", e.Name())
				continue
			}
			subdirs = append(subdirs, filepath.Join(dir, e.Name()))
			continue
		}

		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := extSet[ext]; ok {
			fileCount++
			if lang, ok := extToLang(ext); ok {
				langSet[lang] = struct{}{}
			}
		}
	}

	// Compute the relative path from repo root to current dir.
	relPath, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("computing relative path for %s: %w", dir, err)
	}
	relPath = filepath.ToSlash(relPath)

	// The repo root itself — always descend, never a candidate.
	if relPath == "." {
		for _, sub := range subdirs {
			if err := walkDir(ctx, root, sub, depth+1, maxDepth, skipSet, containerSet, extSet, minFiles, log, candidates); err != nil {
				return err
			}
		}
		return nil
	}

	// Container directories are structural scaffolding: descend into their
	// children rather than treating the container itself as a domain candidate.
	dirName := filepath.Base(dir)
	if _, isContainer := containerSet[dirName]; isContainer {
		log.Debug("container dir, descending", "dir", relPath)
		for _, sub := range subdirs {
			if err := walkDir(ctx, root, sub, depth+1, maxDepth, skipSet, containerSet, extSet, minFiles, log, candidates); err != nil {
				return err
			}
		}
		return nil
	}

	// Emit a candidate if the directory meets the file-count threshold.
	if fileCount >= minFiles {
		c := Candidate{
			Path:      relPath,
			FileCount: fileCount,
			Languages: setToSortedSlice(langSet),
		}
		log.Debug("candidate found", "path", relPath, "files", fileCount, "languages", c.Languages)
		*candidates = append(*candidates, c)
	} else {
		log.Debug("below threshold", "dir", relPath, "files", fileCount, "min", minFiles)
	}

	// Always descend into non-container, non-skip subdirs so deeper packages
	// are not missed.
	for _, sub := range subdirs {
		if err := walkDir(ctx, root, sub, depth+1, maxDepth, skipSet, containerSet, extSet, minFiles, log, candidates); err != nil {
			return err
		}
	}

	return nil
}

// toSet converts a slice of strings to a presence set.
func toSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// setToSortedSlice converts a string set to a sorted slice.
func setToSortedSlice(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Insertion sort — sets are small in practice.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// extToLang maps a lowercase file extension to a human-readable language name.
func extToLang(ext string) (string, bool) {
	langs := map[string]string{
		".go":    "Go",
		".ts":    "TypeScript",
		".js":    "JavaScript",
		".py":    "Python",
		".sh":    "Shell",
		".rs":    "Rust",
		".rb":    "Ruby",
		".java":  "Java",
		".kt":    "Kotlin",
		".swift": "Swift",
	}
	l, ok := langs[ext]
	return l, ok
}
