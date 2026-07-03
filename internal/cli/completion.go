package cli

import (
	"sort"
	"strings"

	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

type completionFunc func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// completeTokenNames suggests token names in the selected profile for the first
// positional argument. Once it is set we return Default so `exec <name> -- <cmd>`
// falls back to the shell's own completion for the child command.
func completeTokenNames(load indexLoader) completionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		idx, err := load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		profile := selectedProfile(cmd, idx)
		return filterSorted(names(idx.Profile(profile).All()), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

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

func loaderFromPath(indexPath func() (string, error)) indexLoader {
	return func() (*store.Index, error) {
		path, err := indexPath()
		if err != nil {
			return nil, err
		}
		return store.Load(path)
	}
}

func names(entries map[string]store.Entry) []string {
	out := make([]string, 0, len(entries))
	for n := range entries {
		out = append(out, n)
	}
	return out
}

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
