// Package assets contains the default resources required by a standalone executable.
package assets

import "embed"

// Templates includes both images and masks referenced by the default manifest.
//
//go:embed templates/*.png templates/manifest.json
var Templates embed.FS
