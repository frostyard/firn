package progress

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNDJSONStream(t *testing.T) {
	var buf bytes.Buffer
	em := NewNDJSON(&buf)

	events := []Event{
		Start{Protocol: Protocol, Firn: "0.1.0", Steps: []Step{{Name: "preflight", Weight: 2}, {Name: "partition", Weight: 5}}},
		StepStart{Index: 0, Name: "preflight"},
		Warning{Code: CodeNoTPM, Message: "no TPM device; skipping enrollment"},
		StepProgress{Index: 1, Fraction: 0.5, Bytes: 512, TotalBytes: 1024},
		Info{Message: "writing image"},
		Summary{Items: []SummaryItem{{Code: CodeFlatpakUnreachable, Detail: "org.mozilla.firefox"}}},
		RecoveryKey{Key: "aaaa-bbbb"},
		Done{OK: true, BootEntry: "0003"},
	}
	for _, e := range events {
		if err := em.Emit(e); err != nil {
			t.Fatalf("emit %s: %v", e.Kind(), err)
		}
	}

	want := []string{
		`{"event":"start","protocol":1,"firn":"0.1.0","steps":[{"name":"preflight","weight":2},{"name":"partition","weight":5}]}`,
		`{"event":"step_start","index":0,"name":"preflight"}`,
		`{"event":"warning","code":"no_tpm","message":"no TPM device; skipping enrollment"}`,
		`{"event":"step_progress","index":1,"fraction":0.5,"bytes":512,"total_bytes":1024}`,
		`{"event":"info","message":"writing image"}`,
		`{"event":"summary","items":[{"code":"flatpak_unreachable","detail":"org.mozilla.firefox"}]}`,
		`{"event":"recovery_key","key":"aaaa-bbbb"}`,
		`{"event":"done","ok":true,"boot_entry":"0003"}`,
	}

	sc := bufio.NewScanner(&buf)
	for i := 0; sc.Scan(); i++ {
		if i >= len(want) {
			t.Fatalf("unexpected extra line %q", sc.Text())
		}
		if sc.Text() != want[i] {
			t.Errorf("line %d:\n got %s\nwant %s", i, sc.Text(), want[i])
		}
		// Every line must be standalone valid JSON with an event tag.
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Errorf("line %d not valid JSON: %v", i, err)
		} else if _, ok := m["event"].(string); !ok {
			t.Errorf("line %d missing event tag", i)
		}
	}
}

func TestErrorEventOmitsEmptyStep(t *testing.T) {
	var buf bytes.Buffer
	if err := NewNDJSON(&buf).Emit(Error{Code: "download_failed", Message: "checksum mismatch"}); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	want := `{"event":"error","code":"download_failed","message":"checksum mismatch"}`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestOversizedLineRejected(t *testing.T) {
	var buf bytes.Buffer
	if err := NewNDJSON(&buf).Emit(Info{Message: strings.Repeat("x", maxLine)}); err == nil {
		t.Error("expected oversized event to be rejected")
	}
	if buf.Len() != 0 {
		t.Error("oversized event must not be partially written")
	}
}

func TestEmptyPayloadEvent(t *testing.T) {
	// A hypothetical fieldless event must still serialize as a lone tag.
	var buf bytes.Buffer
	if err := NewNDJSON(&buf).Emit(Summary{}); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("not valid JSON: %v (%s)", err, buf.String())
	}
}
