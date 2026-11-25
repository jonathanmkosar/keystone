// Package fixture compiles the test tenant's fixtures into the binary, so
// the server and the scripts find them without the repo on disk.
//
// Everything in this folder is invented and every file says so. The loader
// that reads it is internal/testtenant.
package fixture

import "embed"

// FS holds tenant.json and the chats, turns and cases folders.
//
//go:embed tenant.json chats/*.json turns/*.json cases/*.json
var FS embed.FS
