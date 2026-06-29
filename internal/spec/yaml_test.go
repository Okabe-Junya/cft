package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_ValidMinimal(t *testing.T) {
	src := `name: foo
policies:
  - permissions: [DNS Write]
    zone: example.com
`
	toks, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(toks) != 1 {
		t.Fatalf("expected 1 token, got %d", len(toks))
	}
	got := toks[0]
	if got.Name != "foo" {
		t.Errorf("Name: got %q", got.Name)
	}
	if len(got.Policies) != 1 {
		t.Fatalf("expected 1 policy")
	}
	p := got.Policies[0]
	if p.Zone != "example.com" {
		t.Errorf("Zone: got %q", p.Zone)
	}
	if len(p.Permissions) != 1 || p.Permissions[0] != "DNS Write" {
		t.Errorf("Permissions: got %v", p.Permissions)
	}
}

func TestParse_MultiDocument(t *testing.T) {
	src := `name: a
policies:
  - permissions: [X]
    zone: e.com
---
name: b
policies:
  - permissions: [X]
    account: acc1
`
	toks, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(toks) != 2 {
		t.Fatalf("expected 2, got %d", len(toks))
	}
	if toks[0].Name != "a" || toks[1].Name != "b" {
		t.Errorf("names: %q, %q", toks[0].Name, toks[1].Name)
	}
}

func TestParse_RejectsUnknownField(t *testing.T) {
	src := `name: foo
policies:
  - permissions: [X]
    zone: e.com
unknown_field: bar
`
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestParse_RejectsMalformedYAML(t *testing.T) {
	src := `name: foo
policies:
  - permissions: [X
    zone: e.com
`
	_, err := Parse(strings.NewReader(src))
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestParseFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.yaml")
	src := "name: foo\npolicies:\n  - permissions: [X]\n    zone: e.com\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	toks, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(toks) != 1 || toks[0].Name != "foo" {
		t.Errorf("unexpected: %+v", toks)
	}
}

func TestLoad_Directory(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.yaml", "name: a\npolicies:\n  - permissions: [X]\n    zone: e.com\n")
	write("b.yml", "name: b\npolicies:\n  - permissions: [X]\n    zone: e.com\n")
	write("ignored.txt", "should not be loaded")

	toks, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(toks) != 2 {
		t.Fatalf("expected 2 tokens, got %d (%v)", len(toks), toks)
	}
}

func TestLoad_FileArg(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t.yaml")
	if err := os.WriteFile(p, []byte("name: foo\npolicies:\n  - permissions: [X]\n    zone: e.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	toks, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(toks) != 1 {
		t.Fatalf("expected 1, got %d", len(toks))
	}
}

func TestFormatError_NonNil(t *testing.T) {
	_, err := Parse(strings.NewReader("name: foo\npolicies: not_a_list\n"))
	if err == nil {
		t.Fatal("expected error")
	}
	s := FormatError(err, false)
	if s == "" {
		t.Error("FormatError returned empty string for non-nil error")
	}
}

func TestFormatError_Nil(t *testing.T) {
	if got := FormatError(nil, false); got != "" {
		t.Errorf("FormatError(nil) = %q, want empty", got)
	}
}
