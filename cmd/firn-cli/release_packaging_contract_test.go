package main

// Contract tests for .goreleaser.yaml: every archive and every deb, rpm and
// apk package ships firn's LICENSE and NOTICE and the third-party notices of
// its binary (ADR-0023, frostyard/firn#151). GoReleaser runs only on a tag,
// so these tests are the pull-request gate of the package contents. A new
// archive or package must be added to the tables below, or the tests fail.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// archiveFile is an entry of an archive's `files`: GoReleaser takes a plain
// path or a map with `src`.
type archiveFile struct {
	Src string
}

func (f *archiveFile) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		f.Src = node.Value
		return nil
	}
	var entry struct {
		Src string `yaml:"src"`
	}
	if err := node.Decode(&entry); err != nil {
		return err
	}
	f.Src = entry.Src
	return nil
}

type nfpmContent struct {
	Src      string `yaml:"src"`
	Dst      string `yaml:"dst"`
	Type     string `yaml:"type"`
	Packager string `yaml:"packager"`
}

type goreleaserConfig struct {
	Archives []struct {
		ID    string        `yaml:"id"`
		Files []archiveFile `yaml:"files"`
	} `yaml:"archives"`
	Nfpms []struct {
		ID          string        `yaml:"id"`
		PackageName string        `yaml:"package_name"`
		Formats     []string      `yaml:"formats"`
		Contents    []nfpmContent `yaml:"contents"`
	} `yaml:"nfpms"`
}

// requiredArchiveFiles names the license and notice files each archive id
// must carry.
var requiredArchiveFiles = map[string][]string{
	"firn":     {"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES"},
	"firn-gui": {"LICENSE", "NOTICE", "gui/THIRD_PARTY_NOTICES"},
}

// nfpmNoticesSource names the third-party notices file of each nfpm id.
var nfpmNoticesSource = map[string]string{
	"firn":     "./THIRD_PARTY_NOTICES",
	"firn-gui": "./gui/THIRD_PARTY_NOTICES",
}

// licenseContents returns the 3 contents entries a package of the given
// format must have for package name pkg and notices source notices: the
// conventional license paths of each format (ADR-0023).
func licenseContents(format, pkg, notices string) ([]nfpmContent, error) {
	switch format {
	case "deb":
		doc := "/usr/share/doc/" + pkg + "/"
		return []nfpmContent{
			{Src: "./LICENSE", Dst: doc + "copyright", Packager: "deb"},
			{Src: "./NOTICE", Dst: doc + "NOTICE", Packager: "deb"},
			{Src: notices, Dst: doc + "THIRD_PARTY_NOTICES", Packager: "deb"},
		}, nil
	case "rpm", "apk":
		dir := "/usr/share/licenses/" + pkg + "/"
		typ := ""
		if format == "rpm" {
			typ = "license"
		}
		return []nfpmContent{
			{Src: "./LICENSE", Dst: dir + "LICENSE", Type: typ, Packager: format},
			{Src: "./NOTICE", Dst: dir + "NOTICE", Type: typ, Packager: format},
			{Src: notices, Dst: dir + "THIRD_PARTY_NOTICES", Type: typ, Packager: format},
		}, nil
	default:
		return nil, fmt.Errorf("format %q has no license path convention in this test; add one (ADR-0023)", format)
	}
}

func loadGoreleaserConfig(t *testing.T) goreleaserConfig {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	var config goreleaserConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse .goreleaser.yaml: %v", err)
	}
	return config
}

func TestReleasePackagingArchivesShipLicenses(t *testing.T) {
	config := loadGoreleaserConfig(t)
	if len(config.Archives) == 0 {
		t.Fatal(".goreleaser.yaml has no archives, so this contract checks nothing")
	}
	for _, archive := range config.Archives {
		required, ok := requiredArchiveFiles[archive.ID]
		if !ok {
			t.Errorf(".goreleaser.yaml archive %q has no entry in requiredArchiveFiles; add its license and notice files there (ADR-0023)", archive.ID)
			continue
		}
		files := make([]string, 0, len(archive.Files))
		for _, file := range archive.Files {
			files = append(files, file.Src)
		}
		for _, want := range required {
			if !slices.Contains(files, want) {
				t.Errorf(".goreleaser.yaml archive %q files lack %s (ADR-0023)", archive.ID, want)
			}
		}
	}
}

func TestReleasePackagingPackagesShipLicenses(t *testing.T) {
	config := loadGoreleaserConfig(t)
	if len(config.Nfpms) == 0 {
		t.Fatal(".goreleaser.yaml has no nfpms, so this contract checks nothing")
	}
	for _, nfpm := range config.Nfpms {
		notices, ok := nfpmNoticesSource[nfpm.ID]
		if !ok {
			t.Errorf(".goreleaser.yaml nfpm %q has no entry in nfpmNoticesSource; add its third-party notices file there (ADR-0023)", nfpm.ID)
			continue
		}
		if nfpm.PackageName == "" {
			t.Errorf(".goreleaser.yaml nfpm %q has no package_name", nfpm.ID)
			continue
		}
		for _, format := range nfpm.Formats {
			want, err := licenseContents(format, nfpm.PackageName, notices)
			if err != nil {
				t.Errorf(".goreleaser.yaml nfpm %q: %v", nfpm.ID, err)
				continue
			}
			for _, entry := range want {
				matches := 0
				for _, got := range nfpm.Contents {
					if got.Src != entry.Src || got.Dst != entry.Dst || got.Packager != entry.Packager {
						continue
					}
					matches++
					if got.Type != entry.Type {
						t.Errorf(".goreleaser.yaml nfpm %q (%s) contents %s -> %s has type %q, want %q (ADR-0023)",
							nfpm.ID, format, got.Src, got.Dst, got.Type, entry.Type)
					}
				}
				switch matches {
				case 0:
					t.Errorf(".goreleaser.yaml nfpm %q (%s) lacks contents src %s dst %s packager %s%s (ADR-0023)",
						nfpm.ID, format, entry.Src, entry.Dst, entry.Packager, typeSuffix(entry.Type))
				case 1:
				default:
					t.Errorf(".goreleaser.yaml nfpm %q (%s) has %d contents entries src %s dst %s, want exactly 1",
						nfpm.ID, format, matches, entry.Src, entry.Dst)
				}
			}
			// No other entry of this format, and no entry for every format,
			// ships a license or notice source: the 3 above are the only ones.
			sources := []string{want[0].Src, want[1].Src, want[2].Src}
			for _, got := range nfpm.Contents {
				if !slices.Contains(sources, got.Src) || (got.Packager != "" && got.Packager != format) {
					continue
				}
				if !slices.Contains(want, nfpmContent{Src: got.Src, Dst: got.Dst, Type: got.Type, Packager: got.Packager}) {
					t.Errorf(".goreleaser.yaml nfpm %q (%s) has an extra contents entry src %s dst %s packager %q type %q; want only the license entries of ADR-0023",
						nfpm.ID, format, got.Src, got.Dst, got.Packager, got.Type)
				}
			}
		}
	}
}

func typeSuffix(typ string) string {
	if typ == "" {
		return ""
	}
	return " type " + typ
}

func TestReleasePackagingPackagerNamesAFormat(t *testing.T) {
	config := loadGoreleaserConfig(t)
	for _, nfpm := range config.Nfpms {
		for _, content := range nfpm.Contents {
			if content.Packager != "" && !slices.Contains(nfpm.Formats, content.Packager) {
				t.Errorf(".goreleaser.yaml nfpm %q contents %s -> %s has packager %q, which is not in its formats %v",
					nfpm.ID, content.Src, content.Dst, content.Packager, nfpm.Formats)
			}
		}
	}
}

func TestReleasePackagingSourcesExist(t *testing.T) {
	sources := map[string]bool{}
	for _, files := range requiredArchiveFiles {
		for _, file := range files {
			sources[file] = true
		}
	}
	for _, notices := range nfpmNoticesSource {
		sources[strings.TrimPrefix(notices, "./")] = true
	}
	for source := range sources {
		info, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(source)))
		switch {
		case err != nil:
			t.Errorf("license or notice file %s: %v (run make notices for a THIRD_PARTY_NOTICES file)", source, err)
		case !info.Mode().IsRegular():
			t.Errorf("license or notice file %s is not a regular file", source)
		case info.Size() == 0:
			t.Errorf("license or notice file %s is empty", source)
		}
	}
}
