package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/keychain"
	"github.com/Okabe-Junya/cft/internal/spec"
	"github.com/Okabe-Junya/cft/internal/store"
	"github.com/spf13/cobra"
)

// cfClient is the cfapi subset the apply command uses. Defined here (not in
// cfapi) so tests can supply a small fake without standing up an
// httptest.Server.
type cfClient interface {
	ListPermissionGroups(ctx context.Context) ([]cfapi.PermissionGroup, error)
	ResolveZoneID(ctx context.Context, name string) (string, error)
	ResolveTokensByName(ctx context.Context, name string) ([]cfapi.Token, error)
	CreateToken(ctx context.Context, s cfapi.TokenSpec) (*cfapi.CreatedToken, error)
	UpdateToken(ctx context.Context, id string, s cfapi.TokenSpec) (*cfapi.Token, error)
}

// applyDeps bundles every side-effect boundary the apply command crosses.
type applyDeps struct {
	keychain     keychain.Store
	indexPath    func() (string, error)
	specs        func(paths ...string) ([]spec.Token, error)
	withLock     func(path string, fn func(*store.Index) error) error
	newClient    func(bootstrapToken string) cfClient
	stderrWarner func(format string, a ...any)
}

func defaultApplyDeps(ks keychain.Store) applyDeps {
	return applyDeps{
		keychain:  ks,
		indexPath: store.DefaultPath,
		specs:     spec.Load,
		withLock:  store.WithLock,
		newClient: func(t string) cfClient { return cfapi.New(t) },
	}
}

func newApplyCmd(deps applyDeps) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply <file|dir> [<file|dir>...]",
		Short: "Reconcile token specs with Cloudflare + Keychain + local index",
		Long: "Reads YAML token specs and ensures Cloudflare matches them: creates missing tokens (storing values in the Keychain), " +
			"updates existing token policies in-place, and never alters token values (use `cft rotate` for that). " +
			"When a spec'd name is absent from the local index but already exists in Cloudflare, apply adopts that token " +
			"(records its ID and updates the policy in place) instead of creating a duplicate; since its value is unknown locally, run `cft rotate <name>` before `cft exec`.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tokens, err := deps.specs(args...)
			if err != nil {
				return withExit(ExitSpec, err)
			}
			if err := spec.ValidateAll(tokens); err != nil {
				return withExit(ExitSpec, err)
			}
			if deps.stderrWarner == nil {
				deps.stderrWarner = func(format string, a ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: "+format+"\n", a...)
				}
			}
			indexPath, err := deps.indexPath()
			if err != nil {
				return err
			}
			// Resolve the profile before the lock: the bootstrap token is
			// keyed by profile, and the current pointer lives in the index.
			pre, err := store.Load(indexPath)
			if err != nil {
				return err
			}
			profile := selectedProfile(cmd, pre)
			return runApply(cmd.Context(), cmd.OutOrStdout(), tokens, deps, dryRun, profile)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan without calling the Cloudflare API")
	return cmd
}

// runApply orchestrates the reconcile loop for a single profile. It is split
// out from the cobra closure so tests can drive it directly when convenient.
func runApply(ctx context.Context, out io.Writer, tokens []spec.Token, deps applyDeps, dryRun bool, profile string) error {
	bootstrap, err := deps.keychain.Get(keychain.ServiceBootstrap, profile)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return withExit(ExitAuth, fmt.Errorf("bootstrap token not found for profile %q. Run: cft login --profile %s", profile, profile))
		}
		return err
	}
	client := deps.newClient(string(bootstrap))

	r, err := newAPIResolver(ctx, client)
	if err != nil {
		return err
	}

	indexPath, err := deps.indexPath()
	if err != nil {
		return err
	}

	var applied, total int
	// We compute every action under the lock so a concurrent apply cannot
	// race index reads against our cfapi mutations. executePlan signals
	// partial progress via *store.PartialError so WithLock still persists
	// successful entries when a later action fails.
	lockErr := deps.withLock(indexPath, func(idx *store.Index) error {
		p := idx.Profile(profile)
		plan, err := buildPlan(ctx, tokens, p, r, client)
		if err != nil {
			return err
		}
		printPlan(out, plan, dryRun)
		if dryRun {
			return nil
		}
		total = len(plan)
		var perr error
		applied, perr = executePlan(ctx, plan, p, client, deps, profile)
		return perr
	})
	if !dryRun {
		printApplySummary(out, applied, total, lockErr)
	}
	return lockErr
}

// action describes one reconcile decision for a single spec token.
type action struct {
	token   spec.Token
	cf      cfapi.TokenSpec
	op      string // "create" | "update" | "adopt"
	id      string // populated when op=="update" or op=="adopt"
	expires string
}

// buildPlan decides create/update/adopt for each spec token. Names present in
// the local index update in place. Names absent from the index are looked up
// remotely by exact name: 0 matches create, exactly 1 adopts (records the
// existing ID and updates its policy without touching the value), and >1 is an
// ambiguous error the operator must resolve by hand. The remote lookup only
// runs for index misses, so an intact index preserves the prior behavior.
func buildPlan(ctx context.Context, tokens []spec.Token, p *store.Profile, r resolver, client cfClient) ([]action, error) {
	out := make([]action, 0, len(tokens))
	for _, t := range tokens {
		cf, err := translateToken(t, r)
		if err != nil {
			return nil, err
		}
		a := action{token: t, cf: cf, expires: t.Expires}
		if e, ok := p.Get(t.Name); ok {
			a.op = "update"
			a.id = e.ID
		} else {
			matches, err := client.ResolveTokensByName(ctx, t.Name)
			if err != nil {
				return nil, fmt.Errorf("look up existing token %q: %w", t.Name, err)
			}
			switch len(matches) {
			case 0:
				a.op = "create"
			case 1:
				a.op = "adopt"
				a.id = matches[0].ID
			default:
				ids := make([]string, len(matches))
				for i, m := range matches {
					ids[i] = m.ID
				}
				return nil, fmt.Errorf("token %q is ambiguous: %d existing Cloudflare tokens share this name (IDs: %s); delete the duplicates so exactly one remains, then re-run apply", t.Name, len(matches), strings.Join(ids, ", "))
			}
		}
		out = append(out, a)
	}
	return out, nil
}

func printPlan(w io.Writer, plan []action, dryRun bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	header := "OP\tNAME\tID\tEXPIRES"
	if dryRun {
		header = "PLAN " + header
	}
	fmt.Fprintln(tw, header)
	for _, a := range plan {
		exp := a.expires
		if exp == "" {
			exp = "-"
		}
		id := a.id
		if id == "" {
			id = "(new)"
		}
		prefix := ""
		if dryRun {
			prefix = "(dry) \t"
		}
		fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\n", prefix, a.op, a.token.Name, id, exp)
	}
}

// executePlan runs each action in order. The returned count is the number
// of actions that fully succeeded (Cloudflare + Keychain + idx mutation).
// If a later action fails after at least one success, the error is wrapped
// in *store.PartialError so WithLock persists the successful entries.
func executePlan(ctx context.Context, plan []action, p *store.Profile, client cfClient, deps applyDeps, profile string) (int, error) {
	for i, a := range plan {
		if err := applyOne(ctx, a, p, client, deps, profile); err != nil {
			if i > 0 {
				return i, &store.PartialError{Err: err}
			}
			return 0, err
		}
	}
	return len(plan), nil
}

func applyOne(ctx context.Context, a action, p *store.Profile, client cfClient, deps applyDeps, profile string) error {
	acct := keychain.TokenAccount(profile, a.token.Name)
	switch a.op {
	case "create":
		resp, err := client.CreateToken(ctx, a.cf)
		if err != nil {
			return fmt.Errorf("create token %q: %w", a.token.Name, err)
		}
		if err := deps.keychain.Set(keychain.ServiceTokens, acct, []byte(resp.Value)); err != nil {
			return fmt.Errorf("write keychain for %q: %w", a.token.Name, err)
		}
		p.Set(a.token.Name, store.Entry{ID: resp.ID, Expires: a.expires})
	case "update":
		if _, err := client.UpdateToken(ctx, a.id, a.cf); err != nil {
			return fmt.Errorf("update token %q: %w", a.token.Name, err)
		}
		if _, err := deps.keychain.Get(keychain.ServiceTokens, acct); errors.Is(err, keychain.ErrNotFound) {
			deps.stderrWarner("token %q value missing from Keychain; run `cft rotate %s` to re-issue", a.token.Name, a.token.Name)
		}
		p.Set(a.token.Name, store.Entry{ID: a.id, Expires: a.expires})
	case "adopt":
		// Adopt an existing same-name token the index lost track of: update
		// its policy in place and record the ID, but never write a value to
		// the Keychain — the live value is unknown, so cft exec needs a
		// `cft rotate` first to mint one it can read.
		if _, err := client.UpdateToken(ctx, a.id, a.cf); err != nil {
			return fmt.Errorf("adopt token %q: %w", a.token.Name, err)
		}
		p.Set(a.token.Name, store.Entry{ID: a.id, Expires: a.expires})
		deps.stderrWarner("adopted existing token %q (id %s); its value is not stored locally, run `cft rotate %s` before `cft exec`", a.token.Name, a.id, a.token.Name)
	}
	return nil
}

func printApplySummary(w io.Writer, applied, total int, err error) {
	if total == 0 {
		return
	}
	if err == nil {
		fmt.Fprintf(w, "applied: %d\n", applied)
		return
	}
	fmt.Fprintf(w, "applied: %d of %d\n", applied, total)
}
