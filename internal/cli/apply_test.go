package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/spec"
	"github.com/Okabe-Junya/cft/internal/store"
)

// fakeClient implements cfClient. Each method records its calls so tests
// can assert that idempotency and ordering hold.
type fakeClient struct {
	permissionGroups []cfapi.PermissionGroup
	zones            map[string]string

	createResp map[string]*cfapi.CreatedToken
	updateResp map[string]*cfapi.Token

	// createErr forces CreateToken to return the given error for matching
	// spec.Name values without recording a successful response.
	createErr map[string]error

	createCalls []cfapi.TokenSpec
	updateCalls []struct {
		ID   string
		Spec cfapi.TokenSpec
	}
}

func (f *fakeClient) ListPermissionGroups(_ context.Context) ([]cfapi.PermissionGroup, error) {
	return f.permissionGroups, nil
}
func (f *fakeClient) ResolveZoneID(_ context.Context, name string) (string, error) {
	id, ok := f.zones[name]
	if !ok {
		return "", errors.New("zone not found: " + name)
	}
	return id, nil
}
func (f *fakeClient) CreateToken(_ context.Context, s cfapi.TokenSpec) (*cfapi.CreatedToken, error) {
	f.createCalls = append(f.createCalls, s)
	if e, ok := f.createErr[s.Name]; ok {
		return nil, e
	}
	if r, ok := f.createResp[s.Name]; ok {
		return r, nil
	}
	return &cfapi.CreatedToken{
		Token: cfapi.Token{ID: "id-" + s.Name, Name: s.Name},
		Value: "value-of-" + s.Name,
	}, nil
}
func (f *fakeClient) UpdateToken(_ context.Context, id string, s cfapi.TokenSpec) (*cfapi.Token, error) {
	f.updateCalls = append(f.updateCalls, struct {
		ID   string
		Spec cfapi.TokenSpec
	}{ID: id, Spec: s})
	if r, ok := f.updateResp[id]; ok {
		return r, nil
	}
	return &cfapi.Token{ID: id, Name: s.Name}, nil
}

func writeSpec(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApply_CreateNewToken(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: dns-editor-example-com
policies:
  - permissions: [DNS Write]
    zone: example.com
expires: 2027-01-01
`)

	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bootstrap"))

	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "pg-dns", Name: "DNS Write"}},
		zones:            map[string]string{"example.com": "zone-1"},
	}

	indexFile := filepath.Join(dir, "index.json")
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(client.createCalls) != 1 {
		t.Fatalf("create calls = %d", len(client.createCalls))
	}
	got := client.createCalls[0]
	if got.Name != "dns-editor-example-com" {
		t.Errorf("create name = %q", got.Name)
	}
	if got.ExpiresOn != "2027-01-01T00:00:00Z" {
		t.Errorf("ExpiresOn = %q", got.ExpiresOn)
	}

	val, err := ks.Get(keychain.ServiceTokens, "dns-editor-example-com")
	if err != nil {
		t.Fatalf("keychain: %v", err)
	}
	if string(val) != "value-of-dns-editor-example-com" {
		t.Errorf("keychain stored %q", val)
	}

	idx, err := store.Load(indexFile)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := idx.Profile(store.DefaultProfile).Get("dns-editor-example-com")
	if !ok || e.ID != "id-dns-editor-example-com" || e.Expires != "2027-01-01" {
		t.Errorf("index entry = %+v ok=%v", e, ok)
	}
}

func TestApply_IsIdempotent_SecondRunUpdatesNotCreates(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: t1
policies:
  - permissions: [DNS Write]
    zone: example.com
`)
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))

	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "pg-dns", Name: "DNS Write"}},
		zones:            map[string]string{"example.com": "zone-1"},
	}
	indexFile := filepath.Join(dir, "index.json")
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
	}
	for i := 0; i < 2; i++ {
		cmd := newApplyCmd(deps)
		cmd.SetArgs([]string{specPath})
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetContext(context.Background())
		if err := cmd.Execute(); err != nil {
			t.Fatalf("apply #%d: %v", i, err)
		}
	}
	if len(client.createCalls) != 1 {
		t.Errorf("create calls = %d, want 1", len(client.createCalls))
	}
	if len(client.updateCalls) != 1 {
		t.Errorf("update calls = %d, want 1", len(client.updateCalls))
	}
}

func TestApply_DryRun_NoApiCalls(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: t1
policies:
  - permissions: [DNS Write]
    zone: example.com
`)
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))

	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "pg-dns", Name: "DNS Write"}},
		zones:            map[string]string{"example.com": "zone-1"},
	}
	indexFile := filepath.Join(dir, "index.json")
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{"--dry-run", specPath})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run apply: %v", err)
	}
	if len(client.createCalls) != 0 || len(client.updateCalls) != 0 {
		t.Errorf("dry run hit API: create=%d update=%d", len(client.createCalls), len(client.updateCalls))
	}
	if _, err := ks.Get(keychain.ServiceTokens, "t1"); !errors.Is(err, keychain.ErrNotFound) {
		t.Error("dry run wrote to keychain")
	}
	if !strings.Contains(buf.String(), "create") {
		t.Errorf("plan missing 'create': %q", buf.String())
	}
}

func TestApply_NoBootstrap_ExitsAuth(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: t1
policies:
  - permissions: [X]
    zone: e.com
`)
	deps := applyDeps{
		keychain:  keychain.NewFake(),
		indexPath: func() (string, error) { return filepath.Join(dir, "index.json"), nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient {
			t.Fatal("client must not be built without bootstrap")
			return nil
		},
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitAuth {
		t.Errorf("err = %v, want ExitAuth", err)
	}
}

func TestApply_SpecValidationFailsExitsTwo(t *testing.T) {
	dir := t.TempDir()
	// Two tokens with the same name; ValidateAll rejects.
	specPath := writeSpec(t, dir, "t.yaml",
		`name: dup
policies:
  - permissions: [X]
    zone: e.com
---
name: dup
policies:
  - permissions: [X]
    zone: f.com
`)
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return filepath.Join(dir, "index.json"), nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { t.Fatal("must not build client"); return nil },
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitSpec {
		t.Errorf("err = %v, want ExitSpec", err)
	}
}

func TestApply_MissingKeychainValueOnUpdate_Warns(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: known
policies:
  - permissions: [X]
    zone: e.com
`)
	// Pre-seed index so the action will be update, then leave keychain empty
	// for the per-token value.
	indexFile := filepath.Join(dir, "index.json")
	if err := store.WithLock(indexFile, func(i *store.Index) error {
		i.Profile(store.DefaultProfile).Set("known", store.Entry{ID: "pre-existing"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))

	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "p", Name: "X"}},
		zones:            map[string]string{"e.com": "z"},
	}

	var warnings []string
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
		stderrWarner: func(format string, a ...any) {
			warnings = append(warnings, format)
		},
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(client.updateCalls) != 1 {
		t.Errorf("update calls = %d", len(client.updateCalls))
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "missing from Keychain") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestApply_PartialFailure_PersistsIndexForSucceeded(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: a
policies: [{permissions: [X], zone: e.com}]
---
name: b
policies: [{permissions: [X], zone: e.com}]
---
name: c
policies: [{permissions: [X], zone: e.com}]
`)

	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))

	wantErr := errors.New("simulated 403 from Cloudflare")
	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "p", Name: "X"}},
		zones:            map[string]string{"e.com": "z"},
		createErr:        map[string]error{"c": wantErr},
	}

	indexFile := filepath.Join(dir, "index.json")
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from apply")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err does not wrap simulated CF error: %v", err)
	}

	// Index must contain entries for a and b but not c.
	idx, lerr := store.Load(indexFile)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if _, ok := idx.Profile(store.DefaultProfile).Get("a"); !ok {
		t.Error("index missing 'a' after partial failure")
	}
	if _, ok := idx.Profile(store.DefaultProfile).Get("b"); !ok {
		t.Error("index missing 'b' after partial failure")
	}
	if _, ok := idx.Profile(store.DefaultProfile).Get("c"); ok {
		t.Error("index unexpectedly contains 'c' (failed action)")
	}

	// Keychain must have the values for a and b.
	for _, n := range []string{"a", "b"} {
		if _, kerr := ks.Get(keychain.ServiceTokens, n); kerr != nil {
			t.Errorf("keychain missing value for %q: %v", n, kerr)
		}
	}
	if _, kerr := ks.Get(keychain.ServiceTokens, "c"); !errors.Is(kerr, keychain.ErrNotFound) {
		t.Errorf("keychain has value for failed token c: err=%v", kerr)
	}

	// User-visible summary must report partial progress.
	if !strings.Contains(out.String(), "applied: 2 of 3") {
		t.Errorf("summary missing 'applied: 2 of 3': %q", out.String())
	}
}

func TestApply_FirstActionFails_NothingPersisted(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: a
policies: [{permissions: [X], zone: e.com}]
---
name: b
policies: [{permissions: [X], zone: e.com}]
`)
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))

	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "p", Name: "X"}},
		zones:            map[string]string{"e.com": "z"},
		createErr:        map[string]error{"a": errors.New("boom")},
	}
	indexFile := filepath.Join(dir, "index.json")
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return indexFile, nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error")
	}

	idx, err := store.Load(indexFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.Profile(store.DefaultProfile).All(); len(got) != 0 {
		t.Errorf("index should be empty on first-action failure, got %v", got)
	}
	if !strings.Contains(out.String(), "applied: 0 of 2") {
		t.Errorf("summary missing 'applied: 0 of 2': %q", out.String())
	}
}

func TestApply_UnknownPermissionGroup_ExitsSpec(t *testing.T) {
	dir := t.TempDir()
	specPath := writeSpec(t, dir, "t.yaml",
		`name: t1
policies:
  - permissions: [Typo]
    zone: e.com
`)
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceBootstrap, keychain.BootstrapAccount, []byte("bs"))
	client := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{{ID: "p1", Name: "Real"}},
		zones:            map[string]string{"e.com": "z"},
	}
	deps := applyDeps{
		keychain:  ks,
		indexPath: func() (string, error) { return filepath.Join(dir, "index.json"), nil },
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(string) cfClient { return client },
	}
	cmd := newApplyCmd(deps)
	cmd.SetArgs([]string{specPath})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitSpec {
		t.Errorf("err = %v, want ExitSpec", err)
	}
}
