package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/store"
)

type fakeRotater struct {
	value string
	err   error
	calls []string
}

func (f *fakeRotater) RollToken(_ context.Context, id string) (string, error) {
	f.calls = append(f.calls, id)
	return f.value, f.err
}

func seedRotate(t *testing.T) (string, keychain.Store, *fakeRotater, rotateDeps) {
	t.Helper()
	dir := t.TempDir()
	indexFile := filepath.Join(dir, "index.json")
	if err := store.WithLock(indexFile, func(i *store.Index) error {
		i.Profile(store.DefaultProfile).Set("dns-editor", store.Entry{ID: "abc123"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))
	_ = ks.Set(keychain.ServiceTokens, "dns-editor", []byte("old-value"))
	rot := &fakeRotater{value: "new-value"}
	deps := rotateDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		newClient: func(string) rotater { return rot },
		stdin:     strings.NewReader("y\n"),
	}
	return indexFile, ks, rot, deps
}

func TestRotate_HappyPath(t *testing.T) {
	_, ks, rot, deps := seedRotate(t)
	cmd := newRotateCmd(deps)
	cmd.SetArgs([]string{"dns-editor"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if len(rot.calls) != 1 || rot.calls[0] != "abc123" {
		t.Errorf("rolltoken calls = %v", rot.calls)
	}
	got, _ := ks.Get(keychain.ServiceTokens, "dns-editor")
	if string(got) != "new-value" {
		t.Errorf("keychain stored %q, want new-value", got)
	}
}

func TestRotate_YesFlagSkipsPrompt(t *testing.T) {
	_, _, rot, deps := seedRotate(t)
	deps.stdin = strings.NewReader("") // no input
	cmd := newRotateCmd(deps)
	cmd.SetArgs([]string{"-y", "dns-editor"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rotate -y: %v", err)
	}
	if len(rot.calls) != 1 {
		t.Errorf("calls = %d", len(rot.calls))
	}
}

func TestRotate_DeclineAborts(t *testing.T) {
	_, ks, rot, deps := seedRotate(t)
	deps.stdin = strings.NewReader("n\n")
	cmd := newRotateCmd(deps)
	cmd.SetArgs([]string{"dns-editor"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if len(rot.calls) != 0 {
		t.Errorf("RollToken called %d times after decline", len(rot.calls))
	}
	got, _ := ks.Get(keychain.ServiceTokens, "dns-editor")
	if string(got) != "old-value" {
		t.Errorf("keychain mutated despite decline: %q", got)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("missing abort message: %q", out.String())
	}
}

func TestRotate_UnknownToken(t *testing.T) {
	_, _, _, deps := seedRotate(t)
	cmd := newRotateCmd(deps)
	cmd.SetArgs([]string{"-y", "missing"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found in profile") {
		t.Errorf("err = %v", err)
	}
}

func TestRotate_NoBootstrap_ExitsAuth(t *testing.T) {
	dir := t.TempDir()
	indexFile := filepath.Join(dir, "index.json")
	if err := store.WithLock(indexFile, func(i *store.Index) error {
		i.Profile(store.DefaultProfile).Set("t1", store.Entry{ID: "abc"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deps := rotateDeps{
		keychain:  keychain.NewFake(),
		indexPath: func() (string, error) { return indexFile, nil },
		newClient: func(string) rotater {
			t.Fatal("client must not be built without bootstrap")
			return nil
		},
		stdin: strings.NewReader(""),
	}
	cmd := newRotateCmd(deps)
	cmd.SetArgs([]string{"-y", "t1"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitAuth {
		t.Errorf("err = %v, want ExitAuth", err)
	}
}
