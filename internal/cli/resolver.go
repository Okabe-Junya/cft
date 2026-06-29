package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/spec"
)

// apiResolver implements `resolver` against a live cfClient. It caches the
// permission-group list and zone lookups for the lifetime of one apply run
// so the same spec referencing "DNS Write" three times costs one API call.
type apiResolver struct {
	ctx    context.Context
	client cfClient
	groups map[string]map[string]string // cf scope → name → id
	zones  map[string]string            // name → id (only resolutions we have made)
}

func newAPIResolver(ctx context.Context, client cfClient) (*apiResolver, error) {
	pg, err := client.ListPermissionGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list permission groups: %w", err)
	}
	idx, err := cfapi.PermissionGroupIndex(pg)
	if err != nil {
		return nil, err
	}
	return &apiResolver{
		ctx:    ctx,
		client: client,
		groups: idx,
		zones:  map[string]string{},
	}, nil
}

// cfScopeFor maps a spec policy scope (spec.ScopeZone / ScopeAccount /
// ScopeUser) to the Cloudflare scope identifier used by the permission
// group index.
func cfScopeFor(specScope string) string {
	switch specScope {
	case spec.ScopeZone:
		return cfapi.ScopeZone
	case spec.ScopeAccount:
		return cfapi.ScopeAccount
	case spec.ScopeUser:
		return cfapi.ScopeUser
	}
	return cfapi.ScopeUnknown
}

// PermissionGroupID resolves a permission group name within the policy's
// scope. Cloudflare reuses names across scopes (account vs zone), so the
// scope decides which ID is meant. Groups the API listed without scopes sit
// in the ScopeUnknown bucket and resolve from any policy scope.
func (r *apiResolver) PermissionGroupID(name, specScope string) (string, error) {
	cfScope := cfScopeFor(specScope)
	if id, ok := r.groups[cfScope][name]; ok {
		return id, nil
	}
	if id, ok := r.groups[cfapi.ScopeUnknown][name]; ok {
		return id, nil
	}
	// Targeted error when the name exists, just not for this scope —
	// "unknown group" would send the user chasing typos that aren't there.
	var others []string
	for s, m := range r.groups {
		if s == cfScope || s == cfapi.ScopeUnknown {
			continue
		}
		if _, ok := m[name]; ok {
			others = append(others, s)
		}
	}
	if len(others) > 0 {
		sort.Strings(others)
		return "", fmt.Errorf("permission group %q is not available for %s-scoped policies (Cloudflare offers it for: %s)", name, specScope, strings.Join(others, ", "))
	}
	return "", fmt.Errorf("unknown permission group %q (check spelling against Cloudflare's list)", name)
}

func (r *apiResolver) ResolveZoneID(name string) (string, error) {
	if id, ok := r.zones[name]; ok {
		return id, nil
	}
	id, err := r.client.ResolveZoneID(r.ctx, name)
	if err != nil {
		return "", err
	}
	r.zones[name] = id
	return id, nil
}
