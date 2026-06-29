package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/store"
)

func TestBuildEnv_ReplacesExisting(t *testing.T) {
	base := []string{"HOME=/u/me", EnvName + "=old", "PATH=/bin"}
	got := buildEnv(base, "new")
	want := []string{"HOME=/u/me", EnvName + "=new", "PATH=/bin"}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// base must not be mutated.
	if base[1] != EnvName+"=old" {
		t.Errorf("base mutated: %v", base)
	}
}

func TestBuildEnv_AppendsWhenAbsent(t *testing.T) {
	base := []string{"HOME=/u/me"}
	got := buildEnv(base, "v")
	want := []string{"HOME=/u/me", EnvName + "=v"}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitExecArgs(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		dashIdx   int
		wantName  string
		wantArgv  []string
		wantErrSS string
	}{
		{
			name:     "happy path",
			args:     []string{"my-token", "terraform", "plan"},
			dashIdx:  1,
			wantName: "my-token",
			wantArgv: []string{"terraform", "plan"},
		},
		{
			name:      "no dash",
			args:      []string{"foo", "bar"},
			dashIdx:   -1,
			wantErrSS: "missing '--' separator",
		},
		{
			name:      "two args before dash",
			args:      []string{"a", "b", "cmd"},
			dashIdx:   2,
			wantErrSS: "expected one argument before",
		},
		{
			name:      "no command after dash",
			args:      []string{"name"},
			dashIdx:   1,
			wantErrSS: "missing command after",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, argv, err := splitExecArgs(c.args, c.dashIdx)
			if c.wantErrSS != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSS) {
					t.Errorf("err = %v, want substring %q", err, c.wantErrSS)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if name != c.wantName || !equalStrings(argv, c.wantArgv) {
				t.Errorf("got (%q, %v), want (%q, %v)", name, argv, c.wantName, c.wantArgv)
			}
		})
	}
}

type capturedExec struct {
	argv []string
	env  []string
}

func TestExec_HappyPath(t *testing.T) {
	idx := store.NewIndex()
	idx.Profile(store.DefaultProfile).Set("dns-editor", store.Entry{ID: "id1"})

	ks := keychain.NewFake()
	_ = ks.Set(keychain.ServiceTokens, "dns-editor", []byte("the-value"))

	var got capturedExec
	cmd := newExecCmd(
		func() (*store.Index, error) { return idx, nil },
		ks,
		func(argv, env []string) error {
			got = capturedExec{argv: argv, env: env}
			return nil
		},
	)
	cmd.SetArgs([]string{"dns-editor", "--", "terraform", "plan"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !equalStrings(got.argv, []string{"terraform", "plan"}) {
		t.Errorf("argv = %v", got.argv)
	}
	if !containsString(got.env, EnvName+"=the-value") {
		t.Errorf("env missing %s=the-value; got %v", EnvName, got.env)
	}
}

func TestExec_UnknownToken(t *testing.T) {
	cmd := newExecCmd(
		func() (*store.Index, error) { return store.NewIndex(), nil },
		keychain.NewFake(),
		func(_, _ []string) error { t.Fatal("execer must not run"); return nil },
	)
	cmd.SetArgs([]string{"missing", "--", "cmd"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "not found in profile") {
		t.Errorf("err = %v", err)
	}
}

func TestExec_MissingKeychainValueHintsRotate(t *testing.T) {
	idx := store.NewIndex()
	idx.Profile(store.DefaultProfile).Set("known", store.Entry{ID: "id1"})

	cmd := newExecCmd(
		func() (*store.Index, error) { return idx, nil },
		keychain.NewFake(),
		func(_, _ []string) error { t.Fatal("execer must not run"); return nil },
	)
	cmd.SetArgs([]string{"known", "--", "cmd"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "cft rotate known") {
		t.Errorf("err = %v", err)
	}
}

func TestExec_NoSeparator(t *testing.T) {
	cmd := newExecCmd(
		func() (*store.Index, error) { return store.NewIndex(), nil },
		keychain.NewFake(),
		func(_, _ []string) error { t.Fatal("execer must not run"); return nil },
	)
	cmd.SetArgs([]string{"name", "cmd"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing '--' separator") {
		t.Errorf("err = %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
