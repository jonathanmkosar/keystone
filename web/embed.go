// Package web holds the built web interface and compiles it into the binary.
//
// The files in dist/ are committed, so building Keystone needs Go only.
// Until P4-01 builds the real interface, dist/ holds one placeholder page.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files returns the built interface, with index.html at its root.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is compiled in, so this cannot happen in a built binary
	}
	return sub
}
