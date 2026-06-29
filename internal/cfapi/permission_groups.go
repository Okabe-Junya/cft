package cfapi

import (
	"context"
	"fmt"

	"github.com/cloudflare/cloudflare-go/v7/user"
)

// Cloudflare resource scopes as they appear in a permission group's `scopes`
// list and in TokenPolicy.Resources keys. ScopeUnknown is cft's own bucket
// for groups the API returns without any scope; lookups fall back to it so
// such groups stay resolvable from any policy scope.
const (
	ScopeAccount = "com.cloudflare.api.account"
	ScopeZone    = "com.cloudflare.api.account.zone"
	ScopeUser    = "com.cloudflare.api.user"
	ScopeUnknown = ""
)

// PermissionGroup is the lookup record used both as the response shape of
// ListPermissionGroups and inside TokenPolicy.PermissionGroups when sending a
// CreateToken / UpdateToken request. We only carry the fields cft uses.
type PermissionGroup struct {
	ID     string
	Name   string
	Scopes []string
}

// ListPermissionGroups returns every permission group available to the
// authenticated user, drained via the SDK's auto-pagination. This is the
// fix-for-free that the SDK gives us: the hand-rolled client only ever
// fetched page 1, so groups outside the first page produced a misleading
// "unknown permission group" failure in `cft apply`.
func (c *Client) ListPermissionGroups(ctx context.Context) ([]PermissionGroup, error) {
	iter := c.cf.User.Tokens.PermissionGroups.ListAutoPaging(ctx, user.TokenPermissionGroupListParams{})
	var out []PermissionGroup
	for iter.Next() {
		g := iter.Current()
		pg := PermissionGroup{ID: g.ID, Name: g.Name}
		for _, s := range g.Scopes {
			pg.Scopes = append(pg.Scopes, string(s))
		}
		out = append(out, pg)
	}
	if err := iter.Err(); err != nil {
		return nil, mapError(err)
	}
	return out, nil
}

// PermissionGroupIndex returns a scope → name → id lookup table. The outer
// key is one of the Scope* constants above.
//
// Indexing must be scope-aware because Cloudflare reuses display names
// across scopes: e.g. "Access: Apps and Policies Read" exists once
// account-scoped and once zone-scoped, with different IDs. A flat name → id
// map therefore cannot be built from the live list at all (the duplicate
// check would always fire). A group listing several scopes is indexed under
// each; a group with no scopes goes into the ScopeUnknown bucket. Duplicate
// names *within* one scope pointing at different IDs are still an error so
// a genuinely ambiguous spec surfaces instead of silently picking one.
func PermissionGroupIndex(groups []PermissionGroup) (map[string]map[string]string, error) {
	idx := make(map[string]map[string]string)
	add := func(scope string, g PermissionGroup) error {
		m := idx[scope]
		if m == nil {
			m = make(map[string]string)
			idx[scope] = m
		}
		if existing, ok := m[g.Name]; ok && existing != g.ID {
			return fmt.Errorf("cfapi: duplicate permission group name %q in scope %q (ids %s and %s)", g.Name, scope, existing, g.ID)
		}
		m[g.Name] = g.ID
		return nil
	}
	for _, g := range groups {
		if g.Name == "" {
			continue
		}
		if len(g.Scopes) == 0 {
			if err := add(ScopeUnknown, g); err != nil {
				return nil, err
			}
			continue
		}
		for _, s := range g.Scopes {
			if err := add(s, g); err != nil {
				return nil, err
			}
		}
	}
	return idx, nil
}
