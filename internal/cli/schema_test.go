package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Okabe-Junya/cft/schema"
)

func TestSchemaCmd_PrintsEmbeddedSchema(t *testing.T) {
	cmd := newSchemaCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !bytes.Equal(out.Bytes(), schema.TokenSpec) {
		t.Error("output differs from embedded schema")
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc["$schema"] == "" {
		t.Error("missing $schema")
	}
}
