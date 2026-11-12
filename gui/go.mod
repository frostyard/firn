module github.com/frostyard/firn/gui

// The graphical installer is a nested module (ADR-0021): mygo needs Go
// 1.27.3 or later, while the root module keeps its own toolchain pin. The
// replace makes firn's internal packages importable from this tree.
go 1.27.4

require (
	github.com/egoist/mygo v0.9.0
	github.com/frostyard/firn v0.0.0-00010101000000-000000000000
	github.com/frostyard/frostui v0.5.1
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/ebitengine/purego v0.11.3 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/frostyard/firn => ../
