package flatpak

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// CoreLabel is the OCI image label carrying the image's core Flatpak set
// (docs/specs/core-flatpaks-label.md, ADR-0018).
const CoreLabel = "org.frostyard.core-flatpaks"

// coreLabelVersion is the only label format version this firn understands.
const coreLabelVersion = 1

// appIDRE is Flatpak's application ID grammar: at least three dot-separated
// elements of [A-Za-z0-9_-], none starting with a digit, '-' only in the
// last element. The 255-character limit is checked separately.
var appIDRE = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*\.){2,}[A-Za-z_-][A-Za-z0-9_-]*$`)

// ErrMalformedCoreLabel wraps every rejection of a present label, so
// callers can tell a malformed label from other failures.
var ErrMalformedCoreLabel = errors.New("malformed " + CoreLabel + " label")

// coreLabel mirrors the label's JSON. Pointers distinguish a missing or
// null field from a zero value.
type coreLabel struct {
	Version  *int             `json:"version"`
	Flatpaks *[]coreLabelItem `json:"flatpaks"`
}

type coreLabelItem struct {
	ID   *string `json:"id"`
	Name *string `json:"name"`
}

// CoreApp is one entry of an image's core Flatpak set.
type CoreApp struct {
	ID   string
	Name string
}

// ParseCoreLabel returns the app IDs of an image's core Flatpak set from the
// value of its CoreLabel. present reports whether the label key exists.
//
// An absent label, or a valid label with an empty flatpaks array, is no
// core set: (nil, nil). Anything else that does not match the spec is an
// error wrapping ErrMalformedCoreLabel. Duplicate IDs are dropped, keeping
// the first occurrence.
func ParseCoreLabel(value string, present bool) ([]string, error) {
	apps, err := ParseCoreLabelApps(value, present)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, app := range apps {
		ids = append(ids, app.ID)
	}
	return ids, nil
}

// ParseCoreLabelApps is ParseCoreLabel keeping each app's display name, for
// the wizard's preview.
func ParseCoreLabelApps(value string, present bool) ([]CoreApp, error) {
	if !present {
		return nil, nil
	}
	malformed := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrMalformedCoreLabel, fmt.Sprintf(format, args...))
	}
	if value == "" {
		return nil, malformed("empty value")
	}
	// Decode accepts a bare null as an empty object; the label must be one.
	if bytes.Equal(bytes.TrimSpace([]byte(value)), []byte("null")) {
		return nil, malformed("not a JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	var label coreLabel
	if err := dec.Decode(&label); err != nil {
		return nil, malformed("%v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, malformed("trailing data after the JSON object")
	}
	if label.Version == nil {
		return nil, malformed("version is missing or null")
	}
	if *label.Version != coreLabelVersion {
		return nil, malformed("unsupported version %d", *label.Version)
	}
	if label.Flatpaks == nil {
		return nil, malformed("flatpaks is missing or null")
	}
	// encoding/json matches field names case-insensitively, so
	// DisallowUnknownFields alone accepts "VERSION" or "Id". The spec's
	// names are exact; check them at both object levels.
	if err := exactFieldNames(value); err != nil {
		return nil, malformed("%v", err)
	}
	var apps []CoreApp
	seen := make(map[string]bool, len(*label.Flatpaks))
	for i, item := range *label.Flatpaks {
		if item.ID == nil || *item.ID == "" || len(*item.ID) > 255 || !appIDRE.MatchString(*item.ID) {
			return nil, malformed("flatpaks[%d]: invalid Flatpak application ID", i)
		}
		if item.Name == nil || strings.TrimSpace(*item.Name) == "" {
			return nil, malformed("flatpaks[%d]: name is missing or empty", i)
		}
		// Names reach the installer console before the image is verified;
		// control characters and escape sequences must not.
		if strings.IndexFunc(*item.Name, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
			return nil, malformed("flatpaks[%d]: name contains a non-printable character", i)
		}
		if !seen[*item.ID] {
			seen[*item.ID] = true
			apps = append(apps, CoreApp{ID: *item.ID, Name: *item.Name})
		}
	}
	return apps, nil
}

// exactFieldNames rejects any key in the label object, or in a flatpaks
// entry, that is not spelled exactly as the spec spells it. It runs after
// the typed decode, so the shapes it walks are already known to be valid.
func exactFieldNames(value string) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &top); err != nil {
		return err
	}
	for key := range top {
		if key != "version" && key != "flatpaks" {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(top["flatpaks"], &items); err != nil {
		return err
	}
	for i, item := range items {
		for key := range item {
			if key != "id" && key != "name" {
				return fmt.Errorf("flatpaks[%d]: unknown field %q", i, key)
			}
		}
	}
	return nil
}
