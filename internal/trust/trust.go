// Ported from frostyard/snosi (GPL-3.0-only),
// shared/native-installer/tree/usr/libexec/snosi-install
// (fetch_verified_index, index_object_sha256, latest_channel_version,
// minimum_disk_bytes, and the hash-checked manifest fetch in main /
// fetch_verified_features).
//
// Package trust fetches and verifies the signed native-A/B release
// index (SHA256SUMS + SHA256SUMS.gpg) and the artifacts it lists.
// Everything downloaded is either signature-verified (the index, via
// gpgv against the shipped pubring) or hash-verified against that
// signed index (manifests) before any byte of it is trusted. Any
// verification failure is fatal to the fetch: this package never
// falls back to unverified data.
package trust

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frostyard/firn/internal/runner"
)

// Defaults mirroring snosi-install's top-level configuration.
const (
	// DefaultOrigin is the published artifact origin.
	DefaultOrigin = "https://repository.frostyard.org"
	// DefaultArch is the only architecture published today
	// (docs/native-ab-contracts.md pins the x86-64 path segment).
	DefaultArch = "x86-64"
)

// DefaultPubringPaths are searched IN ORDER when Options.PubringPath
// is empty; the first existing non-empty file wins. The pubring lives
// at different paths per environment: the installer ISO ships it at
// snosi-install's hardcoded location (PUBRING_DEFAULT, line 81),
// while installed snosi systems carry the same key only as
// systemd-sysupdate's import pubring. Firn runs in both. The keyring
// FILE is the trust root — no fingerprint is pinned here.
var DefaultPubringPaths = []string{
	"/usr/lib/snosi/os-update-pubring.gpg", // installer ISO / live media
	"/usr/lib/systemd/import-pubring.gpg",  // installed snosi systems
}

// Options configures index/artifact fetching. Zero-value fields take
// the documented defaults.
//
// Product is the channel-style product name firn recipes carry
// ("cayo-ab", "snow-ab"). The published URL path uses the BARE
// product ("cayo"): artifact names inside the index keep the full
// channel prefix (cayo-ab_<version>.manifest.json), but the
// directory is os/native/v1/cayo/x86-64 — see baseURL.
//
// PubringPath: when set, only that file is used (fail-closed if
// missing/empty); when empty, DefaultPubringPaths are searched in
// order at fetch time.
type Options struct {
	Origin      string
	Product     string
	Arch        string
	PubringPath string
	Client      *http.Client
}

// withDefaults returns o with zero-value fields replaced by defaults.
// The 30s client timeout ports snosi-install's `curl --connect-timeout
// 10 --max-time 30` posture for the small metadata fetches this
// package performs.
func (o Options) withDefaults() Options {
	if o.Origin == "" {
		o.Origin = DefaultOrigin
	}
	if o.Arch == "" {
		o.Arch = DefaultArch
	}
	// PubringPath deliberately stays empty here: an empty path means
	// "search DefaultPubringPaths", resolved by resolvePubring at
	// fetch time (the right answer depends on the running system).
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return o
}

// baseURL is <origin>/os/native/v1/<bare-product>/<arch>, the
// per-product publication directory (docs/native-ab-contracts.md §5;
// publish-lib.sh product_path). The path segment is the product with
// a trailing "-ab" stripped: snosi-install derives it the same way
// from the channel (`product="${channel%-ab}"`, fetch_verified_index
// line 295) — bash's `%-ab` removes only a trailing occurrence and
// leaves other names untouched, which is exactly TrimSuffix. So
// Product "cayo-ab" and bare "cayo" both address os/native/v1/cayo.
func (o Options) baseURL() string {
	bare := strings.TrimSuffix(o.Product, "-ab")
	return strings.TrimSuffix(o.Origin, "/") + "/os/native/v1/" + bare + "/" + o.Arch
}

// channelRE and versionRE pin the frozen naming grammar
// (snosi prepare-native-publication.sh lines 162-173,
// docs/native-ab-contracts.md §1-§2). Channel and version are caller
// inputs (a pinned release is typed by a human) that get interpolated
// into URLs and artifact names, so they are validated before use —
// the bash interpolated the channel straight into a grep regex
// (latest_channel_version, line 341), which Go's exact matching
// avoids, but URL-path injection is still refused here.
var (
	channelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	versionRE = regexp.MustCompile(`^[0-9]{14}$`)
)

func validateChannel(channel string) error {
	if !channelRE.MatchString(channel) {
		return fmt.Errorf("invalid channel %q (expected lowercase name like cayo-ab)", channel)
	}
	return nil
}

func validateVersion(version string) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("invalid version %q: does not match the frozen grammar ^[0-9]{14}$ (docs/native-ab-contracts.md §2)", version)
	}
	return nil
}

// Index is a signature-verified SHA256SUMS: artifact name → sha256
// (lowercase hex). Only FetchIndex constructs one, so holding an
// *Index means the signature already checked out.
type Index struct {
	entries map[string]string
}

// Sha256 reports the expected sha256 for name from the verified
// index, or ok=false if the index has no such artifact (port of
// index_object_sha256, snosi-install line 313).
func (i *Index) Sha256(name string) (string, bool) {
	sha, ok := i.entries[name]
	return sha, ok
}

// LatestVersion returns the numeric MAXIMUM 14-digit version among
// this channel's manifest entries — line order in SHA256SUMS is not
// guaranteed newest-first (port of latest_channel_version,
// snosi-install lines 337-342; same technique as
// snosi-update-status). Errors when the channel has no published
// manifest (snosi-install line 1518's die).
func (i *Index) LatestVersion(channel string) (string, error) {
	if err := validateChannel(channel); err != nil {
		return "", err
	}
	prefix := channel + "_"
	const suffix = ".manifest.json"
	latest := ""
	for name := range i.entries {
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if !versionRE.MatchString(v) {
			continue
		}
		// Fixed-width 14-digit strings: lexicographic max ==
		// numeric max (bash used `sort -n`).
		if v > latest {
			latest = v
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no published version found for %s in the verified index", channel)
	}
	return latest, nil
}

// ArtifactURL is the download URL for a named artifact under the
// product's publication directory (bash INDEX_BASE_URL, line 308).
func (i *Index) ArtifactURL(o Options, name string) string {
	return o.withDefaults().baseURL() + "/" + name
}

// fetchBytes GETs url and returns the body, failing on any non-200
// status (curl -f).
func fetchBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not fetch %s: HTTP %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", url, err)
	}
	return body, nil
}

// FetchIndex downloads SHA256SUMS + SHA256SUMS.gpg from
// <origin>/os/native/v1/<bare-product>/<arch>/, gpgv-verifies the
// signature against the pubring, and returns the parsed index (port
// of fetch_verified_index, snosi-install lines 288-309). Fail-closed:
// a missing/empty pubring or any gpgv failure refuses the index —
// there is no unverified fallback.
func FetchIndex(ctx context.Context, r *runner.Runner, o Options) (*Index, error) {
	o = o.withDefaults()
	if o.Product == "" {
		return nil, errors.New("trust: Options.Product is required")
	}
	base := o.baseURL()

	sums, err := fetchBytes(ctx, o.Client, base+"/SHA256SUMS")
	if err != nil {
		return nil, err
	}
	sig, err := fetchBytes(ctx, o.Client, base+"/SHA256SUMS.gpg")
	if err != nil {
		return nil, err
	}

	// [[ -s "$PUBRING" ]] — a missing or empty pubring must refuse,
	// not "verify" against nothing (snosi-install line 303).
	pubring, err := resolvePubring(o.PubringPath, DefaultPubringPaths)
	if err != nil {
		return nil, err
	}

	// gpgv works on files, so land both downloads in a private temp
	// dir for the verification call only.
	dir, err := os.MkdirTemp("", "firn-trust-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	sumsPath := filepath.Join(dir, "SHA256SUMS")
	sigPath := filepath.Join(dir, "SHA256SUMS.gpg")
	if err := os.WriteFile(sumsPath, sums, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(sigPath, sig, 0o600); err != nil {
		return nil, err
	}

	if _, err := r.Run(ctx, "gpgv", "--keyring", pubring, sigPath, sumsPath); err != nil {
		return nil, fmt.Errorf("signature verification FAILED for %s/SHA256SUMS -- refusing to trust this index: %w", base, err)
	}

	return parseIndex(sums), nil
}

// parseIndex parses sha256sum output lines ("<sha256>  <name>").
// Lines that do not look like an entry are ignored, matching the
// bash's awk field matching (index_object_sha256).
func parseIndex(sums []byte) *Index {
	entries := make(map[string]string)
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		// sha256sum prefixes binary-mode names with '*'; the
		// publisher uses text mode, but strip it for safety.
		entries[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return &Index{entries: entries}
}

// resolvePubring picks the keyring file for gpgv. An explicit path is
// used alone (fail-closed when missing/empty, like the bash's
// [[ -s ]] guard); an empty path searches candidates in order for the
// first existing non-empty file, erroring with every tried path so
// the diagnosis names both known locations.
func resolvePubring(explicit string, candidates []string) (string, error) {
	if explicit != "" {
		if fi, err := os.Stat(explicit); err != nil || fi.Size() == 0 {
			return "", fmt.Errorf("update pubring not found or empty: %s", explicit)
		}
		return explicit, nil
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("update pubring not found or empty: tried %s", strings.Join(candidates, ", "))
}

// Manifest is the published <channel>_<version>.manifest.json: mkosi's
// own ManifestFormat=json output, copied verbatim into the publication
// (snosi prepare-native-publication.sh lines 271-275; generate-sbom.sh
// header). Schema observed there and in snosi-install line 1534:
// .config.{name,distribution,architecture,version} and .packages[]
// {type,name,version,size}. It carries NO partition or image sizes —
// see MinimumDiskBytes for what that means.
type Manifest struct {
	ManifestVersion int64          `json:"manifest_version"`
	Config          ManifestConfig `json:"config"`
	Packages        []Package      `json:"packages"`
}

// ManifestConfig is mkosi's .config object. Name is the product
// (== ImageId=), Version the frozen 14-digit version
// (prepare-native-publication.sh lines 159-166).
type ManifestConfig struct {
	Name         string `json:"name"`
	Distribution string `json:"distribution"`
	Architecture string `json:"architecture"`
	Version      string `json:"version"`
}

// Package is one mkosi manifest package entry (the fields
// generate-sbom.sh consumes; Size is the installed size mkosi
// records, not an artifact size).
type Package struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
}

// Architecture returns the manifest's architecture, defaulting to
// x86-64 exactly like the bash's `jq -r '.config.architecture //
// "x86-64"'` (snosi-install line 1534).
func (m *Manifest) Architecture() string {
	if m.Config.Architecture == "" {
		return DefaultArch
	}
	return m.Config.Architecture
}

// ManifestName is the published manifest object name for a
// channel+version (docs/native-ab-contracts.md §2 naming).
func ManifestName(channel, version string) string {
	return channel + "_" + version + ".manifest.json"
}

// DiskImageName is the published whole-disk artifact name
// (prepare-native-publication.sh line 241; always xz-compressed in
// real publications).
func DiskImageName(channel, version string) string {
	return channel + "_" + version + ".disk.raw.xz"
}

// FetchManifest downloads <channel>_<version>.manifest.json and
// verifies its sha256 against the signed index BEFORE parsing — the
// same hash-checked-fetch posture as fetch_verified_features
// (snosi-install lines 319-335) and main's manifest fetch (lines
// 1528-1536). Unlike the bash main(), which soft-falls-back to
// arch=x86-64 when the manifest is absent or mismatched, this API
// fails closed and leaves any fallback policy to the caller.
func FetchManifest(ctx context.Context, o Options, i *Index, channel, version string) (*Manifest, error) {
	o = o.withDefaults()
	if err := validateChannel(channel); err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	name := ManifestName(channel, version)
	want, ok := i.Sha256(name)
	if !ok {
		return nil, fmt.Errorf("signed index has no entry for %s", name)
	}
	body, err := fetchBytes(ctx, o.Client, i.ArtifactURL(o, name))
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s does not match the signed index hash", name)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("%s: invalid manifest JSON: %w", name, err)
	}
	return &m, nil
}

// MinimumVarBytes is the minimum /var a fresh install must fit
// (snosi-install minimum_disk_bytes var_min, line 372).
const MinimumVarBytes int64 = 4294967296 // 4 GiB

// MinimumDiskBytes derives the smallest disk a fresh A/B install of
// channel/version fits on: ESP + 2x root slot (A/B) + 2x verity slot
// (A/B) + minimum /var.
//
// UPSTREAM HAZARD FIXED HERE (documented in firn
// docs/design/architecture.md "Preflight"): snosi-install's
// minimum_disk_bytes (lines 355-378) answered this from a per-product
// capacity table hand-copied from the repart configs, so any
// image-size change silently broke the installer. Firn derives the
// answer from the published artifacts instead. What the publication
// actually offers, in preference order:
//
//  1. Explicit size metadata in published JSON: none exists today.
//     The manifest is mkosi's package manifest (config + packages,
//     no partition/image sizes), and publication-info.json — which
//     DOES record artifact sizes (prepare-native-publication.sh
//     lines 335-380) — is deliberately not uploaded; it is a local
//     pipeline record (the bash's own minimum_disk_bytes comment,
//     lines 360-368, and publish-candidate.sh). If the publisher
//     ever starts shipping sizes, prefer them here.
//  2. The disk artifact's own xz container: the xz Index at the END
//     of <channel>_<version>.disk.raw.xz records the exact
//     decompressed image size. Two small HTTP Range requests (footer,
//     then index — a few hundred bytes) recover it PRE-download,
//     which is what this function does.
//
// The decompressed disk image IS the complete minimum layout: mkosi
// Format=disk bakes every partition at build time — ESP, populated A
// slot, both EMPTY B-slot partitions, and the minimum /var (the repart
// definitions 00-esp through 30-var, i.e. exactly snosi's
// esp + 2*root + 2*verity + var_min table). The install minimum is
// therefore the decompressed image size itself; anything beyond it is
// optional /var growth. (Verified against the live cayo-ab
// publication: decompressed size 16.7 GB = the 15.5 GiB contract
// minimum.)
//
// KNOWN GAP: the ranged bytes are served over TLS but are NOT
// hash-verifiable until the full artifact is downloaded, so this
// figure is a capacity/refusal check, not a trust decision — firn's
// stream step independently verifies the signed hash and that the
// real decompressed size fits the disk. Publications without an
// xz disk artifact (or servers without Range support) predate this
// derivation and get a clear error rather than a guess.
func MinimumDiskBytes(ctx context.Context, o Options, i *Index, channel, version string) (int64, error) {
	o = o.withDefaults()
	if err := validateChannel(channel); err != nil {
		return 0, err
	}
	if err := validateVersion(version); err != nil {
		return 0, err
	}
	name := DiskImageName(channel, version)
	if _, ok := i.Sha256(name); !ok {
		// No xz disk artifact in the signed index: either the
		// version does not exist or it was published without xz
		// (prepare-native-publication.sh allows it) — no size is
		// derivable pre-download for such an index.
		return 0, fmt.Errorf("signed index has no entry for %s: cannot derive minimum disk size for this release", name)
	}
	imageBytes, err := diskImageUncompressedBytes(ctx, o.Client, i.ArtifactURL(o, name))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return imageBytes, nil
}

// diskImageUncompressedBytes reads the artifact's xz Stream Footer
// and Index via HTTP Range requests and returns the total
// decompressed size, without downloading the payload.
func diskImageUncompressedBytes(ctx context.Context, client *http.Client, url string) (int64, error) {
	// Stream Footer: last 12 bytes of a (single-stream) .xz file.
	footer, total, err := fetchRange(ctx, client, url, -12, 0)
	if err != nil {
		return 0, err
	}
	if len(footer) != 12 || footer[10] != 'Y' || footer[11] != 'Z' {
		return 0, errors.New("not an xz stream footer (artifact is not xz, or was truncated)")
	}
	if crc32.ChecksumIEEE(footer[4:10]) != binary.LittleEndian.Uint32(footer[0:4]) {
		return 0, errors.New("xz stream footer CRC mismatch")
	}
	// Backward Size field stores (index size / 4) - 1.
	indexSize := (int64(binary.LittleEndian.Uint32(footer[4:8])) + 1) * 4
	indexStart := total - 12 - indexSize
	if indexStart < 12 { // must leave room for the stream header
		return 0, errors.New("xz index does not fit the file (corrupt footer?)")
	}
	index, _, err := fetchRange(ctx, client, url, indexStart, indexStart+indexSize-1)
	if err != nil {
		return 0, err
	}
	return parseXZIndex(index)
}

// fetchRange GETs a byte range. start < 0 means a suffix range of
// -start bytes. Requires a 206 response: a server that ignores Range
// would make us download the multi-GiB artifact just to size it, so
// refuse instead. Returns the bytes and the total object size parsed
// from Content-Range.
func fetchRange(ctx context.Context, client *http.Client, url string, start, end int64) ([]byte, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if start < 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d", start))
	} else {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, 0, fmt.Errorf("server did not honor Range request (HTTP %s): cannot size the artifact without downloading it", resp.Status)
	}
	// Content-Range: bytes <first>-<last>/<total>
	cr := resp.Header.Get("Content-Range")
	slash := strings.LastIndexByte(cr, '/')
	if slash < 0 {
		return nil, 0, fmt.Errorf("missing or malformed Content-Range %q", cr)
	}
	total, err := strconv.ParseInt(cr[slash+1:], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("malformed Content-Range %q", cr)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	return body, total, nil
}

// parseXZIndex sums the Uncompressed Size of every record in an xz
// Index field (indicator 0x00, varint record count, records of two
// varints each, zero padding to 4-byte alignment, CRC32).
func parseXZIndex(index []byte) (int64, error) {
	if len(index) < 8 || index[0] != 0x00 {
		return 0, errors.New("malformed xz index")
	}
	if crc32.ChecksumIEEE(index[:len(index)-4]) != binary.LittleEndian.Uint32(index[len(index)-4:]) {
		return 0, errors.New("xz index CRC mismatch")
	}
	pos := 1
	count, n, err := decodeXZVarint(index[pos:])
	if err != nil {
		return 0, err
	}
	pos += n
	var totalUncompressed int64
	for rec := uint64(0); rec < count; rec++ {
		// Unpadded Size (compressed footprint) — skipped.
		if _, n, err = decodeXZVarint(index[pos:]); err != nil {
			return 0, err
		}
		pos += n
		uncompressed, n, err := decodeXZVarint(index[pos:])
		if err != nil {
			return 0, err
		}
		pos += n
		totalUncompressed += int64(uncompressed)
	}
	// Remaining bytes before the CRC must be zero padding.
	for _, b := range index[pos : len(index)-4] {
		if b != 0 {
			return 0, errors.New("malformed xz index padding")
		}
	}
	if totalUncompressed <= 0 {
		return 0, errors.New("xz index reports a non-positive uncompressed size")
	}
	return totalUncompressed, nil
}

// decodeXZVarint decodes xz's multibyte integer encoding (7 bits per
// byte, low bits first, high bit is the continuation flag, max 9
// bytes).
func decodeXZVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 9; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			if b[i] == 0 && i > 0 {
				return 0, 0, errors.New("non-minimal xz varint")
			}
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("truncated or overlong xz varint")
}
