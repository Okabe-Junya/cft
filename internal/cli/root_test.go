package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand_Help(t *testing.T) {
	cmd := newRootCmd("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute --help: %v", err)
	}
	if !strings.Contains(out.String(), "cft") {
		t.Errorf("expected help output to mention 'cft', got:\n%s", out.String())
	}
}

func TestRootCommand_Version(t *testing.T) {
	cmd := newRootCmd("1.2.3")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute --version: %v", err)
	}
	if !strings.Contains(out.String(), "1.2.3") {
		t.Errorf("expected version output to include '1.2.3', got: %s", out.String())
	}
}

// TestRootCommand_AllSubcommandsRegistered guards against accidental
// AddCommand removals during refactors: cft --help must list every advertised
// subcommand.
func TestRootCommand_AllSubcommandsRegistered(t *testing.T) {
	cmd := newRootCmd("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute --help: %v", err)
	}
	help := out.String()
	for _, name := range []string{"apply", "delete", "exec", "list", "login", "rotate", "profile"} {
		if !strings.Contains(help, name) {
			t.Errorf("expected %q in --help output:\n%s", name, help)
		}
	}
}
