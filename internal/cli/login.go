package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// EnvCloudflareToken is the environment variable read by `cft login --from-env`.
const EnvCloudflareToken = "CLOUDFLARE_API_TOKEN"

// tokenVerifier is the seam used by login. Production calls cfapi; tests
// inject a fake.
type tokenVerifier interface {
	Verify(ctx context.Context) (*cfapi.Token, error)
}

// loginDeps holds everything login needs to be tested without a real
// terminal, network, or Keychain.
type loginDeps struct {
	keychain    keychain.Store
	stdin       io.Reader
	getenv      func(string) string
	isTTY       func() bool
	readSecret  func() (string, error) // no-echo TTY read
	newVerifier func(token string, opts ...cfapi.Option) tokenVerifier
}

// defaultLoginDeps wires the production loginDeps (the given Keychain backend,
// stdin, TTY, and cfapi verifier). Shared by `cft login` and `cft profile add`
// so both honour the root --keychain backend.
func defaultLoginDeps(ks keychain.Store) loginDeps {
	return loginDeps{
		keychain: ks,
		stdin:    os.Stdin,
		getenv:   os.Getenv,
		isTTY:    func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		readSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
		newVerifier: func(token string, opts ...cfapi.Option) tokenVerifier {
			return cfapi.New(token, opts...)
		},
	}
}

func newLoginCmd(ks keychain.Store, stdin io.Reader, allowAnyApp *bool) *cobra.Command {
	deps := defaultLoginDeps(ks)
	deps.stdin = stdin
	cmd := newLoginCmdWithDeps(deps)
	cmd.Flags().BoolVar(allowAnyApp, "allow-any-app", false,
		"relax the Keychain ACL so any application can read the stored token without a prompt (test-time only)")
	return cmd
}

// newLoginCmdWithDeps is the test entry point.
func newLoginCmdWithDeps(deps loginDeps) *cobra.Command {
	var fromEnv bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store a Cloudflare bootstrap API token in the macOS Keychain",
		Long: "Reads a bootstrap Cloudflare API token from a TTY prompt (no echo), " +
			"from --from-env (CLOUDFLARE_API_TOKEN), or from stdin if not a TTY. " +
			"Verifies the token against /user/tokens/verify and stores it in the login keychain at " +
			"service=" + keychain.ServiceBootstrap + " account=<profile> (the selected profile, " +
			"default " + keychain.BootstrapAccount + "). Use --profile or `cft profile add` to manage " +
			"more than one account.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// login establishes a profile's bootstrap; it resolves via flag /
			// $CFT_PROFILE / default, deliberately not the `current` pointer
			// (which lives in the index login does not otherwise touch).
			profile := resolveProfile(profileFromCmd(cmd), deps.getenv, nil)
			if err := writeBootstrap(cmd, deps, fromEnv, profile); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "bootstrap token stored for profile %q\n", profile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromEnv, "from-env", false, "read token from $"+EnvCloudflareToken+" instead of prompting")
	return cmd
}

// writeBootstrap reads, verifies, and stores a bootstrap token for the given
// profile. Shared by `cft login` and `cft profile add`. The Keychain write
// only happens after a successful Verify so a bad token never lands on disk.
func writeBootstrap(cmd *cobra.Command, deps loginDeps, fromEnv bool, profile string) error {
	token, err := readBootstrapToken(cmd.OutOrStderr(), deps, fromEnv)
	if err != nil {
		return err
	}
	v := deps.newVerifier(token)
	if _, err := v.Verify(cmd.Context()); err != nil {
		return withExit(ExitAuth, fmt.Errorf("verify token: %w", err))
	}
	if err := deps.keychain.Set(keychain.ServiceBootstrap, profile, []byte(token)); err != nil {
		return fmt.Errorf("write keychain: %w", err)
	}
	return nil
}

// readBootstrapToken decides where to read the token from and returns it
// trimmed. Empty input is rejected before we burn a Verify call on it.
func readBootstrapToken(prompt io.Writer, deps loginDeps, fromEnv bool) (string, error) {
	var raw string
	switch {
	case fromEnv:
		raw = deps.getenv(EnvCloudflareToken)
		if raw == "" {
			return "", withExit(ExitAuth, fmt.Errorf("$%s is empty", EnvCloudflareToken))
		}
	case deps.isTTY():
		fmt.Fprint(prompt, "Cloudflare API token: ")
		s, err := deps.readSecret()
		fmt.Fprintln(prompt) // newline after the masked input
		if err != nil {
			return "", fmt.Errorf("read tty: %w", err)
		}
		raw = s
	default:
		s, err := bufio.NewReader(deps.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		raw = s
	}
	tok := strings.TrimSpace(raw)
	if tok == "" {
		return "", withExit(ExitAuth, errors.New("empty token"))
	}
	return tok, nil
}
