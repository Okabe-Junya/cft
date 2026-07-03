package cli

import (
	"sort"
	"strings"

	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

// completionFunc is the shape cobra expects for dynamic argument completion
// (ValidArgsFunction). It runs in a throwaway `cft __complete ...` process the
// shell spawns, so it must be cheap and must never prompt or hit the network —
// it only reads the local index.
type completionFunc func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// completeTokenNames suggests managed token names in the selected profile for
// the first positional argument (the <name> of rotate/exec/delete). load is
// the same index seam the command uses, so tests can inject a fixture.
//
// Only the first argument is a token name; once it is set we return
// ShellCompDirectiveDefault so callers like `exec <name> -- <cmd>` fall back to
// the shell's own file/command completion for the child command.
func completeTokenNames(load indexLoader) completionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		idx, err := load()
		if err != nil {
			// A missing or unreadable index is not an error at completion
			// time — just offer nothing and suppress noisy file completion.
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		profile := selectedProfile(cmd, idx)
		return filterSorted(names(idx.Profile(profile).All()), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProfileNames suggests known profile names for the first positional
// argument (the <name> of `profile use`).
func completeProfileNames(load indexLoader) completionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		idx, err := load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterSorted(idx.ProfileNames(), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// loaderFromPath adapts the (indexPath, store.Load) pair some commands hold
// into the indexLoader seam the completion helpers expect.
func loaderFromPath(indexPath func() (string, error)) indexLoader {
	return func() (*store.Index, error) {
		path, err := indexPath()
		if err != nil {
			return nil, err
		}
		return store.Load(path)
	}
}

// names returns the keys of an entry map.
func names(entries map[string]store.Entry) []string {
	out := make([]string, 0, len(entries))
	for n := range entries {
		out = append(out, n)
	}
	return out
}

// filterSorted keeps the candidates that start with prefix and returns them
// sorted. cobra also filters by prefix, but doing it here keeps the suggestion
// list small and deterministic.
func filterSorted(candidates []string, prefix string) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
