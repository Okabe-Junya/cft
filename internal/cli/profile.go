package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

// EnvProfile selects the active profile when --profile is not given.
const EnvProfile = "CFT_PROFILE"

// resolveProfile picks the active profile using the precedence
//
//	--profile flag > $CFT_PROFILE > index "current" pointer > store.DefaultProfile
//
// Pass idx=nil to skip the current-pointer step (e.g. `cft login`, which sets
// up a profile rather than following the selected one).
func resolveProfile(flagVal string, getenv func(string) string, idx *store.Index) string {
	if flagVal != "" {
		return flagVal
	}
	if getenv != nil {
		if v := getenv(EnvProfile); v != "" {
			return v
		}
	}
	if idx != nil && idx.Current != "" {
		return idx.Current
	}
	return store.DefaultProfile
}

// profileFromCmd reads the inherited --profile persistent flag, returning ""
// when the flag is absent (e.g. a subcommand constructed standalone in a test
// without the root command's persistent flags).
func profileFromCmd(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	if f := cmd.Flag("profile"); f != nil {
		return f.Value.String()
	}
	return ""
}

// selectedProfile resolves the active profile for a command using os.Getenv
// for the environment step. idx supplies the current pointer (nil to skip).
func selectedProfile(cmd *cobra.Command, idx *store.Index) string {
	return resolveProfile(profileFromCmd(cmd), os.Getenv, idx)
}

// profileDeps bundles the side-effect boundaries the profile subcommands cross.
type profileDeps struct {
	login     loginDeps // for `add`: read + verify + store a bootstrap token
	keychain  keychain.Store
	indexPath func() (string, error)
	load      func() (*store.Index, error)
	withLock  func(path string, fn func(*store.Index) error) error
}

func defaultProfileDeps(ks keychain.Store) profileDeps {
	return profileDeps{
		login:     defaultLoginDeps(ks),
		keychain:  ks,
		indexPath: store.DefaultPath,
		load:      defaultIndexLoader,
		withLock:  store.WithLock,
	}
}

func newProfileCmd(deps profileDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage named profiles (one Cloudflare account each)",
		Long: "A profile bundles one account's bootstrap token and its managed tokens. " +
			"Commands select a profile via --profile, $" + EnvProfile + ", or the current " +
			"pointer set by `cft profile use`.",
		Args: cobra.NoArgs,
		// With no subcommand, show current + the list rather than bare help.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProfileList(cmd, deps)
		},
	}
	cmd.AddCommand(newProfileAddCmd(deps))
	cmd.AddCommand(newProfileListCmd(deps))
	cmd.AddCommand(newProfileUseCmd(deps))
	cmd.AddCommand(newProfileCurrentCmd(deps))
	return cmd
}

func newProfileAddCmd(deps profileDeps) *cobra.Command {
	var fromEnv bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Store a bootstrap token under a new profile",
		Long: "Reads and verifies a bootstrap token (TTY, --from-env, or stdin) and stores it " +
			"for profile <name>. Becomes the current profile if no current is set yet; " +
			"otherwise switch with `cft profile use`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := writeBootstrap(cmd, deps.login, fromEnv, name); err != nil {
				return err
			}
			path, err := deps.indexPath()
			if err != nil {
				return err
			}
			var becameCurrent bool
			if err := deps.withLock(path, func(i *store.Index) error {
				i.Profile(name) // materialise the profile so `profile list` shows it
				if i.Current == "" {
					i.Current = name
					becameCurrent = true
				}
				return nil
			}); err != nil {
				return fmt.Errorf("update index: %w", err)
			}
			if becameCurrent {
				fmt.Fprintf(cmd.OutOrStdout(), "added profile %q and set it current\n", name)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "added profile %q (current unchanged; `cft profile use %s` to switch)\n", name, name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromEnv, "from-env", false, "read token from $"+EnvCloudflareToken+" instead of prompting")
	return cmd
}

func newProfileListCmd(deps profileDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles (current, bootstrap presence, token count)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProfileList(cmd, deps)
		},
	}
}

func runProfileList(cmd *cobra.Command, deps profileDeps) error {
	idx, err := deps.load()
	if err != nil {
		return err
	}
	// A profile is "known" if it has a bootstrap token in the Keychain or an
	// entry in the index (it may have one without the other mid-setup).
	bootstraps, err := deps.keychain.List(keychain.ServiceBootstrap)
	if err != nil {
		return fmt.Errorf("list keychain bootstraps: %w", err)
	}
	hasBootstrap := map[string]bool{}
	set := map[string]struct{}{}
	for _, b := range bootstraps {
		hasBootstrap[b] = true
		set[b] = struct{}{}
	}
	for _, n := range idx.ProfileNames() {
		set[n] = struct{}{}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "CURRENT\tPROFILE\tBOOTSTRAP\tTOKENS")
	for _, n := range names {
		marker := ""
		if n == idx.Current {
			marker = "*"
		}
		boot := "no"
		if hasBootstrap[n] {
			boot = "yes"
		}
		count := 0
		if idx.HasProfile(n) {
			count = len(idx.Profile(n).All())
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", marker, n, boot, count)
	}
	return nil
}

func newProfileUseCmd(deps profileDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set the current profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			// Warn (do not block) if the target has no credentials yet, so a
			// typo is visible but pre-staging a profile name stays possible.
			if _, err := deps.keychain.Get(keychain.ServiceBootstrap, name); errors.Is(err, keychain.ErrNotFound) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: profile %q has no bootstrap token yet; run `cft profile add %s` or `cft login --profile %s`\n", name, name, name)
			}
			path, err := deps.indexPath()
			if err != nil {
				return err
			}
			if err := deps.withLock(path, func(i *store.Index) error {
				i.Current = name
				i.Profile(name) // ensure it exists in the index
				return nil
			}); err != nil {
				return fmt.Errorf("update index: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "current profile is now %q\n", name)
			return nil
		},
	}
}

func newProfileCurrentCmd(deps profileDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the current profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			idx, err := deps.load()
			if err != nil {
				return err
			}
			if idx.Current == "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (default; none selected)\n", store.DefaultProfile)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), idx.Current)
			return nil
		},
	}
}
