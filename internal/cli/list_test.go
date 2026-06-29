package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Okabe-Junya/cft/internal/store"
)

func TestList_EmptyIndex(t *testing.T) {
	cmd := newListCmd(func() (*store.Index, error) {
		return store.NewIndex(), nil
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "EXPIRES") {
		t.Errorf("missing header: %q", got)
	}
	// Exactly one line of output (the header) when index is empty.
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d: %q", len(lines), got)
	}
}

func TestList_SortedByName(t *testing.T) {
	cmd := newListCmd(func() (*store.Index, error) {
		i := store.NewIndex()
		// Insert deliberately out of order to verify sort.
		i.Profile(store.DefaultProfile).Set("zeta", store.Entry{ID: "zid", Expires: "2027-01-01"})
		i.Profile(store.DefaultProfile).Set("alpha", store.Entry{ID: "aid", Expires: "2026-12-31"})
		i.Profile(store.DefaultProfile).Set("mike", store.Entry{ID: "mid"}) // no expires → "-"
		return i, nil
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (header + 3), got %d:\n%s", len(lines), got)
	}
	// Verify rows are in alphabetical order on the first column.
	for i, want := range []string{"NAME", "alpha", "mike", "zeta"} {
		if !strings.HasPrefix(strings.TrimLeft(lines[i], " "), want) {
			t.Errorf("line %d = %q, expected to start with %q", i, lines[i], want)
		}
	}
	// Missing expires renders as a placeholder rather than blank.
	if !strings.Contains(lines[2], "-") {
		t.Errorf("expected '-' placeholder for missing expires, got %q", lines[2])
	}
}

func TestList_PropagatesLoadError(t *testing.T) {
	sentinel := errString("load boom")
	cmd := newListCmd(func() (*store.Index, error) { return nil, sentinel })
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// listIndex builds a fixture with known names and expires for colour tests.
func listIndex() *store.Index {
	i := store.NewIndex()
	i.Profile(store.DefaultProfile).Set("expired-row", store.Entry{ID: "id-e", Expires: "2025-01-01"})
	i.Profile(store.DefaultProfile).Set("soon-row", store.Entry{ID: "id-s", Expires: "2026-06-10"}) // ~11 days after fixedNow
	i.Profile(store.DefaultProfile).Set("safe-row", store.Entry{ID: "id-x", Expires: "2027-01-01"})
	i.Profile(store.DefaultProfile).Set("no-expires", store.Entry{ID: "id-n"})
	return i
}

// fixedNow is the wall clock the colour tests pin to. 2026-05-30 is between
// "expired-row" (past) and "soon-row" (~11 days ahead).
var fixedNow = time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)

func runColored(t *testing.T, noColor bool, isTTY bool, env map[string]string) string {
	t.Helper()
	deps := listDeps{
		load:  func() (*store.Index, error) { return listIndex(), nil },
		now:   func() time.Time { return fixedNow },
		isTTY: func() bool { return isTTY },
		getenv: func(k string) string {
			if env == nil {
				return ""
			}
			return env[k]
		},
	}
	cmd := newListCmdWithDeps(deps)
	args := []string{}
	if noColor {
		args = append(args, "--no-color")
	}
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.String()
}

func TestList_Color_OnTTY_HighlightsExpiredAndSoon(t *testing.T) {
	got := runColored(t, false /*noColor*/, true /*isTTY*/, nil)
	// Header carries no ANSI.
	header := strings.SplitN(got, "\n", 2)[0]
	if strings.Contains(header, "\x1b[") {
		t.Errorf("header should not contain ANSI: %q", header)
	}
	// Expired row → red. Soon row → yellow. Safe and no-expires rows → no ANSI.
	for _, want := range []struct {
		row, ansi string
	}{
		{"expired-row", ansiRed},
		{"soon-row", ansiYellow},
	} {
		if !strings.Contains(got, want.ansi) {
			t.Errorf("expected %s ANSI for row %s; out=%q", want.ansi, want.row, got)
		}
	}
	// Reset must close every coloured line so colour does not bleed into the
	// shell prompt.
	if strings.Count(got, ansiReset) != 2 {
		t.Errorf("expected two ANSI resets (one per coloured row), got %d in %q",
			strings.Count(got, ansiReset), got)
	}
	// Lines for safe-row and no-expires must be ANSI-free.
	for _, name := range []string{"safe-row", "no-expires"} {
		for _, line := range strings.Split(got, "\n") {
			if strings.Contains(line, name) && strings.Contains(line, "\x1b[") {
				t.Errorf("row %s should be uncoloured, got %q", name, line)
			}
		}
	}
}

func TestList_Color_OffWhenNotTTY(t *testing.T) {
	got := runColored(t, false, false, nil)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("non-TTY output must be plain, got %q", got)
	}
}

func TestList_Color_NoColorFlagWins(t *testing.T) {
	got := runColored(t, true /*noColor*/, true /*isTTY*/, nil)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("--no-color must suppress ANSI even on TTY, got %q", got)
	}
}

func TestList_Color_NoColorEnvWins(t *testing.T) {
	got := runColored(t, false, true, map[string]string{"NO_COLOR": "1"})
	if strings.Contains(got, "\x1b[") {
		t.Errorf("NO_COLOR must suppress ANSI, got %q", got)
	}
}

func TestExpiresStyle_Boundaries(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want rowStyle
	}{
		{"", styleNormal},
		{"garbage", styleNormal},
		{"2026-05-29", styleExpired},                          // yesterday
		{"2026-05-30T00:00:00Z", styleExpired},                // exactly now-ish (≤0)
		{"2026-06-10", styleExpiringSoon},                     // 11 days out
		{"2026-06-29", styleExpiringSoon},                     // ~30 days, still within window
		{"2026-07-30", styleNormal},                           // ~61 days out
		{"2027-01-01T00:00:00Z", styleNormal},                 // RFC3339 far future
		{"2026-06-15T00:00:00.123456789Z", styleExpiringSoon}, // RFC3339Nano
	}
	for _, c := range cases {
		got := expiresStyle(c.in, now)
		if got != c.want {
			t.Errorf("expiresStyle(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
