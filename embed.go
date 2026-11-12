// Package shuttle holds the files built into the binary: the default web
// page and the changelog shown in About.
package shuttle

import _ "embed"

// IndexHTML is the built-in web page. A page_dir in the host config overrides
// it with a plain file, so a page-only change needs no rebuild.
//
//go:embed web/index.html
var IndexHTML []byte

// Changelog is shown in the page's About panel.
//
//go:embed CHANGELOG.md
var Changelog string
