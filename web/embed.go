// Package web embeds the dashboard's static assets and HTML templates
// directly into the binary so that no runtime files are required on disk.
package web

import "embed"

//go:embed all:templates all:static
var FS embed.FS
