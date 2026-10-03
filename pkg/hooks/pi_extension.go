package hooks

import (
	_ "embed"
	"encoding/json"
	"strings"

	"tzro/pkg/executor"
)

//go:embed pi_extension.ts
var piExtension string

func renderPiExtension(binary string) string {
	path, _ := json.Marshal(binary)
	description, _ := json.Marshal(executor.GraphToolDescription)
	return strings.NewReplacer(
		"__TZRO_BINARY__", string(path),
		"__TZRO_GRAPH_DESCRIPTION__", string(description),
		"__TZRO_GRAPH_SCHEMA__", executor.GraphToolSchemaJSON,
	).Replace(piExtension)
}
