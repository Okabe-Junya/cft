package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaPath = "../../schema/cft-token.schema.json"

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	sch, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		t.Fatalf("compile %s: %v", schemaPath, err)
	}
	return sch
}

// validateWithSchema checks every YAML document in src against the published
// JSON Schema, normalizing through JSON the way yaml-language-server does.
func validateWithSchema(t *testing.T, sch *jsonschema.Schema, src string) error {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(src))
	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			t.Fatalf("decode fixture: %v", err)
		}
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("unmarshal fixture: %v", err)
		}
		if err := sch.Validate(v); err != nil {
			return err
		}
	}
}

func validateWithGo(src string) error {
	tokens, err := Parse(strings.NewReader(src))
	if err != nil {
		return err
	}
	return ValidateAll(tokens)
}

// TestSchema_MatchesGoValidation ensures the JSON Schema and the Go parser
// agree on each fixture, so editor diagnostics never diverge from `cft apply`.
// Cross-document rules (duplicate names) are out of the schema's reach and not
// covered here.
func TestSchema_MatchesGoValidation(t *testing.T) {
	sch := compileSchema(t)

	cases := []struct {
		name  string
		src   string
		valid bool
	}{
		{
			name: "minimal zone scope",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
`,
			valid: true,
		},
		{
			name: "account scope",
			src: `name: foo
policies:
  - permissions: [Workers Scripts Write]
    account: my-account
`,
			valid: true,
		},
		{
			name: "user scope with deny effect",
			src: `name: foo
policies:
  - permissions: [Memberships Read]
    user: true
    effect: deny
`,
			valid: true,
		},
		{
			name: "expires date",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
expires: "2026-09-01"
`,
			valid: true,
		},
		{
			name: "expires rfc3339",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
expires: "2026-09-01T12:30:00+09:00"
`,
			valid: true,
		},
		{
			name: "multiple policies and documents",
			src: `name: a
policies:
  - permissions: [DNS Write, DNS Read]
    zone: example.com
  - permissions: [Workers Scripts Write]
    account: acc
---
name: b
policies:
  - permissions: [Memberships Read]
    user: true
`,
			valid: true,
		},
		{
			name: "missing name",
			src: `policies:
  - permissions: [DNS Write]
    zone: example.com
`,
			valid: false,
		},
		{
			name: "name not dns-1123",
			src: `name: Foo_Bar
policies:
  - permissions: [DNS Write]
    zone: example.com
`,
			valid: false,
		},
		{
			name: "name too long",
			src: "name: " + strings.Repeat("a", 64) + `
policies:
  - permissions: [DNS Write]
    zone: example.com
`,
			valid: false,
		},
		{
			name:  "no policies",
			src:   "name: foo\npolicies: []\n",
			valid: false,
		},
		{
			name: "empty permissions",
			src: `name: foo
policies:
  - permissions: []
    zone: example.com
`,
			valid: false,
		},
		{
			name: "empty permission string",
			src: `name: foo
policies:
  - permissions: [""]
    zone: example.com
`,
			valid: false,
		},
		{
			name: "no scope",
			src: `name: foo
policies:
  - permissions: [DNS Write]
`,
			valid: false,
		},
		{
			name: "two scopes",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
    account: acc
`,
			valid: false,
		},
		{
			name: "user false is not a scope",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    user: false
`,
			valid: false,
		},
		{
			name: "bad effect",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
    effect: block
`,
			valid: false,
		},
		{
			name: "bad expires",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
expires: someday
`,
			valid: false,
		},
		{
			name: "unknown token field",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
ttl: 30d
`,
			valid: false,
		},
		{
			name: "unknown policy field",
			src: `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
    scope: zone
`,
			valid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goErr := validateWithGo(tc.src)
			schemaErr := validateWithSchema(t, sch, tc.src)
			if (goErr == nil) != tc.valid {
				t.Errorf("Go validation: got err=%v, want valid=%v", goErr, tc.valid)
			}
			if (schemaErr == nil) != tc.valid {
				t.Errorf("schema validation: got err=%v, want valid=%v", schemaErr, tc.valid)
			}
		})
	}
}
