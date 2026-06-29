package cli

import (
	"bytes"
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
			useColor := shouldUseColor(noColor, deps)
			return renderList(cmd.OutOrStdout(), idx.Profile(profile).All(), deps.now(), useColor)
		},
	}
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable ANSI colour even on a TTY")
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
// v1 — design.md §15 Open Question #4 leaves the exact value to the
// implementer; 30d matches the dashboard convention.
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
	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tID\tEXPIRES"); err != nil {
		return err
	}
	for _, n := range names {
		e := entries[n]
		expires := e.Expires
		if expires == "" {
			expires = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", n, e.ID, expires); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if !useColor {
		_, err := w.Write(buf.Bytes())
		return err
	}

	// Walk the rendered text line-by-line. The first line is the header and
	// stays uncoloured; the rest align 1:1 with sorted names.
	lines := strings.SplitAfter(buf.String(), "\n")
	for i, line := range lines {
		if i == 0 || line == "" {
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
			continue
		}
		// i-1 indexes into names; bounds check guards against trailing blank
		// lines tabwriter might emit.
		idx := i - 1
		if idx >= len(names) {
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
			continue
		}
		style := expiresStyle(entries[names[idx]].Expires, now)
		prefix, suffix := ansiFor(style)
		if prefix == "" {
			if _, err := io.WriteString(w, line); err != nil {
				return err
			}
			continue
		}
		// Wrap the row's content (without trailing newline) so the colour
		// reset lands before the line break — terminals stop at \n.
		content := strings.TrimRight(line, "\n")
		if _, err := fmt.Fprintf(w, "%s%s%s\n", prefix, content, suffix); err != nil {
			return err
		}
	}
	return nil
}

func ansiFor(s rowStyle) (prefix, suffix string) {
	switch s {
	case styleExpired:
		return ansiRed, ansiReset
	case styleExpiringSoon:
		return ansiYellow, ansiReset
	default:
		return "", ""
	}
}
