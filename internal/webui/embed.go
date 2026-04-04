// Package webui embeds the compiled React frontend (web/dist) into the binary.
// Keeping the embed directive here (close to web/dist) avoids the Go restriction
// that prohibits ".." in embed paths.
package webui

import "embed"

// FS holds the embedded web/dist directory tree.
//
//go:embed dist
var FS embed.FS
