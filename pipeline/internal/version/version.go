// Package version holds build-time metadata for the pipeline binary.
// Values are injected via -ldflags at build time; zero values fall through
// to sensible defaults inside clix.App.
package version

// Version, Commit, Date, and BuiltBy are set by goreleaser / the Justfile
// build target via -X ldflags.  When built locally without those flags every
// field will be the empty string and clix.App.defaults() will substitute
// "dev", "none", "unknown", and "local" respectively.
var (
	Version = ""
	Commit  = ""
	Date    = ""
	BuiltBy = ""
)
