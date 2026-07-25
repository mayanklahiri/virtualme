package config

import _ "embed"

//go:embed schema.json
var embeddedSchema []byte

func EmbeddedSchemaBytes() []byte {
	return append([]byte(nil), embeddedSchema...)
}
