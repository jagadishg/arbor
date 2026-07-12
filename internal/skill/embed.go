package skill

import "embed"

// Files contains the vendor-neutral Arbor authoring skill and its agent metadata.
// Embedding keeps `arbor skill install` functional in release binaries.
//
//go:embed assets
var Files embed.FS
