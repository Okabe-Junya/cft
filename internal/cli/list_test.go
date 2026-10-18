package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
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

// listJSONOut runs `list --json` against the fixed fixture and returns stdout.
func listJSONOut(t *testing.T, load indexLoader) []byte {
	t.Helper()
	deps := listDeps{
		load:   load,
		now:    func() time.Time { return fixedNow },
		isTTY:  func() bool { return true }, // even on a TTY, --json must stay plain
		getenv: func(string) string { return "" },
	}
	cmd := newListCmdWithDeps(deps)
	cmd.SetArgs([]string{"--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out.Bytes()
}

func TestList_JSON_StructureAndStatus(t *testing.T) {
	raw := listJSONOut(t, func() (*store.Index, error) { return listIndex(), nil })

	if bytes.Contains(raw, []byte("\x1b[")) {
		t.Errorf("--json output must never contain ANSI escapes: %q", raw)
	}

	var got struct {
		Tokens []struct {
			Name    string  `json:"name"`
			ID      string  `json:"id"`
			Expires *string `json:"expires"`
			Status  string  `json:"status"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}

	// Sorted by name, same order the table uses.
	names := make([]string, 0, len(got.Tokens))
	for _, tk := range got.Tokens {
		names = append(names, tk.Name)
	}
	wantNames := []string{"expired-row", "no-expires", "safe-row", "soon-row"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("names = %v, want %v", names, wantNames)
	}

	byName := map[string]struct {
		expires *string
		status  string
	}{}
	for _, tk := range got.Tokens {
		byName[tk.Name] = struct {
			expires *string
			status  string
		}{tk.Expires, tk.Status}
	}

	// status must agree with the colour classification (same 30-day window).
	wantStatus := map[string]string{
		"expired-row": "expired",
		"soon-row":    "expiring_soon",
		"safe-row":    "active",
		"no-expires":  "active",
	}
	for name, want := range wantStatus {
		if byName[name].status != want {
			t.Errorf("status[%s] = %q, want %q", name, byName[name].status, want)
		}
	}

	// Missing expires is JSON null (stable key, not omitted), present otherwise.
	if byName["no-expires"].expires != nil {
		t.Errorf("no-expires.expires = %v, want null", *byName["no-expires"].expires)
	}
	if e := byName["expired-row"].expires; e == nil || *e != "2025-01-01" {
		t.Errorf("expired-row.expires = %v, want \"2025-01-01\"", e)
	}
}

func TestList_JSON_EmptyIndexIsEmptyArray(t *testing.T) {
	raw := listJSONOut(t, func() (*store.Index, error) { return store.NewIndex(), nil })
	// An empty index must serialise tokens as [] (not null) so agents can
	// iterate unconditionally.
	var got struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}
	if got.Tokens == nil {
		t.Errorf("tokens must be [] not null: %s", raw)
	}
	if len(got.Tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d: %s", len(got.Tokens), raw)
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
