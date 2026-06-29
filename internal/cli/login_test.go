package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
)

type fakeVerifier struct {
	err error
}

func (f fakeVerifier) Verify(_ context.Context) (*cfapi.Token, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &cfapi.Token{ID: "tok", Status: "active"}, nil
}

func TestLogin_FromEnv_Success(t *testing.T) {
	ks := keychain.NewFake()
	cmd := newLoginCmdWithDeps(loginDeps{
		keychain: ks,
		stdin:    strings.NewReader(""),
		getenv: func(k string) string {
			if k == EnvCloudflareToken {
				return "env-token-value"
			}
			return ""
		},
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", errors.New("should not be called") },
		newVerifier: func(token string, _ ...cfapi.Option) tokenVerifier {
			if token != "env-token-value" {
				t.Errorf("verifier got token %q", token)
			}
			return fakeVerifier{}
		},
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--from-env"})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := ks.Get(keychain.ServiceBootstrap, keychain.BootstrapAccount)
	if err != nil {
		t.Fatalf("keychain.Get: %v", err)
	}
	if string(got) != "env-token-value" {
		t.Errorf("keychain stored %q, want env-token-value", got)
	}
}

func TestLogin_FromEnv_Empty(t *testing.T) {
	cmd := newLoginCmdWithDeps(loginDeps{
		keychain:   keychain.NewFake(),
		stdin:      strings.NewReader(""),
		getenv:     func(string) string { return "" },
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
		newVerifier: func(string, ...cfapi.Option) tokenVerifier {
			t.Fatal("verifier must not be called on empty token")
			return nil
		},
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--from-env"})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitAuth {
		t.Errorf("exit code = %v, want %d", err, ExitAuth)
	}
}

func TestLogin_Stdin_NonTTY(t *testing.T) {
	ks := keychain.NewFake()
	cmd := newLoginCmdWithDeps(loginDeps{
		keychain:   ks,
		stdin:      strings.NewReader("piped-token\n"),
		getenv:     func(string) string { return "" },
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", errors.New("should not be called") },
		newVerifier: func(token string, _ ...cfapi.Option) tokenVerifier {
			if token != "piped-token" {
				t.Errorf("verifier got token %q", token)
			}
			return fakeVerifier{}
		},
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := ks.Get(keychain.ServiceBootstrap, keychain.BootstrapAccount)
	if string(got) != "piped-token" {
		t.Errorf("keychain stored %q", got)
	}
}

func TestLogin_TTYPrompt(t *testing.T) {
	ks := keychain.NewFake()
	cmd := newLoginCmdWithDeps(loginDeps{
		keychain:   ks,
		stdin:      strings.NewReader(""),
		getenv:     func(string) string { return "" },
		isTTY:      func() bool { return true },
		readSecret: func() (string, error) { return "typed-token", nil },
		newVerifier: func(token string, _ ...cfapi.Option) tokenVerifier {
			if token != "typed-token" {
				t.Errorf("verifier got %q", token)
			}
			return fakeVerifier{}
		},
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "Cloudflare API token:") {
		t.Errorf("expected prompt; got %q", buf.String())
	}
	if got, _ := ks.Get(keychain.ServiceBootstrap, keychain.BootstrapAccount); string(got) != "typed-token" {
		t.Errorf("stored %q", got)
	}
}

func TestLogin_VerifyFails_ExitsThree_NoKeychainWrite(t *testing.T) {
	ks := keychain.NewFake()
	cmd := newLoginCmdWithDeps(loginDeps{
		keychain:   ks,
		stdin:      strings.NewReader("bad\n"),
		getenv:     func(string) string { return "" },
		isTTY:      func() bool { return false },
		readSecret: func() (string, error) { return "", nil },
		newVerifier: func(string, ...cfapi.Option) tokenVerifier {
			return fakeVerifier{err: &cfapi.Error{Status: 401, Message: "no"}}
		},
	})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitAuth {
		t.Errorf("exit code = %v, want %d", err, ExitAuth)
	}
	if _, kerr := ks.Get(keychain.ServiceBootstrap, keychain.BootstrapAccount); !errors.Is(kerr, keychain.ErrNotFound) {
		t.Errorf("keychain should not have been written; got err=%v", kerr)
	}
}

func TestLogin_TrimsWhitespace(t *testing.T) {
	ks := keychain.NewFake()
	cmd := newLoginCmdWithDeps(loginDeps{
		keychain:    ks,
		stdin:       strings.NewReader("   trimmed   \n"),
		getenv:      func(string) string { return "" },
		isTTY:       func() bool { return false },
		readSecret:  func() (string, error) { return "", nil },
		newVerifier: func(string, ...cfapi.Option) tokenVerifier { return fakeVerifier{} },
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := ks.Get(keychain.ServiceBootstrap, keychain.BootstrapAccount)
	if string(got) != "trimmed" {
		t.Errorf("stored %q, want %q", got, "trimmed")
	}
}
