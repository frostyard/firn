package flatpak

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestParseCoreLabel pins every row of the ADR-0018 / core-flatpaks-label
// spec table. The rejection cases mirror snosi's producer test
// (test/core-flatpaks-test.sh) so producer and consumer agree on them.
func TestParseCoreLabel(t *testing.T) {
	const app = `{"id":"org.example.App","name":"App"}`
	for _, tc := range []struct {
		name    string
		value   string
		present bool
		want    []string // nil with wantErr false means no core set
		wantErr bool
	}{
		{name: "key absent", present: false},
		{name: "valid set", present: true,
			value: `{"version":1,"flatpaks":[{"id":"org.mozilla.firefox","name":"Firefox"},` + app + `]}`,
			want:  []string{"org.mozilla.firefox", "org.example.App"}},
		{name: "re-encoded with whitespace", present: true,
			value: "{\n  \"version\": 1,\n  \"flatpaks\": [" + app + "]\n}\n",
			want:  []string{"org.example.App"}},
		{name: "empty flatpaks array is no core set", present: true,
			value: `{"version":1,"flatpaks":[]}`},
		{name: "duplicates keep the first occurrence", present: true,
			value: `{"version":1,"flatpaks":[{"id":"org.b.B","name":"B"},` + app + `,{"id":"org.b.B","name":"B again"}]}`,
			want:  []string{"org.b.B", "org.example.App"}},
		{name: "name with spaces and non-ASCII letters", present: true,
			value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":"Visionneuse d’images"}]}`,
			want:  []string{"org.example.App"}},
		{name: "dash in the last element", present: true,
			value: `{"version":1,"flatpaks":[{"id":"org.example.my-app","name":"App"}]}`,
			want:  []string{"org.example.my-app"}},

		{name: "empty string", present: true, value: "", wantErr: true},
		{name: "null", present: true, value: "null", wantErr: true},
		{name: "not JSON", present: true, value: "not json", wantErr: true},
		{name: "array, not object", present: true, value: "[" + app + "]", wantErr: true},
		{name: "trailing data", present: true, value: `{"version":1,"flatpaks":[` + app + `]} {}`, wantErr: true},
		{name: "version missing", present: true, value: `{"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "version null", present: true, value: `{"version":null,"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "version 2", present: true, value: `{"version":2,"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "version 1.0", present: true, value: `{"version":1.0,"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "version true", present: true, value: `{"version":true,"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "version string", present: true, value: `{"version":"1","flatpaks":[` + app + `]}`, wantErr: true},
		{name: "flatpaks missing", present: true, value: `{"version":1}`, wantErr: true},
		{name: "flatpaks null", present: true, value: `{"version":1,"flatpaks":null}`, wantErr: true},
		{name: "unknown top-level field", present: true, value: `{"version":1,"flatpaks":[` + app + `],"extra":1}`, wantErr: true},
		{name: "uppercase top-level fields", present: true, value: `{"VERSION":1,"FLATPAKS":[` + app + `]}`, wantErr: true},
		{name: "capitalized version only", present: true, value: `{"Version":1,"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "uppercase entry fields", present: true, value: `{"version":1,"flatpaks":[{"ID":"org.example.App","NAME":"App"}]}`, wantErr: true},
		{name: "case-variant duplicate key", present: true, value: `{"version":1,"Version":2,"flatpaks":[` + app + `]}`, wantErr: true},
		{name: "unknown entry field", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":"App","x":1}]}`, wantErr: true},
		{name: "null entry", present: true, value: `{"version":1,"flatpaks":[null]}`, wantErr: true},
		{name: "id missing", present: true, value: `{"version":1,"flatpaks":[{"name":"App"}]}`, wantErr: true},
		{name: "id empty", present: true, value: `{"version":1,"flatpaks":[{"id":"","name":"App"}]}`, wantErr: true},
		{name: "id with two elements", present: true, value: `{"version":1,"flatpaks":[{"id":"org.App","name":"App"}]}`, wantErr: true},
		{name: "element starting with a digit", present: true, value: `{"version":1,"flatpaks":[{"id":"org.1example.App","name":"App"}]}`, wantErr: true},
		{name: "dash before the last element", present: true, value: `{"version":1,"flatpaks":[{"id":"org.ex-ample.App","name":"App"}]}`, wantErr: true},
		{name: "id with a space", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.My App","name":"App"}]}`, wantErr: true},
		{name: "id with a leading option dash", present: true, value: `{"version":1,"flatpaks":[{"id":"--system","name":"App"}]}`, wantErr: true},
		{name: "id over 255 characters", present: true,
			value: `{"version":1,"flatpaks":[{"id":"org.example.` + strings.Repeat("a", 250) + `","name":"App"}]}`, wantErr: true},
		{name: "name with an escape sequence", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":"B\u001b[2J\u001b[31mEVIL"}]}`, wantErr: true},
		{name: "name with a newline", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":"B\nC"}]}`, wantErr: true},
		{name: "name with a tab", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":"B\tC"}]}`, wantErr: true},
		{name: "name with a zero-width joiner", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":"B\u200dC"}]}`, wantErr: true},
		{name: "name missing", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App"}]}`, wantErr: true},
		{name: "name empty", present: true, value: `{"version":1,"flatpaks":[{"id":"org.example.App","name":" "}]}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCoreLabel(tc.value, tc.present)
			if tc.wantErr {
				if !errors.Is(err, ErrMalformedCoreLabel) {
					t.Fatalf("ParseCoreLabel(%q) error = %v, want ErrMalformedCoreLabel", tc.value, err)
				}
				if got != nil {
					t.Fatalf("ParseCoreLabel(%q) returned %v alongside an error", tc.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCoreLabel(%q) error = %v", tc.value, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ParseCoreLabel(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// The error names the actual problem, so the wizard note and the preflight
// error tell the user what is wrong with the label.
func TestParseCoreLabelMessages(t *testing.T) {
	for value, want := range map[string]string{
		`{"version":1}`:                           "flatpaks is missing or null",
		`{"flatpaks":[]}`:                         "version is missing or null",
		`{"version":1,"FLATPAKS":[]}`:             `unknown field "FLATPAKS"`,
		`{"version":1,"flatpaks":[],"Version":1}`: `unknown field "Version"`,
	} {
		_, err := ParseCoreLabel(value, true)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseCoreLabel(%s) error = %v, want it to mention %q", value, err, want)
		}
	}
}
