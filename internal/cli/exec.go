package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/spf13/cobra"
)

// EnvName is the environment variable injected into the child process. v1
// design pins this; changing it later would require a flag.
const EnvName = "CLOUDFLARE_API_TOKEN"

// execer runs argv with env. In production this is syscall.Exec, which
// replaces the current process so signal handling and the child's exit code
// flow through naturally. Tests substitute a recording fake.
type execer func(argv []string, env []string) error

func defaultExecer(argv []string, env []string) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscall.Exec(bin, argv, env)
}

func newExecCmd(load indexLoader, ks keychain.Store, run execer) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <name> -- <command> [args...]",
		Short: "Run <command> with the named token injected as " + EnvName,
		Long: "Looks up <name> in the local index, reads its value from the macOS Keychain " +
			"(which may prompt for Touch ID), and execs <command> with " + EnvName + " set. " +
			"The '--' separator is required so cobra leaves the child's flags alone.",
		// Custom args check; cobra's MinimumNArgs cannot tell us where '--' was.
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeTokenNames(load),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, argv, err := splitExecArgs(args, cmd.ArgsLenAtDash())
			if err != nil {
				return err
			}
			idx, err := load()
			if err != nil {
				return err
			}
			profile := selectedProfile(cmd, idx)
			if _, ok := idx.Profile(profile).Get(name); !ok {
				return fmt.Errorf("token %q not found in profile %q. Run: cft apply ./<spec>.yaml", name, profile)
			}
			data, err := ks.Get(keychain.ServiceTokens, keychain.TokenAccount(profile, name))
			if err != nil {
				if errors.Is(err, keychain.ErrNotFound) {
					return fmt.Errorf("token value for %q missing from Keychain. Run: cft rotate %s", name, name)
				}
				return err
			}
			env := buildEnv(os.Environ(), string(data))
			return run(argv, env)
		},
	}
}

// splitExecArgs extracts the token name and the child's argv from cobra's
// args list and the index of '--'. Returns a helpful error when args are
// malformed. The dashIdx convention follows cobra: -1 means '--' was not
// present.
func splitExecArgs(args []string, dashIdx int) (name string, argv []string, err error) {
	if dashIdx < 0 {
		return "", nil, errors.New("missing '--' separator; usage: cft exec <name> -- <command> [args...]")
	}
	if dashIdx != 1 {
		return "", nil, fmt.Errorf("expected one argument before '--' (the token name); got %d", dashIdx)
	}
	if len(args) <= dashIdx {
		return "", nil, errors.New("missing command after '--'")
	}
	return args[0], args[dashIdx:], nil
}

// buildEnv returns base with EnvName replaced (or appended) using value.
// It does not mutate base.
func buildEnv(base []string, value string) []string {
	out := make([]string, 0, len(base)+1)
	prefix := EnvName + "="
	replaced := false
	for _, e := range base {
		if strings.HasPrefix(e, prefix) {
			out = append(out, prefix+value)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}
