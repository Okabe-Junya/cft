package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

// rotater is the cfapi subset rotate needs.
type rotater interface {
	RollToken(ctx context.Context, id string) (string, error)
}

type rotateDeps struct {
	keychain  keychain.Store
	indexPath func() (string, error)
	newClient func(bootstrapToken string) rotater
	stdin     io.Reader
}

func defaultRotateDeps(ks keychain.Store) rotateDeps {
	return rotateDeps{
		keychain:  ks,
		indexPath: store.DefaultPath,
		newClient: func(t string) rotater { return cfapi.New(t) },
		stdin:     os.Stdin,
	}
}

func newRotateCmd(deps rotateDeps) *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "rotate <name>",
		Short: "Re-issue a token's value via the Cloudflare roll endpoint",
		Long: "Calls /user/tokens/<id>/value to mint a new value and overwrites the Keychain entry. " +
			"The token's policy and ID are untouched; only the secret rotates. " +
			"Any process still holding the old value will start failing at this point.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path, err := deps.indexPath()
			if err != nil {
				return err
			}
			idx, err := store.Load(path)
			if err != nil {
				return err
			}
			profile := selectedProfile(cmd, idx)
			entry, ok := idx.Profile(profile).Get(name)
			if !ok {
				return fmt.Errorf("token %q not found in profile %q. Run: cft apply ./<spec>.yaml", name, profile)
			}

			if !assumeYes {
				yes, err := confirm(deps.stdin, cmd.OutOrStderr(),
					fmt.Sprintf("Rotate %q (id %s)? Any process using the current value will start failing.", name, entry.ID))
				if err != nil {
					return err
				}
				if !yes {
					fmt.Fprintln(cmd.OutOrStdout(), "aborted")
					return nil
				}
			}

			bootstrap, err := deps.keychain.Get(keychain.ServiceBootstrap, profile)
			if err != nil {
				if errors.Is(err, keychain.ErrNotFound) {
					return withExit(ExitAuth, fmt.Errorf("bootstrap token not found for profile %q. Run: cft login --profile %s", profile, profile))
				}
				return err
			}
			client := deps.newClient(string(bootstrap))
			newVal, err := client.RollToken(cmd.Context(), entry.ID)
			if err != nil {
				return fmt.Errorf("roll token: %w", err)
			}
			if err := deps.keychain.Set(keychain.ServiceTokens, keychain.TokenAccount(profile, name), []byte(newVal)); err != nil {
				return fmt.Errorf("write keychain: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated %q\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip confirmation prompt")
	return cmd
}
