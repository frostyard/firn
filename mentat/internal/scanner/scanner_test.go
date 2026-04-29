package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/frostyard/firn/mentat/internal/scanner"
)

// mkTree creates a nested directory tree under a temp root and returns the root.
// spec is a slice of relative file paths to create (parent dirs are created automatically).
func mkTree(t *testing.T, spec []string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range spec {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkTree: mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("// placeholder\n"), 0o644); err != nil {
			t.Fatalf("mkTree: write %s: %v", full, err)
		}
	}
	return root
}

// candidatePaths extracts the Path field from a slice of Candidates.
func candidatePaths(cs []scanner.Candidate) []string {
	paths := make([]string, len(cs))
	for i, c := range cs {
		paths[i] = c.Path
	}
	return paths
}

// containsPath reports whether path appears in cs.
func containsPath(cs []scanner.Candidate, path string) bool {
	for _, c := range cs {
		if c.Path == path {
			return true
		}
	}
	return false
}

func TestScan_BasicCandidates(t *testing.T) {
	root := mkTree(t, []string{
		"auth/login.go",
		"auth/logout.go",
		"billing/invoice.go",
		"billing/payment.go",
	})

	cfg := scanner.DefaultConfig()
	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if !containsPath(candidates, "auth") {
		t.Errorf("expected candidate 'auth', got %v", candidatePaths(candidates))
	}
	if !containsPath(candidates, "billing") {
		t.Errorf("expected candidate 'billing', got %v", candidatePaths(candidates))
	}
}

func TestScan_SkipDirs(t *testing.T) {
	root := mkTree(t, []string{
		"auth/login.go",
		"auth/logout.go",
		".git/config",
		".git/HEAD",
		"vendor/lib/lib.go",
		"vendor/lib/util.go",
		"node_modules/react/index.js",
		"node_modules/react/react.js",
	})

	cfg := scanner.DefaultConfig()
	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	for _, skip := range []string{".git", "vendor", "vendor/lib", "node_modules", "node_modules/react"} {
		if containsPath(candidates, skip) {
			t.Errorf("expected %q to be skipped, but it appeared as a candidate; all candidates: %v",
				skip, candidatePaths(candidates))
		}
	}

	if !containsPath(candidates, "auth") {
		t.Errorf("expected 'auth' candidate, got %v", candidatePaths(candidates))
	}
}

func TestScan_ContainerDirsDescent(t *testing.T) {
	// "src", "internal", "cmd" are container dirs — their children are candidates,
	// not the containers themselves.
	root := mkTree(t, []string{
		"internal/auth/auth.go",
		"internal/auth/middleware.go",
		"internal/billing/billing.go",
		"internal/billing/invoice.go",
		"cmd/server/main.go",
		"cmd/server/server.go",
	})

	cfg := scanner.DefaultConfig()
	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	// Children of containers should appear.
	for _, want := range []string{"internal/auth", "internal/billing", "cmd/server"} {
		if !containsPath(candidates, want) {
			t.Errorf("expected candidate %q, got %v", want, candidatePaths(candidates))
		}
	}

	// The containers themselves must NOT appear.
	for _, notWant := range []string{"internal", "cmd"} {
		if containsPath(candidates, notWant) {
			t.Errorf("container dir %q should not appear as a candidate; all candidates: %v",
				notWant, candidatePaths(candidates))
		}
	}
}

func TestScan_MinFilesThreshold(t *testing.T) {
	root := mkTree(t, []string{
		"bigpkg/a.go",
		"bigpkg/b.go",
		"bigpkg/c.go", // 3 files — qualifies with default MinFiles=2
		"tiny/only.go", // 1 file — below threshold
	})

	cfg := scanner.DefaultConfig() // MinFiles = 2
	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if !containsPath(candidates, "bigpkg") {
		t.Errorf("expected candidate 'bigpkg', got %v", candidatePaths(candidates))
	}
	if containsPath(candidates, "tiny") {
		t.Errorf("'tiny' should be filtered out (1 file < MinFiles=2); all candidates: %v",
			candidatePaths(candidates))
	}
}

func TestScan_MinFiles_CustomThreshold(t *testing.T) {
	root := mkTree(t, []string{
		"pkgA/a.go",
		"pkgA/b.go", // 2 files
		"pkgB/a.go",
		"pkgB/b.go",
		"pkgB/c.go", // 3 files
	})

	cfg := scanner.DefaultConfig()
	cfg.MinFiles = 3 // raise threshold

	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if containsPath(candidates, "pkgA") {
		t.Errorf("pkgA has 2 files, should be filtered with MinFiles=3; candidates: %v",
			candidatePaths(candidates))
	}
	if !containsPath(candidates, "pkgB") {
		t.Errorf("expected pkgB (3 files) with MinFiles=3; candidates: %v", candidatePaths(candidates))
	}
}

func TestScan_LanguageDetection(t *testing.T) {
	root := mkTree(t, []string{
		"api/handler.go",
		"api/routes.go",
		"api/middleware.py", // mixed Go + Python
	})

	cfg := scanner.DefaultConfig()
	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if !containsPath(candidates, "api") {
		t.Fatalf("expected candidate 'api', got %v", candidatePaths(candidates))
	}

	var apiC scanner.Candidate
	for _, c := range candidates {
		if c.Path == "api" {
			apiC = c
			break
		}
	}

	hasGo := false
	hasPy := false
	for _, l := range apiC.Languages {
		if l == "Go" {
			hasGo = true
		}
		if l == "Python" {
			hasPy = true
		}
	}
	if !hasGo {
		t.Errorf("expected Go language in api candidate, got %v", apiC.Languages)
	}
	if !hasPy {
		t.Errorf("expected Python language in api candidate, got %v", apiC.Languages)
	}
}

func TestScan_FileCount(t *testing.T) {
	root := mkTree(t, []string{
		"storage/db.go",
		"storage/cache.go",
		"storage/README.md", // .md not in extensions — should not count
	})

	cfg := scanner.DefaultConfig()
	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	if !containsPath(candidates, "storage") {
		t.Fatalf("expected candidate 'storage', got %v", candidatePaths(candidates))
	}

	for _, c := range candidates {
		if c.Path == "storage" {
			if c.FileCount != 2 {
				t.Errorf("expected FileCount=2 for storage (README.md not counted), got %d", c.FileCount)
			}
			break
		}
	}
}

func TestScan_MaxDepth(t *testing.T) {
	// default MaxDepth=3; depth-4 dirs should be ignored
	root := mkTree(t, []string{
		"a/b/c/d/deep.go",
		"a/b/c/d/deep2.go",
		"a/b/c/shallow.go",
		"a/b/c/shallow2.go",
	})

	cfg := scanner.DefaultConfig()
	cfg.MaxDepth = 2 // root=0, a=1, b=2 — c is depth 3 → should be cut off

	candidates, err := scanner.Scan(context.Background(), root, cfg)
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}

	for _, c := range candidates {
		if c.Path == "a/b/c" || c.Path == "a/b/c/d" {
			t.Errorf("depth-exceeded candidate %q should not appear; candidates: %v",
				c.Path, candidatePaths(candidates))
		}
	}
}

func TestScan_ContextCancellation(t *testing.T) {
	root := mkTree(t, []string{
		"auth/a.go",
		"auth/b.go",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := scanner.Scan(ctx, root, scanner.DefaultConfig())
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := scanner.DefaultConfig()

	if cfg.MinFiles != 2 {
		t.Errorf("expected MinFiles=2, got %d", cfg.MinFiles)
	}
	if cfg.MaxDepth != 3 {
		t.Errorf("expected MaxDepth=3, got %d", cfg.MaxDepth)
	}
	if len(cfg.SkipDirs) == 0 {
		t.Error("expected non-empty SkipDirs")
	}
	if len(cfg.ContainerDirs) == 0 {
		t.Error("expected non-empty ContainerDirs")
	}
	if len(cfg.Extensions) == 0 {
		t.Error("expected non-empty Extensions")
	}
}

func TestScan_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		files        []string
		cfg          func() scanner.Config
		wantPaths    []string
		notWantPaths []string
	}{
		{
			name: "src container descends",
			files: []string{
				"src/auth/auth.go",
				"src/auth/token.go",
				"src/storage/db.go",
				"src/storage/repo.go",
			},
			cfg:          scanner.DefaultConfig,
			wantPaths:    []string{"src/auth", "src/storage"},
			notWantPaths: []string{"src"},
		},
		{
			name: "nested skip inside normal dir",
			files: []string{
				"myapp/main.go",
				"myapp/run.go",
				"myapp/testdata/fixture.go",
				"myapp/testdata/other.go",
			},
			cfg:          scanner.DefaultConfig,
			wantPaths:    []string{"myapp"},
			notWantPaths: []string{"myapp/testdata"},
		},
		{
			name: "all extensions recognised",
			files: []string{
				"mixed/a.ts",
				"mixed/b.rs",
				"mixed/c.py",
			},
			cfg:          scanner.DefaultConfig,
			wantPaths:    []string{"mixed"},
			notWantPaths: []string{},
		},
		{
			name: "non-source files do not count",
			files: []string{
				"docs/guide.md",
				"docs/api.md",
			},
			cfg: scanner.DefaultConfig,
			// 0 source files — should not qualify
			wantPaths:    []string{},
			notWantPaths: []string{"docs"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := mkTree(t, tc.files)
			cfg := tc.cfg()

			candidates, err := scanner.Scan(context.Background(), root, cfg)
			if err != nil {
				t.Fatalf("Scan error: %v", err)
			}

			for _, want := range tc.wantPaths {
				if !containsPath(candidates, want) {
					t.Errorf("expected candidate %q; all candidates: %v", want, candidatePaths(candidates))
				}
			}
			for _, notWant := range tc.notWantPaths {
				if containsPath(candidates, notWant) {
					t.Errorf("unexpected candidate %q; all candidates: %v", notWant, candidatePaths(candidates))
				}
			}
		})
	}
}
