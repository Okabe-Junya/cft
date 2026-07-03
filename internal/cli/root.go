// Package cli wires the cobra command tree.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/spf13/cobra"
	cobracompletefig "github.com/withfig/autocomplete-tools/integrations/cobra"
)

// Exit code conventions, per docs/design.md §12. Subcommands signal a code
// other than 1 by returning an error that satisfies exitCoder.
const (
	ExitOK      = 0
	ExitRuntime = 1
	ExitSpec    = 2
	ExitAuth    = 3
)

// exitCoder lets subcommand errors carry a specific process exit code.
// Errors that do not implement it fall through to ExitRuntime.
type exitCoder interface{ ExitCode() int }

// exitErr is the canonical implementation of exitCoder. Subcommands wrap
// their underlying error so the message surface stays intact for users.
type exitErr struct {
	code int
	err  error
}

func (e *exitErr) Error() string { return e.err.Error() }
func (e *exitErr) Unwrap() error { return e.err }
func (e *exitErr) ExitCode() int { return e.code }

// withExit decorates err with code. err is returned unchanged when nil.
func withExit(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitErr{code: code, err: err}
}

func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "cft",
		Short:         "Cloudflare API token manager backed by the macOS Keychain",
		Long:          "cft applies YAML specs for Cloudflare API tokens and stores the issued values in the macOS Keychain, not on disk. See docs/design.md.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var keychainPath string
	var allowAnyApp bool
	root.PersistentFlags().StringVar(&keychainPath, "keychain", "",
		"path to a keychain file to use instead of the login keychain (docs/design.md §6.1)")
	// --profile is a persistent flag so every subcommand can select which
	// account (profile) to act on. Empty means "fall back to $CFT_PROFILE,
	// then the current pointer, then the default profile".
	root.PersistentFlags().String("profile", "", "profile to use (overrides $"+EnvProfile+" and the current pointer)")

	ks := deferredStore{path: &keychainPath}
	loginKS := deferredStore{path: &keychainPath, allowAnyApp: &allowAnyApp}

	root.AddCommand(newListCmd(defaultIndexLoader))
	root.AddCommand(newLoginCmd(loginKS, os.Stdin, &allowAnyApp))
	root.AddCommand(newExecCmd(defaultIndexLoader, ks, defaultExecer))
	root.AddCommand(newApplyCmd(defaultApplyDeps(ks)))
	root.AddCommand(newRotateCmd(defaultRotateDeps(ks)))
	root.AddCommand(newDeleteCmd(defaultDeleteDeps(ks)))
	root.AddCommand(newSchemaCmd())
	root.AddCommand(newProfileCmd(defaultProfileDeps(loginKS)))
	// Hidden `generate-fig-spec`: emits a Fig/Amazon Q autocomplete spec built
	// from this command tree, for the IDE-style popup (which does not use the
	// shell completion scripts). See README "Shell completion".
	root.AddCommand(cobracompletefig.CreateCompletionSpecCommand())
	return root
}

// deferredStore builds the backend per call: the command tree is constructed
// before cobra parses --keychain / --allow-any-app, so an eager keychain.New
// would never see the flags.
type deferredStore struct {
	path        *string
	allowAnyApp *bool
}

func (d deferredStore) store() keychain.Store {
	var opts []keychain.Option
	if *d.path != "" {
		opts = append(opts, keychain.WithPath(*d.path))
	}
	if d.allowAnyApp != nil && *d.allowAnyApp {
		opts = append(opts, keychain.WithAllowAnyApp())
	}
	return keychain.New(opts...)
}

func (d deferredStore) Get(service, account string) ([]byte, error) {
	return d.store().Get(service, account)
}

func (d deferredStore) Set(service, account string, data []byte) error {
	return d.store().Set(service, account, data)
}

func (d deferredStore) Delete(service, account string) error {
	return d.store().Delete(service, account)
}

func (d deferredStore) List(service string) ([]string, error) {
	return d.store().List(service)
}

// Execute runs the cobra command tree and returns a process exit code.
func Execute(version string) int {
	root := newRootCmd(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "cft: %v\n", err)
		var ec exitCoder
		if errors.As(err, &ec) {
			return ec.ExitCode()
		}
		return ExitRuntime
	}
	return ExitOK
}
