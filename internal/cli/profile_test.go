package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

func TestResolveProfile_Precedence(t *testing.T) {
	idx := store.NewIndex()
	idx.Current = "curr"
	withEnv := func(k string) string {
		if k == EnvProfile {
			return "envp"
		}
		return ""
	}
	noEnv := func(string) string { return "" }

	if got := resolveProfile("flagp", withEnv, idx); got != "flagp" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := resolveProfile("", withEnv, idx); got != "envp" {
		t.Errorf("env should win over current, got %q", got)
	}
	if got := resolveProfile("", noEnv, idx); got != "curr" {
		t.Errorf("current should win over default, got %q", got)
	}
	if got := resolveProfile("", noEnv, store.NewIndex()); got != store.DefaultProfile {
		t.Errorf("empty index → default, got %q", got)
	}
	if got := resolveProfile("", noEnv, nil); got != store.DefaultProfile {
		t.Errorf("nil index skips current → default, got %q", got)
	}
}

func TestProfileFromCmd_ReadsInheritedPersistentFlag(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("profile", "", "")
	var seen string
	child := &cobra.Command{
		Use:  "child",
		RunE: func(c *cobra.Command, _ []string) error { seen = profileFromCmd(c); return nil },
	}
	root.AddCommand(child)
	root.SetArgs([]string{"child", "--profile", "gmail"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if seen != "gmail" {
		t.Errorf("profileFromCmd = %q, want gmail", seen)
	}
}

// profileTestDeps builds profileDeps backed by a temp index and a shared fake
// Keychain. fromEnvToken (when non-empty) is returned for CLOUDFLARE_API_TOKEN
// so `profile add --from-env` works without a TTY.
func profileTestDeps(t *testing.T, fromEnvToken string) (string, *keychain.Fake, profileDeps) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.json")
	ks := keychain.NewFake()
	deps := profileDeps{
		login: loginDeps{
			keychain: ks,
			stdin:    strings.NewReader(""),
			getenv: func(k string) string {
				if k == EnvCloudflareToken {
					return fromEnvToken
				}
				return ""
			},
			isTTY:       func() bool { return false },
			readSecret:  func() (string, error) { return "", nil },
			newVerifier: func(string, ...cfapi.Option) tokenVerifier { return fakeVerifier{} },
		},
		keychain:  ks,
		indexPath: func() (string, error) { return path, nil },
		load:      func() (*store.Index, error) { return store.Load(path) },
		withLock:  store.WithLock,
	}
	return path, ks, deps
}

func TestProfileAdd_StoresBootstrapAndSetsCurrentWhenEmpty(t *testing.T) {
	path, ks, deps := profileTestDeps(t, "tokA")
	cmd := newProfileAddCmd(deps)
	cmd.SetArgs([]string{"--from-env", "alumni"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got, _ := ks.Get(keychain.ServiceBootstrap, "alumni"); string(got) != "tokA" {
		t.Errorf("bootstrap for alumni = %q, want tokA", got)
	}
	idx, _ := store.Load(path)
	if idx.Current != "alumni" {
		t.Errorf("current = %q, want alumni", idx.Current)
	}
	if !idx.HasProfile("alumni") {
		t.Error("alumni profile not materialised in index")
	}
}

func TestProfileAdd_DoesNotSwitchCurrentWhenAlreadySet(t *testing.T) {
	path, _, deps := profileTestDeps(t, "tokB")
	// Pre-seed a current profile.
	if err := store.WithLock(path, func(i *store.Index) error {
		i.Current = "gmail"
		i.Profile("gmail")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cmd := newProfileAddCmd(deps)
	cmd.SetArgs([]string{"--from-env", "alumni"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetContext(context.Background())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add: %v", err)
	}
	idx, _ := store.Load(path)
	if idx.Current != "gmail" {
		t.Errorf("current = %q, want unchanged gmail", idx.Current)
	}
	if !idx.HasProfile("alumni") {
		t.Error("alumni profile not added")
	}
}

func TestProfileUse_SetsCurrent(t *testing.T) {
	path, ks, deps := profileTestDeps(t, "")
	_ = ks.Set(keychain.ServiceBootstrap, "gmail", []byte("bs"))
	cmd := newProfileUseCmd(deps)
	cmd.SetArgs([]string{"gmail"})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("use: %v", err)
	}
	idx, _ := store.Load(path)
	if idx.Current != "gmail" {
		t.Errorf("current = %q, want gmail", idx.Current)
	}
}

func TestProfileUse_WarnsWhenNoBootstrap(t *testing.T) {
	_, _, deps := profileTestDeps(t, "")
	cmd := newProfileUseCmd(deps)
	cmd.SetArgs([]string{"typo-profile"})
	var errOut bytes.Buffer
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("use: %v", err)
	}
	if !strings.Contains(errOut.String(), "no bootstrap token") {
		t.Errorf("expected warning about missing bootstrap, got %q", errOut.String())
	}
}

func TestProfileCurrent_PrintsCurrentOrDefault(t *testing.T) {
	path, _, deps := profileTestDeps(t, "")

	// No current set → default note.
	cmd := newProfileCurrentCmd(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), store.DefaultProfile) {
		t.Errorf("expected default note, got %q", out.String())
	}

	// With current set → prints it.
	if err := store.WithLock(path, func(i *store.Index) error { i.Current = "gmail"; return nil }); err != nil {
		t.Fatal(err)
	}
	cmd = newProfileCurrentCmd(deps)
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "gmail" {
		t.Errorf("current = %q, want gmail", out.String())
	}
}

func TestProfileList_ShowsProfilesAndCurrentMarker(t *testing.T) {
	path, ks, deps := profileTestDeps(t, "")
	_ = ks.Set(keychain.ServiceBootstrap, "gmail", []byte("bs"))
	if err := store.WithLock(path, func(i *store.Index) error {
		i.Current = "gmail"
		i.Profile("gmail").Set("tok", store.Entry{ID: "1"})
		i.Profile("alumni") // exists in index, no bootstrap yet
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cmd := newProfileListCmd(deps)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list: %v", err)
	}
	got := out.String()
	for _, want := range []string{"gmail", "alumni", "CURRENT", "BOOTSTRAP"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
	// The current marker '*' must sit on the gmail row.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "gmail") && !strings.Contains(line, "*") {
			t.Errorf("gmail row missing current marker: %q", line)
		}
		if strings.Contains(line, "alumni") && strings.Contains(line, "*") {
			t.Errorf("alumni row should not be marked current: %q", line)
		}
	}
}

// TestMultiProfile_ExecPicksProfileToken exercises end-to-end isolation: two
// profiles hold a token of the same name with different Keychain values, and
// $CFT_PROFILE selects which one exec injects.
func TestMultiProfile_ExecPicksProfileToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	if err := store.WithLock(path, func(i *store.Index) error {
		i.Profile("a").Set("shared", store.Entry{ID: "ida"})
		i.Profile("b").Set("shared", store.Entry{ID: "idb"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceTokens, keychain.TokenAccount("a", "shared"), []byte("value-a"))
	_ = ks.Set(keychain.ServiceTokens, keychain.TokenAccount("b", "shared"), []byte("value-b"))

	t.Setenv(EnvProfile, "b")
	var got capturedExec
	cmd := newExecCmd(
		func() (*store.Index, error) { return store.Load(path) },
		ks,
		func(argv, env []string) error { got = capturedExec{argv: argv, env: env}; return nil },
	)
	cmd.SetArgs([]string{"shared", "--", "run"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !containsString(got.env, EnvName+"=value-b") {
		t.Errorf("expected profile b's value injected; env=%v", got.env)
	}
	if containsString(got.env, EnvName+"=value-a") {
		t.Error("profile a's value leaked into env")
	}
}
