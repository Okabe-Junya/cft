// Package schema embeds the published JSON Schema for token spec files so
// the binary can reproduce it (`cft schema`) regardless of install method.
// internal/spec/schema_test.go keeps it in sync with the Go validator.
package schema

import _ "embed"

// TokenSpec is the JSON Schema for one document of a cft token spec file.
//
//go:embed cft-token.schema.json
var TokenSpec []byte
