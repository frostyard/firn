package installer

// Prefills and suggestion lists for the region page. Every prefill is
// optional and changeable; an empty field keeps the image default.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// sessionDefaults are the running session's regional settings, used as the
// region page's initial values when they are well formed.
type sessionDefaults struct {
	locale   string
	timezone string
	keyboard string
}

// Shapes the schema accepts (internal/recipe/validate.go); prefills that do
// not fit are dropped rather than shown as errors the user did not cause.
var (
	prefillLocaleRe   = regexp.MustCompile(`^[a-z]{2,3}_[A-Z]{2}(\.[A-Za-z0-9-]+)?$`)
	prefillKeyboardRe = regexp.MustCompile(`^[a-z0-9_-]+(:[a-z0-9_-]+(:[a-z0-9_-]+)?)?$`)
)

// detectSession reads the session's locale from the environment, its zone
// from /etc/localtime, and its keyboard from /etc/default/keyboard.
func detectSession(getenv func(string) string, zoneinfoDir, localtime, keyboardFile string) sessionDefaults {
	var s sessionDefaults
	for _, name := range []string{"LC_ALL", "LANG"} {
		if v := normalizeLocale(getenv(name)); prefillLocaleRe.MatchString(v) {
			s.locale = v
			break
		}
	}
	if target, err := os.Readlink(localtime); err == nil {
		if _, zone, ok := strings.Cut(target, "zoneinfo/"); ok {
			if _, err := os.Stat(filepath.Join(zoneinfoDir, zone)); err == nil {
				s.timezone = zone
			}
		}
	}
	if kb := readXKB(keyboardFile); prefillKeyboardRe.MatchString(kb) {
		s.keyboard = kb
	}
	return s
}

// normalizeLocale spells the common "utf8" codeset the way the image's
// locale list does.
func normalizeLocale(v string) string {
	if lang, enc, ok := strings.Cut(v, "."); ok && strings.EqualFold(strings.ReplaceAll(enc, "-", ""), "utf8") {
		return lang + ".UTF-8"
	}
	return v
}

// readXKB returns LAYOUT[:VARIANT] from a Debian-style keyboard file, taking
// the first layout when several are configured.
func readXKB(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(v, `"'`)
	}
	layout, _, _ := strings.Cut(vals["XKBLAYOUT"], ",")
	variant, _, _ := strings.Cut(vals["XKBVARIANT"], ",")
	if layout == "" {
		return ""
	}
	if variant != "" {
		return layout + ":" + variant
	}
	return layout
}

// regionLists are the region page's suggestions.
type regionLists struct {
	locales   []string
	timezones []string
	keyboards []string
	// keyboardNames maps a LAYOUT[:VARIANT] to its human name.
	keyboardNames map[string]string
}

func loadRegionLists(zoneinfoDir, supportedLocales, xkbRules string) regionLists {
	l := regionLists{
		locales:   readLocales(supportedLocales),
		timezones: timezoneSuggestions(zoneinfoDir),
	}
	l.keyboards, l.keyboardNames = readKeyboards(xkbRules)
	return l
}

// readLocales lists the UTF-8 locales glibc supports, falling back to a
// common list.
func readLocales(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return commonLocales
	}
	defer func() { _ = f.Close() }()
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, _, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !strings.HasSuffix(name, ".UTF-8") || !prefillLocaleRe.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return commonLocales
	}
	sort.Strings(out)
	return out
}

// readKeyboards lists XKB layouts and variants from an evdev/base.lst rules
// file, falling back to a common list.
func readKeyboards(path string) ([]string, map[string]string) {
	names := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return commonKeyboards, names
	}
	defer func() { _ = f.Close() }()
	var out []string
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "!") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "!"))
			continue
		}
		code, desc, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		desc = strings.TrimSpace(desc)
		switch section {
		case "layout":
			names[code] = desc
			out = append(out, code)
		case "variant":
			// "nodeadkeys      de: German (no dead keys)"
			layout, name, ok := strings.Cut(desc, ":")
			if !ok {
				continue
			}
			key := strings.TrimSpace(layout) + ":" + code
			names[key] = strings.TrimSpace(name)
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return commonKeyboards, names
	}
	sort.Strings(out)
	return out, names
}

// timezoneSuggestions lists zone names from zoneinfoDir (up to three
// levels, e.g. America/Argentina/Buenos_Aires), falling back to a small
// common list when the dir is empty or unreadable. It mirrors the TUI's.
func timezoneSuggestions(zoneinfoDir string) []string {
	if zoneinfoDir == "" {
		return commonTimezones
	}
	skip := map[string]bool{
		"posix": true, "right": true, "SECURITY": true, "leapseconds": true,
		"leap-seconds.list": true, "zone.tab": true, "zone1970.tab": true,
		"iso3166.tab": true, "tzdata.zi": true,
	}
	var zones []string
	var collect func(rel string, depth int)
	collect = func(rel string, depth int) {
		entries, err := os.ReadDir(filepath.Join(zoneinfoDir, rel))
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if rel == "" {
				if skip[name] || strings.Contains(name, ".") {
					continue
				}
				if r := rune(name[0]); !unicode.IsUpper(r) {
					continue // localtime, posixrules, and friends
				}
			}
			full := name
			if rel != "" {
				full = rel + "/" + name
			}
			if e.IsDir() {
				if depth < 3 {
					collect(full, depth+1)
				}
				continue
			}
			zones = append(zones, full)
		}
	}
	collect("", 1)
	if len(zones) == 0 {
		return commonTimezones
	}
	sort.Strings(zones)
	return zones
}

var commonTimezones = []string{
	"UTC",
	"America/Chicago", "America/Denver", "America/Los_Angeles", "America/New_York",
	"Asia/Shanghai", "Asia/Tokyo", "Australia/Sydney",
	"Europe/Amsterdam", "Europe/Berlin", "Europe/London", "Europe/Paris",
}

var commonLocales = []string{
	"en_US.UTF-8", "en_GB.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8", "es_ES.UTF-8",
	"it_IT.UTF-8", "pt_BR.UTF-8", "nl_NL.UTF-8", "pl_PL.UTF-8", "sv_SE.UTF-8",
	"da_DK.UTF-8", "fi_FI.UTF-8", "nb_NO.UTF-8", "cs_CZ.UTF-8", "ru_RU.UTF-8",
	"ja_JP.UTF-8", "ko_KR.UTF-8", "zh_CN.UTF-8",
}

var commonKeyboards = []string{
	"us", "us:intl", "gb", "de", "de:nodeadkeys", "fr", "es", "it", "pt",
	"br", "nl", "pl", "se", "no", "dk", "fi", "cz", "jp", "ru",
}
