package enroll

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/runner"
)

type call struct {
	name string
	args []string
}
type recorder struct {
	calls   []call
	respond func(call) ([]byte, error)
}

func (rec *recorder) runner() *runner.Runner {
	return runner.NewFake(func(_ context.Context, name string, args ...string) ([]byte, error) {
		c := call{name, args}
		rec.calls = append(rec.calls, c)
		if rec.respond != nil {
			return rec.respond(c)
		}
		return nil, nil
	}, func(name string) (string, error) { return "/usr/bin/" + name, nil })
}
func (c call) argv() []string { return append([]string{c.name}, c.args...) }
func (rec *recorder) findCall(name string) int {
	for i, c := range rec.calls {
		if c.name == name {
			return i
		}
	}
	return -1
}
func assertCall(t *testing.T, rec *recorder, i int, name string, args ...string) {
	t.Helper()
	if i >= len(rec.calls) {
		t.Fatalf("missing call %d: %+v", i, rec.calls)
	}
	if got, want := rec.calls[i].argv(), append([]string{name}, args...); !slices.Equal(got, want) {
		t.Errorf("call %d = %v, want %v", i, got, want)
	}
}

func TestStageMOK(t *testing.T) {
	dir := t.TempDir()
	cert, pw := filepath.Join(dir, "mok.pem"), filepath.Join(dir, "password")
	if err := os.WriteFile(cert, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pw, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var importedHash string
	rec := &recorder{respond: func(c call) ([]byte, error) {
		if c.name == "mokutil" && strings.HasPrefix(c.args[0], "--generate-hash=") {
			return []byte("fakehash\n"), nil
		}
		if c.name == "mokutil" && c.args[0] == "--import" {
			b, err := os.ReadFile(c.args[3])
			importedHash = string(b)
			return nil, err
		}
		return nil, nil
	}}
	if err := StageMOK(context.Background(), rec.runner(), cert, pw); err != nil {
		t.Fatal(err)
	}
	assertCall(t, rec, 0, "mokutil", "--generate-hash=hunter2")
	der := rec.calls[1].args[6]
	assertCall(t, rec, 1, "openssl", "x509", "-in", cert, "-outform", "DER", "-out", der)
	hash := rec.calls[2].args[3]
	assertCall(t, rec, 2, "mokutil", "--import", der, "--hash-file", hash)
	if importedHash != "fakehash\n" {
		t.Errorf("hash = %q", importedHash)
	}
	for _, path := range []string{der, hash} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("scratch %s still exists: %v", path, err)
		}
	}
}

func TestStageMOKMissingCert(t *testing.T) {
	rec := &recorder{}
	if err := StageMOK(context.Background(), rec.runner(), "/missing-cert", "/missing-pw"); err == nil || !strings.Contains(err.Error(), "MOK certificate not found") {
		t.Fatalf("error = %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("commands run: %v", rec.calls)
	}
}
