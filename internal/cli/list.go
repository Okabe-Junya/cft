package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// indexLoader is the seam used by newListCmd so tests can inject a fixture
// index without writing to disk.
type indexLoader func() (*store.Index, error)

// listDeps bundles every side-effect boundary the list command crosses so
// colour rendering, the wall clock, and TTY detection can all be replaced
// from tests without touching package-level globals.
type listDeps struct {
	load   indexLoader
	now    func() time.Time
	isTTY  func() bool
	getenv func(string) string
}

func defaultIndexLoader() (*store.Index, error) {
	path, err := store.DefaultPath()
	if err != nil {
		return nil, err
	}
	return store.Load(path)
}

func defaultListDeps() listDeps {
	return listDeps{
		load:   defaultIndexLoader,
		now:    time.Now,
		isTTY:  func() bool { return term.IsTerminal(int(os.Stdout.Fd())) },
		getenv: os.Getenv,
	}
}

// newListCmd preserves the original constructor signature so existing
// callers (root, unit tests that only care about layout) keep working.
func newListCmd(load indexLoader) *cobra.Command {
	deps := defaultListDeps()
	deps.load = load
	return newListCmdWithDeps(deps)
}

func newListCmdWithDeps(deps listDeps) *cobra.Command {
	var noColor bool
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List managed tokens (name, id, expires) from the local index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			idx, err := deps.load()
			if err != nil {
				return err
			}
			profile := selectedProfile(cmd, idx)
			entries := idx.Profile(profile).All()
			if asJSON {
				return renderListJSON(cmd.OutOrStdout(), entries, deps.now())
			}
			useColor := shouldUseColor(noColor, deps)
			return renderList(cmd.OutOrStdout(), entries, deps.now(), useColor)
		},
	}
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable ANSI colour even on a TTY")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit machine-readable JSON instead of the table")
	return cmd
}

// shouldUseColor follows the convention NO_COLOR > --no-color > TTY detection.
// We honour https://no-color.org/: any non-empty NO_COLOR disables colour.
func shouldUseColor(noColorFlag bool, deps listDeps) bool {
	if noColorFlag {
		return false
	}
	if deps.getenv("NO_COLOR") != "" {
		return false
	}
	return deps.isTTY()
}

// ANSI styles for the EXPIRES column. We keep colour selection out of the
// tabwriter step because tabwriter counts bytes for column widths and would
// add spurious whitespace if ANSI escapes were embedded in cells.
const (
	ansiReset  = "\x1b[0m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

type rowStyle int

const (
	styleNormal rowStyle = iota
	styleExpiringSoon
	styleExpired
)

// expiringSoonWindow is the threshold for the "soon" warning. Hard-coded for
// v1 — the exact value is left open for a future implementer to tune; 30d
// matches the dashboard convention.
const expiringSoonWindow = 30 * 24 * time.Hour

// expiresStyle classifies a row by its expires value. Empty or unparseable
// expires is treated as styleNormal — `list` is a read-only view and silently
// degrading is better than erroring out on a freshly hand-edited index.
func expiresStyle(expires string, now time.Time) rowStyle {
	if expires == "" {
		return styleNormal
	}
	t, ok := parseExpiresForList(expires)
	if !ok {
		return styleNormal
	}
	diff := t.Sub(now)
	if diff <= 0 {
		return styleExpired
	}
	if diff <= expiringSoonWindow {
		return styleExpiringSoon
	}
	return styleNormal
}

// parseExpiresForList accepts the same formats the spec validator allows but
// kept inline so `list` does not depend on the spec package for one helper.
func parseExpiresForList(s string) (time.Time, bool) {
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(f, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func renderList(w io.Writer, entries map[string]store.Entry, now time.Time, useColor bool) error {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)

	// Render plain to a buffer first so tabwriter computes column widths from
	// the actual content. Colour is applied row-by-row afterwards.
	var buf bytes.Buffer
	tw := newTable(&buf)
	fmt.Fprintln(tw, "NAME\tID\tEXPIRES")
	for _, n := range names {
		e := entries[n]
		expires := e.Expires
		if expires == "" {
			expires = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, e.ID, expires)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render table: %w", err)
	}

	if !useColor {
		if _, err := w.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("write table: %w", err)
		}
		return nil
	}

	// Walk the rendered text line-by-line. The first line is the header and
	// stays uncoloured; the rest align 1:1 with sorted names. The bounds
	// check guards against trailing blank lines tabwriter might emit.
	for i, line := range strings.SplitAfter(buf.String(), "\n") {
		style := styleNormal
		if i > 0 && line != "" && i-1 < len(names) {
			style = expiresStyle(entries[names[i-1]].Expires, now)
		}
		if err := writeStyledLine(w, line, style); err != nil {
			return err
		}
	}
	return nil
}

// newTable returns the tabwriter layout shared by every tabular command.
func newTable(w io.Writer) *tabwriter.Writer {
	const columnPadding = 2
	return tabwriter.NewWriter(w, 0, 0, columnPadding, ' ', 0)
}

// writeStyledLine wraps the line's content (without its trailing newline) in
// the style's colour so the reset lands before the line break — terminals
// stop at \n.
func writeStyledLine(w io.Writer, line string, style rowStyle) error {
	prefix, suffix := ansiFor(style)
	if prefix != "" {
		line = prefix + strings.TrimRight(line, "\n") + suffix + "\n"
	}
	if _, err := io.WriteString(w, line); err != nil {
		return fmt.Errorf("write table: %w", err)
	}
	return nil
}

// listToken is one row of `list --json`. Keys are always present (expires is
// null rather than omitted) so an agent parses a stable schema every run.
type listToken struct {
	Name    string  `json:"name"`
	ID      string  `json:"id"`
	Expires *string `json:"expires"`
	Status  string  `json:"status"`
}

type listJSON struct {
	Tokens []listToken `json:"tokens"`
}

// statusFor maps the row's colour classification to a machine-readable string,
// so the JSON view carries the same expiry signal the table encodes in colour.
func statusFor(s rowStyle) string {
	switch s {
	case styleExpired:
		return "expired"
	case styleExpiringSoon:
		return "expiring_soon"
	case styleNormal:
	}
	return "active"
}

// renderListJSON writes the entries as a JSON object sorted by name. The
// tokens slice is non-nil so an empty index serialises as [] not null.
func renderListJSON(w io.Writer, entries map[string]store.Entry, now time.Time) error {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)

	out := listJSON{Tokens: make([]listToken, 0, len(names))}
	for _, n := range names {
		e := entries[n]
		var expires *string
		if e.Expires != "" {
			v := e.Expires
			expires = &v
		}
		out.Tokens = append(out.Tokens, listToken{
			Name:    n,
			ID:      e.ID,
			Expires: expires,
			Status:  statusFor(expiresStyle(e.Expires, now)),
		})
	}

	return writeJSON(w, out)
}

// writeJSON writes v as indented JSON followed by a newline.
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(b)); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// ansiFor returns the escape sequences that open and close the style's
// colour; both are empty for styleNormal.
func ansiFor(s rowStyle) (string, string) {
	switch s {
	case styleExpired:
		return ansiRed, ansiReset
	case styleExpiringSoon:
		return ansiYellow, ansiReset
	case styleNormal:
	}
	return "", ""
}
