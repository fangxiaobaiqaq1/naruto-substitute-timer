// Package assets contains the default resources required by a standalone executable.
package assets

import "embed"

// Templates includes both images and masks referenced by the default manifest.
//
//go:embed templates/*.png templates/manifest.json
var Templates embed.FS

//go:embed game/substitutes.json
var SubstituteCatalog []byte

// ASAvatars is the official-index-verified A/S base roster and the skins linked
// to those bases. It is intentionally a bounded subset, not the 529-image
// external corpus. Runtime recognition owns its own internal embed so it can
// retain prepared matching metadata without exporting implementation details.
//
//go:embed avatars/*.png avatars/index.json
var ASAvatars embed.FS

//go:embed avatars/index.json
var ASAvatarIndex []byte
