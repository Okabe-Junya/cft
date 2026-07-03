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

// deleter is the cfapi subset delete needs.
type deleter interface {
	DeleteToken(ctx context.Context, id string) error
}

type deleteDeps struct {
	keychain  keychain.Store
	indexPath func() (string, error)
	withLock  func(path string, fn func(*store.Index) error) error
	newClient func(bootstrapToken string) deleter
	stdin     io.Reader
}

func defaultDeleteDeps(ks keychain.Store) deleteDeps {
	return deleteDeps{
		keychain:  ks,
		indexPath: store.DefaultPath,
		withLock:  store.WithLock,
		newClient: func(t string) deleter { return cfapi.New(t) },
		stdin:     os.Stdin,
	}
}

func newDeleteCmd(deps deleteDeps) *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a token from Cloudflare, the Keychain, and the local index",
		Long: "Removes the token in three steps: Cloudflare DELETE, Keychain Delete, index Delete. " +
			"A 404 from Cloudflare is treated as 'already gone' and the cleanup continues so the local state cannot drift.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeTokenNames(loaderFromPath(deps.indexPath)),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path, err := deps.indexPath()
			if err != nil {
				return err
			}
			// Read once before locking so the confirmation message can show
			// the id. The actual mutation happens under the lock below.
			idx, err := store.Load(path)
			if err != nil {
				return err
			}
			profile := selectedProfile(cmd, idx)
			entry, ok := idx.Profile(profile).Get(name)
			if !ok {
				return fmt.Errorf("token %q not found in profile %q", name, profile)
			}

			if !assumeYes {
				yes, err := confirm(deps.stdin, cmd.OutOrStderr(),
					fmt.Sprintf("Delete %q (id %s) from Cloudflare, Keychain, and the local index?", name, entry.ID))
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

			// Step 1: Cloudflare. 404 means it is already gone — keep going.
			if err := client.DeleteToken(cmd.Context(), entry.ID); err != nil {
				var apiErr *cfapi.Error
				if !errors.As(err, &apiErr) || !apiErr.NotFound() {
					return fmt.Errorf("delete cloudflare token: %w", err)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: Cloudflare returned 404 for %s; continuing with local cleanup\n", entry.ID)
			}

			// Step 2: Keychain. Already idempotent.
			if err := deps.keychain.Delete(keychain.ServiceTokens, keychain.TokenAccount(profile, name)); err != nil {
				return fmt.Errorf("delete keychain value: %w", err)
			}

			// Step 3: index. Done under the lock so the file shows the new
			// state atomically.
			if err := deps.withLock(path, func(i *store.Index) error {
				i.Profile(profile).Delete(name)
				return nil
			}); err != nil {
				return fmt.Errorf("update index: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "deleted %q\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&assumeYes, "yes", "y", false, "skip confirmation prompt")
	return cmd
}
