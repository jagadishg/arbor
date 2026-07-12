package schema

import (
	"embed"
	"fmt"
)

// Files embeds the public resource schemas so installed Arbor binaries can
// provide schema information without requiring an Arbor source checkout.
//
//go:embed assets/*.json
var Files embed.FS

func JSON(kind string) ([]byte, error) {
	if kind != "workspace" && kind != "collection" && kind != "request" && kind != "environment" && kind != "scenario" {
		return nil, fmt.Errorf("unsupported schema kind %q", kind)
	}
	return Files.ReadFile("assets/" + kind + ".schema.json")
}
