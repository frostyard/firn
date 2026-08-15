// Ported from frostyard/snosi (GPL-3.0-only): behaviors pinned here
// mirror test/snosi-install-test.sh's index/verification block
// (lines ~655-740): good signature produces an index, tampered
// signature/body refuse, latest_channel_version picks the numeric
// maximum, index_object_sha256 lookups. Fixtures are shaped after the
// real published index (channel cayo-ab under os/native/v1/cayo/,
// version style 20260811110127); no test touches the network.
package trust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// writePubring creates a non-empty fake pubring file.
func writePubring(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "os-update-pubring.gpg")
	if err := os.WriteFile(p, []byte("fake pubring bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// okRunner is a fake runner whose gpgv always succeeds, recording
// each call's argv.
func okRunner(calls *[][]string) *runner.Runner {
	return runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			*calls = append(*calls, append([]string{name}, args...))
			return []byte("Good signature"), nil
		},
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
	)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// indexServer serves a SHA256SUMS + SHA256SUMS.gpg pair (and any
// extra objects) under the real published layout
// os/native/v1/<bareProduct>/x86-64/.
func indexServer(t *testing.T, bareProduct string, sums, sig []byte, extra map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	base := "/os/native/v1/" + bareProduct + "/x86-64/"
	mux.HandleFunc(base+"SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) { w.Write(sums) })
	mux.HandleFunc(base+"SHA256SUMS.gpg", func(w http.ResponseWriter, _ *http.Request) { w.Write(sig) })
	for name, body := range extra {
		mux.HandleFunc(base+name, func(w http.ResponseWriter, _ *http.Request) { w.Write(body) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchIndexVerifiesSignatureArgv(t *testing.T) {
	sums := []byte(
		sha256hex([]byte("disk")) + "  cayo-ab_20260811110127.disk.raw.xz\n" +
			sha256hex([]byte("manifest")) + "  cayo-ab_20260811110127.manifest.json\n")
	sig := []byte("binary signature bytes")
	srv := indexServer(t, "cayo", sums, sig, nil)
	pubring := writePubring(t)

	var calls [][]string
	var seenSums, seenSig []byte
	r := runner.NewFake(
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{name}, args...))
			// Capture the temp files while they exist: gpgv must
			// be handed exactly the downloaded bytes.
			var err error
			if seenSig, err = os.ReadFile(args[2]); err != nil {
				t.Errorf("sig file unreadable during gpgv: %v", err)
			}
			if seenSums, err = os.ReadFile(args[3]); err != nil {
				t.Errorf("sums file unreadable during gpgv: %v", err)
			}
			return nil, nil
		},
		func(name string) (string, error) { return name, nil },
	)

	// Product carries the channel-style name; the URL path must use
	// the bare product (see TestBaseURLProductMapping).
	idx, err := FetchIndex(context.Background(), r, Options{
		Origin: srv.URL, Product: "cayo-ab", PubringPath: pubring,
	})
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 command, got %v", calls)
	}
	argv := calls[0]
	if argv[0] != "gpgv" || argv[1] != "--keyring" || argv[2] != pubring {
		t.Fatalf("unexpected gpgv argv: %v", argv)
	}
	if len(argv) != 5 || !strings.HasSuffix(argv[3], "SHA256SUMS.gpg") || !strings.HasSuffix(argv[4], "SHA256SUMS") {
		t.Fatalf("unexpected gpgv file arguments: %v", argv)
	}
	if !bytes.Equal(seenSig, sig) || !bytes.Equal(seenSums, sums) {
		t.Fatal("gpgv did not see the exact downloaded bytes")
	}
	if sha, ok := idx.Sha256("cayo-ab_20260811110127.disk.raw.xz"); !ok || sha != sha256hex([]byte("disk")) {
		t.Fatalf("index lookup wrong: %q %v", sha, ok)
	}
	if _, ok := idx.Sha256("nonexistent.disk.raw.xz"); ok {
		t.Fatal("unknown name must not resolve")
	}
}

// Fake gpgv argv positions used above: gpgv --keyring <pubring> <sig> <sums>.
// (Guard: if FetchIndex's invocation changes shape, the test above
// fails on argv length, not on a silent index-out-of-range.)

func TestFetchIndexRefusesOnGpgvFailure(t *testing.T) {
	// Mirrors snosi-install-test.sh "rejects a tampered signature" /
	// "tampered SHA256SUMS body": any gpgv failure is fail-closed.
	srv := indexServer(t, "cayo", []byte("tampered"), []byte("sig"), nil)
	r := runner.NewFake(
		func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("gpgv: BAD signature")
		},
		func(name string) (string, error) { return name, nil },
	)
	idx, err := FetchIndex(context.Background(), r, Options{
		Origin: srv.URL, Product: "cayo-ab", PubringPath: writePubring(t),
	})
	if idx != nil || err == nil {
		t.Fatal("tampered index must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to trust this index") {
		t.Fatalf("error must state the refusal, got: %v", err)
	}
}

func TestFetchIndexRefusesMissingOrEmptyPubring(t *testing.T) {
	// Ports the bash's [[ -s "$PUBRING" ]] guard: never run gpgv
	// against a missing/empty keyring.
	srv := indexServer(t, "cayo", []byte("x"), []byte("y"), nil)
	empty := filepath.Join(t.TempDir(), "empty.gpg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, pubring := range []string{filepath.Join(t.TempDir(), "missing.gpg"), empty} {
		_, err := FetchIndex(context.Background(), okRunner(&calls), Options{
			Origin: srv.URL, Product: "cayo-ab", PubringPath: pubring,
		})
		if err == nil || !strings.Contains(err.Error(), "update pubring not found or empty") {
			t.Fatalf("pubring %s: want refusal, got %v", pubring, err)
		}
	}
	if len(calls) != 0 {
		t.Fatal("gpgv must not run without a usable pubring")
	}
}

func TestFetchIndexHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	var calls [][]string
	_, err := FetchIndex(context.Background(), okRunner(&calls), Options{
		Origin: srv.URL, Product: "cayo-ab", PubringPath: writePubring(t),
	})
	if err == nil || !strings.Contains(err.Error(), "could not fetch") {
		t.Fatalf("want fetch error, got %v", err)
	}
}

func TestFetchIndexRequiresProduct(t *testing.T) {
	var calls [][]string
	if _, err := FetchIndex(context.Background(), okRunner(&calls), Options{}); err == nil {
		t.Fatal("empty Product must be an error")
	}
}

func TestValidateChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		valid   bool
	}{
		{name: "valid bare name", channel: "cayo", valid: true},
		{name: "valid ab name", channel: "snowfield-ab", valid: true},
		{name: "path traversal", channel: "../cayo-ab"},
		{name: "uppercase", channel: "Cayo-ab"},
		{name: "whitespace", channel: "cayo ab"},
		{name: "regex metacharacters", channel: "cayo.*-ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChannel(tt.channel)
			if tt.valid && err != nil {
				t.Fatalf("ValidateChannel(%q): %v", tt.channel, err)
			}
			if !tt.valid && err == nil {
				t.Fatalf("ValidateChannel(%q) unexpectedly succeeded", tt.channel)
			}
		})
	}
}

func TestFetchIndexRejectsInvalidProductBeforeHTTP(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected HTTP request")
	})}
	var calls [][]string
	_, err := FetchIndex(context.Background(), okRunner(&calls), Options{
		Product: "../cayo-ab",
		Client:  client,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid channel") {
		t.Fatalf("FetchIndex invalid product error = %v", err)
	}
	if called {
		t.Fatal("FetchIndex performed HTTP before rejecting an invalid product")
	}
}

func TestBaseURLProductMapping(t *testing.T) {
	// Pins the live-repository layout: URL path uses the BARE product
	// (os/native/v1/cayo/x86-64) while artifact names keep the full
	// channel prefix. Mapping is snosi-install's product="${channel%-ab}"
	// (line 295) / publish-lib.sh product_path.
	tests := []struct {
		product, name, want string
	}{
		{"cayo-ab", "cayo-ab_20260811110127.manifest.json",
			"https://repository.frostyard.org/os/native/v1/cayo/x86-64/cayo-ab_20260811110127.manifest.json"},
		{"snow-ab", "snow-ab_20260811110127.disk.raw.xz",
			"https://repository.frostyard.org/os/native/v1/snow/x86-64/snow-ab_20260811110127.disk.raw.xz"},
		// Bare product passes through unchanged (bash %-ab semantics).
		{"cayo", "SHA256SUMS",
			"https://repository.frostyard.org/os/native/v1/cayo/x86-64/SHA256SUMS"},
		// Only a TRAILING -ab is stripped.
		{"x-ab-y", "SHA256SUMS",
			"https://repository.frostyard.org/os/native/v1/x-ab-y/x86-64/SHA256SUMS"},
	}
	idx := &Index{entries: map[string]string{}}
	for _, tc := range tests {
		got := idx.ArtifactURL(Options{Product: tc.product}, tc.name)
		if got != tc.want {
			t.Errorf("Product %q: got %q, want %q", tc.product, got, tc.want)
		}
	}
}

func TestLatestVersion(t *testing.T) {
	// Ports latest_channel_version: numeric MAXIMUM, not
	// first-listed (snosi-install-test.sh line 701); entries that are
	// not this channel's manifests are ignored.
	idx := parseIndex([]byte(strings.Join([]string{
		sha256hex([]byte("a")) + "  cayo-ab_20260811110127.manifest.json",
		sha256hex([]byte("b")) + "  cayo-ab_20260101000000.manifest.json",
		sha256hex([]byte("c")) + "  cayo-ab_20260201000000.disk.raw.xz",    // wrong suffix
		sha256hex([]byte("d")) + "  snow-ab_20270101000000.manifest.json",  // other channel
		sha256hex([]byte("e")) + "  cayo-ab_123.manifest.json",             // not 14 digits
		sha256hex([]byte("f")) + "  cayo-ab_202601010000001.manifest.json", // 15 digits
	}, "\n")))

	tests := []struct {
		name, channel, want string
		wantErr             string
	}{
		{"numeric max wins over line order", "cayo-ab", "20260811110127", ""},
		{"other channel resolves independently", "snow-ab", "20270101000000", ""},
		{"no versions for channel", "cayo", "", "no published version found for cayo"},
		// Release-pinning input validation: channel is interpolated
		// into names/URLs, so hostile or malformed input is refused,
		// not matched (the bash interpolated it into a grep regex).
		{"empty channel", "", "", "invalid channel"},
		{"path traversal channel", "../cayo-ab", "", "invalid channel"},
		{"regex metachar channel", "cayo.?-ab", "", "invalid channel"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := idx.LatestVersion(tc.channel)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestFetchManifest(t *testing.T) {
	manifest := []byte(`{"manifest_version":1,` +
		`"config":{"name":"cayo","distribution":"debian","architecture":"x86-64","version":"20260811110127"},` +
		`"packages":[{"type":"deb","name":"bash","version":"5.2","size":123}]}`)
	name := "cayo-ab_20260811110127.manifest.json"

	t.Run("verifies hash then parses", func(t *testing.T) {
		srv := indexServer(t, "cayo", nil, nil, map[string][]byte{name: manifest})
		idx := parseIndex([]byte(sha256hex(manifest) + "  " + name + "\n"))
		m, err := FetchManifest(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			idx, "cayo-ab", "20260811110127")
		if err != nil {
			t.Fatalf("FetchManifest: %v", err)
		}
		if m.Config.Name != "cayo" || m.Architecture() != "x86-64" || len(m.Packages) != 1 || m.Packages[0].Name != "bash" {
			t.Fatalf("manifest parsed wrong: %+v", m)
		}
	})

	t.Run("architecture defaults like the bash jq fallback", func(t *testing.T) {
		m := &Manifest{}
		if m.Architecture() != "x86-64" {
			t.Fatalf("got %q", m.Architecture())
		}
	})

	t.Run("refuses hash mismatch before parsing", func(t *testing.T) {
		// The served bytes differ from the signed index entry: the
		// hash-checked fetch must refuse (fetch_verified_features
		// posture, snosi-install lines 331-333).
		srv := indexServer(t, "cayo", nil, nil, map[string][]byte{name: []byte(`{"config":{}} tampered`)})
		idx := parseIndex([]byte(sha256hex(manifest) + "  " + name + "\n"))
		_, err := FetchManifest(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			idx, "cayo-ab", "20260811110127")
		if err == nil || !strings.Contains(err.Error(), "does not match the signed index hash") {
			t.Fatalf("want hash refusal, got %v", err)
		}
	})

	t.Run("refuses names absent from the signed index", func(t *testing.T) {
		srv := indexServer(t, "cayo", nil, nil, map[string][]byte{name: manifest})
		idx := parseIndex(nil)
		_, err := FetchManifest(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			idx, "cayo-ab", "20260811110127")
		if err == nil || !strings.Contains(err.Error(), "signed index has no entry") {
			t.Fatalf("want index refusal, got %v", err)
		}
	})

	t.Run("pinned version input validation happens before any HTTP", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("no HTTP request may be made for invalid input")
		}))
		defer srv.Close()
		idx := parseIndex(nil)
		o := Options{Origin: srv.URL, Product: "cayo-ab"}
		for _, version := range []string{"", "2026", "20260811110127x", "../../SHA256SUMS", "202608111101271"} {
			if _, err := FetchManifest(context.Background(), o, idx, "cayo-ab", version); err == nil ||
				!strings.Contains(err.Error(), "frozen grammar") {
				t.Errorf("version %q: want grammar refusal, got %v", version, err)
			}
		}
		if _, err := FetchManifest(context.Background(), o, idx, "bad channel!", "20260811110127"); err == nil ||
			!strings.Contains(err.Error(), "invalid channel") {
			t.Errorf("want channel refusal, got %v", err)
		}
	})
}

// --- xz size derivation fixtures -----------------------------------

func xzVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

// buildXZ builds a synthetic single-stream .xz whose Index records the
// given per-block uncompressed sizes. Block payloads are opaque filler
// (never decompressed by the code under test); header, index and
// footer follow the xz file-format spec so real parsers of those
// sections accept it.
func buildXZ(uncompressed []uint64) []byte {
	var f bytes.Buffer
	// Stream Header: magic, flags (check = CRC32), CRC32 of flags.
	f.Write([]byte{0xfd, '7', 'z', 'X', 'Z', 0x00})
	flags := []byte{0x00, 0x01}
	f.Write(flags)
	binary.Write(&f, binary.LittleEndian, crc32.ChecksumIEEE(flags))
	// Opaque filler standing in for the compressed blocks.
	f.Write(bytes.Repeat([]byte{0xaa}, 64))

	// Index: indicator, record count, records, pad, CRC32.
	var idx bytes.Buffer
	idx.WriteByte(0x00)
	idx.Write(xzVarint(uint64(len(uncompressed))))
	for i, u := range uncompressed {
		idx.Write(xzVarint(uint64(40 + i))) // unpadded size (ignored)
		idx.Write(xzVarint(u))
	}
	for idx.Len()%4 != 0 {
		idx.WriteByte(0x00)
	}
	binary.Write(&idx, binary.LittleEndian, crc32.ChecksumIEEE(idx.Bytes()))
	f.Write(idx.Bytes())

	// Stream Footer: CRC32(backward size + flags), backward size
	// (stored as size/4 - 1), flags, "YZ".
	var six [6]byte
	binary.LittleEndian.PutUint32(six[0:4], uint32(idx.Len()/4-1))
	copy(six[4:], flags)
	binary.Write(&f, binary.LittleEndian, crc32.ChecksumIEEE(six[:]))
	f.Write(six[:])
	f.Write([]byte{'Y', 'Z'})
	return f.Bytes()
}

// diskServer serves body at the disk artifact path with real Range
// support (http.ServeContent), recording each request's Range header.
func diskServer(t *testing.T, name string, body []byte, ranges *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/os/native/v1/cayo/x86-64/"+name {
			http.NotFound(w, r)
			return
		}
		*ranges = append(*ranges, r.Header.Get("Range"))
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMinimumDiskBytes(t *testing.T) {
	const version = "20260811110127"
	name := "cayo-ab_" + version + ".disk.raw.xz"
	// Shaped after cayo's real image: 1 GiB ESP + 5 GiB root +
	// 256 MiB verity, spread across two blocks like xz -T0 output.
	blockA := uint64(4 << 30)
	blockB := uint64((1 << 30) + (1 << 30) + (256 << 20)) // remainder
	body := buildXZ([]uint64{blockA, blockB})
	image := int64(blockA + blockB)

	newIndex := func(artifact string) *Index {
		return parseIndex([]byte(sha256hex(body) + "  " + artifact + "\n"))
	}

	t.Run("derives image size from ranged xz index", func(t *testing.T) {
		var ranges []string
		srv := diskServer(t, name, body, &ranges)
		got, err := MinimumDiskBytes(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			newIndex(name), "cayo-ab", version)
		if err != nil {
			t.Fatalf("MinimumDiskBytes: %v", err)
		}
		// The image IS the complete minimum layout (both slots + var min).
		want := image
		if got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
		// Pre-download property: every request was ranged (footer +
		// index), never the whole multi-GiB artifact.
		if len(ranges) != 2 {
			t.Fatalf("expected 2 ranged requests, got %v", ranges)
		}
		for _, rg := range ranges {
			if rg == "" {
				t.Fatalf("un-ranged request would download the whole artifact: %v", ranges)
			}
		}
	})

	t.Run("refuses indexes lacking the xz disk artifact", func(t *testing.T) {
		// A publication without <channel>_<version>.disk.raw.xz has
		// no size metadata derivable pre-download: clear error, no
		// guess (the hand-copied capacity table is gone for good).
		var ranges []string
		srv := diskServer(t, name, body, &ranges)
		_, err := MinimumDiskBytes(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			newIndex("cayo-ab_"+version+".disk.raw"), "cayo-ab", version)
		if err == nil || !strings.Contains(err.Error(), "cannot derive minimum disk size") {
			t.Fatalf("want derivation refusal, got %v", err)
		}
		if len(ranges) != 0 {
			t.Fatal("no HTTP may happen when the index lacks the artifact")
		}
	})

	t.Run("refuses servers that ignore Range", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write(body) // 200, full body: Range unsupported
		}))
		defer srv.Close()
		_, err := MinimumDiskBytes(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			newIndex(name), "cayo-ab", version)
		if err == nil || !strings.Contains(err.Error(), "did not honor Range") {
			t.Fatalf("want Range refusal, got %v", err)
		}
	})

	t.Run("refuses non-xz bytes at the xz name", func(t *testing.T) {
		var ranges []string
		garbage := bytes.Repeat([]byte{0x11}, 4096)
		srv := diskServer(t, name, garbage, &ranges)
		_, err := MinimumDiskBytes(context.Background(), Options{Origin: srv.URL, Product: "cayo-ab"},
			parseIndex([]byte(sha256hex(garbage)+"  "+name+"\n")), "cayo-ab", version)
		if err == nil {
			t.Fatal("garbage bytes must not yield a size")
		}
	})

	t.Run("validates pinned inputs before any HTTP", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("no HTTP request may be made for invalid input")
		}))
		defer srv.Close()
		o := Options{Origin: srv.URL, Product: "cayo-ab"}
		if _, err := MinimumDiskBytes(context.Background(), o, newIndex(name), "cayo-ab", "not-a-version"); err == nil {
			t.Fatal("bad version must be refused")
		}
		if _, err := MinimumDiskBytes(context.Background(), o, newIndex(name), "", version); err == nil {
			t.Fatal("bad channel must be refused")
		}
	})
}

func TestParseXZIndexRejectsCorruption(t *testing.T) {
	good := buildXZ([]uint64{1 << 30})
	// Slice out the index+footer to corrupt the index CRC.
	footer := good[len(good)-12:]
	indexSize := (int64(binary.LittleEndian.Uint32(footer[4:8])) + 1) * 4
	index := append([]byte(nil), good[int64(len(good))-12-indexSize:len(good)-12]...)

	if _, err := parseXZIndex(index); err != nil {
		t.Fatalf("control: pristine index must parse, got %v", err)
	}
	index[1] ^= 0xff // corrupt the record count under the CRC
	if _, err := parseXZIndex(index); err == nil {
		t.Fatal("corrupted index must be rejected")
	}
	if _, err := parseXZIndex([]byte{0x01, 0x00, 0x00, 0x00}); err == nil {
		t.Fatal("wrong indicator byte must be rejected")
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()
	if o.Origin != "https://repository.frostyard.org" {
		t.Errorf("Origin default: %q", o.Origin)
	}
	if o.Arch != "x86-64" {
		t.Errorf("Arch default: %q", o.Arch)
	}
	if o.PubringPath != "" {
		t.Errorf("PubringPath must stay empty (search happens at fetch time): %q", o.PubringPath)
	}
	if o.Client == nil {
		t.Error("Client default must be non-nil")
	}
	// Explicit values survive.
	o = Options{Origin: "http://x/", Arch: "arm64", PubringPath: "/p", Client: http.DefaultClient}.withDefaults()
	if o.Origin != "http://x/" || o.Arch != "arm64" || o.PubringPath != "/p" || o.Client != http.DefaultClient {
		t.Errorf("explicit options overridden: %+v", o)
	}
}

func TestPubringSearch(t *testing.T) {
	// Pin the environment-dependent default locations and their
	// order: the installer ISO ships the key at snosi-install's
	// hardcoded path; installed snosi systems carry the same key
	// only as systemd-sysupdate's import pubring.
	want := []string{
		"/usr/lib/snosi/os-update-pubring.gpg",
		"/usr/lib/systemd/import-pubring.gpg",
	}
	if len(DefaultPubringPaths) != len(want) || DefaultPubringPaths[0] != want[0] || DefaultPubringPaths[1] != want[1] {
		t.Fatalf("DefaultPubringPaths changed: %v", DefaultPubringPaths)
	}

	dir := t.TempDir()
	snosiPath := filepath.Join(dir, "os-update-pubring.gpg")
	systemdPath := filepath.Join(dir, "import-pubring.gpg")
	candidates := []string{snosiPath, systemdPath}
	writeKey := func(p string) {
		if err := os.WriteFile(p, []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Neither exists: error names every tried path.
	if _, err := resolvePubring("", candidates); err == nil ||
		!strings.Contains(err.Error(), snosiPath) || !strings.Contains(err.Error(), systemdPath) {
		t.Fatalf("want error naming both candidates, got %v", err)
	}
	// Only the second exists: search falls through to it.
	writeKey(systemdPath)
	if got, err := resolvePubring("", candidates); err != nil || got != systemdPath {
		t.Fatalf("got %q, %v; want %q", got, err, systemdPath)
	}
	// Both exist: first candidate wins (search order).
	writeKey(snosiPath)
	if got, err := resolvePubring("", candidates); err != nil || got != snosiPath {
		t.Fatalf("got %q, %v; want %q", got, err, snosiPath)
	}
	// Explicit path is used alone — even when candidates exist, a
	// missing/empty explicit path fails closed rather than falling
	// back to the search.
	missing := filepath.Join(dir, "explicit.gpg")
	if _, err := resolvePubring(missing, candidates); err == nil ||
		!strings.Contains(err.Error(), missing) {
		t.Fatalf("explicit missing pubring must refuse, got %v", err)
	}
	empty := filepath.Join(dir, "empty.gpg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolvePubring(empty, candidates); err == nil {
		t.Fatal("explicit empty pubring must refuse")
	}
	if got, err := resolvePubring(systemdPath, candidates); err != nil || got != systemdPath {
		t.Fatalf("explicit existing pubring must be used: %q, %v", got, err)
	}
}
