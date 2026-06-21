// Package asset embeds the relay's static web assets (the dashboard, capture and
// login pages, and client scripts) into the binary, so the signalling server is
// self-contained and doesn't need the asset directory shipped alongside it.
package asset

import "embed"

// FS holds every page and script in this directory. Paths are relative to the
// directory (e.g. "index.html"), with no leading "asset/".
//
//go:embed *.html *.js
var FS embed.FS
