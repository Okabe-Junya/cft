package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/store"
)

type fakeDeleter struct {
	err   error
	calls []string
}

func (f *fakeDeleter) DeleteToken(_ context.Context, id string) error {
	f.calls = append(f.calls, id)
	return f.err
}

func seedDelete(t *testing.T, stdin string) (string, keychain.Store, *fakeDeleter, deleteDeps) {
	t.Helper()
	dir := t.TempDir()
	indexFile := filepath.Join(dir, "index.json")
	if err := store.WithLock(indexFile, func(i *store.Index) error {
		i.Profile(store.DefaultProfile).Set("dns-editor", store.Entry{ID: "abc123", Expires: "2027-01-01"})
		i.Profile(store.DefaultProfile).Set("other", store.Entry{ID: "xyz"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))
	_ = ks.Set(keychain.ServiceTokens, "dns-editor", []byte("the-value"))
	d := &fakeDeleter{}
	deps := deleteDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		withLock:  store.WithLock,
		newClient: func(string) deleter { return d },
		stdin:     strings.NewReader(stdin),
	}
	return indexFile, ks, d, deps
}

func TestDelete_HappyPath_OrderAndCleanup(t *testing.T) {
	indexFile, ks, d, deps := seedDelete(t, "y\n")
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"dns-editor"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Cloudflare DELETE called with the correct id.
	if len(d.calls) != 1 || d.calls[0] != "abc123" {
		t.Errorf("DeleteToken calls = %v", d.calls)
	}
	// Keychain entry gone.
	if _, err := ks.Get(keychain.ServiceTokens, "dns-editor"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("keychain entry still present: err=%v", err)
	}
	// Index entry gone, other untouched.
	idx, err := store.Load(indexFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Profile(store.DefaultProfile).Get("dns-editor"); ok {
		t.Error("index still contains dns-editor")
	}
	if _, ok := idx.Profile(store.DefaultProfile).Get("other"); !ok {
		t.Error("unrelated index entry was removed")
	}
}

func TestDelete_YesFlagSkipsPrompt(t *testing.T) {
	_, _, d, deps := seedDelete(t, "")
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"-y", "dns-editor"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("delete -y: %v", err)
	}
	if len(d.calls) != 1 {
		t.Errorf("DeleteToken calls = %d", len(d.calls))
	}
}

func TestDelete_DeclineAborts(t *testing.T) {
	indexFile, ks, d, deps := seedDelete(t, "n\n")
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"dns-editor"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(d.calls) != 0 {
		t.Errorf("DeleteToken called after decline")
	}
	idx, _ := store.Load(indexFile)
	if _, ok := idx.Profile(store.DefaultProfile).Get("dns-editor"); !ok {
		t.Error("index entry removed after decline")
	}
	if _, err := ks.Get(keychain.ServiceTokens, "dns-editor"); err != nil {
		t.Error("keychain entry removed after decline")
	}
}

func TestDelete_404FromCloudflare_ContinuesCleanup(t *testing.T) {
	indexFile, ks, d, deps := seedDelete(t, "y\n")
	d.err = &cfapi.Error{Status: 404, Codes: []int{1001}, Messages: []string{"not found"}}
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"dns-editor"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := ks.Get(keychain.ServiceTokens, "dns-editor"); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("keychain not cleaned up after 404: %v", err)
	}
	idx, _ := store.Load(indexFile)
	if _, ok := idx.Profile(store.DefaultProfile).Get("dns-editor"); ok {
		t.Errorf("index not cleaned up after 404")
	}
	if !strings.Contains(out.String(), "404") {
		t.Errorf("expected 404 warning in output: %q", out.String())
	}
}

func TestDelete_5xxFromCloudflare_StopsAndPreservesState(t *testing.T) {
	indexFile, ks, d, deps := seedDelete(t, "y\n")
	d.err = &cfapi.Error{Status: 503, Messages: []string{"service unavailable"}}
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"dns-editor"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
	// Keychain and index must be untouched.
	if _, err := ks.Get(keychain.ServiceTokens, "dns-editor"); err != nil {
		t.Errorf("keychain mutated on 5xx: %v", err)
	}
	idx, _ := store.Load(indexFile)
	if _, ok := idx.Profile(store.DefaultProfile).Get("dns-editor"); !ok {
		t.Errorf("index mutated on 5xx")
	}
}

func TestDelete_UnknownToken(t *testing.T) {
	_, _, _, deps := seedDelete(t, "")
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"-y", "missing"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found in profile") {
		t.Errorf("err = %v", err)
	}
}

func TestDelete_NoBootstrap_ExitsAuth(t *testing.T) {
	dir := t.TempDir()
	indexFile := filepath.Join(dir, "index.json")
	if err := store.WithLock(indexFile, func(i *store.Index) error {
		i.Profile(store.DefaultProfile).Set("x", store.Entry{ID: "id"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deps := deleteDeps{
		keychain:  keychain.NewFake(),
		indexPath: func() (string, error) { return indexFile, nil },
		withLock:  store.WithLock,
		newClient: func(string) deleter { t.Fatal("must not build client"); return nil },
		stdin:     strings.NewReader(""),
	}
	cmd := newDeleteCmd(deps)
	cmd.SetArgs([]string{"-y", "x"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitAuth {
		t.Errorf("err = %v, want ExitAuth", err)
	}
}
