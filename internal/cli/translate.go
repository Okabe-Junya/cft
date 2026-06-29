package cli

import (
	"fmt"
	"regexp"
	"time"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/spec"
)

// idLike matches Cloudflare's 32-hex-char IDs. When a spec field could be
// either a name (zone "example.com") or an ID, we use this to skip a needless
// name-resolution API call.
var idLike = regexp.MustCompile(`^[a-f0-9]{32}$`)

// resolver collects the API calls translateToken needs. The apply command
// implements it via *cfapi.Client; tests use a fake. PermissionGroupID takes
// the policy's spec scope (spec.ScopeZone / ScopeAccount / ScopeUser) because
// Cloudflare reuses group names across scopes with different IDs.
type resolver interface {
	ResolveZoneID(name string) (string, error)
	PermissionGroupID(name, specScope string) (string, error)
}

// translateToken converts a validated spec.Token into the request body the
// Cloudflare token API expects. Returns ExitSpec-coded errors so the user
// sees a 2 exit on bad specs.
func translateToken(t spec.Token, r resolver) (cfapi.TokenSpec, error) {
	out := cfapi.TokenSpec{Name: t.Name}
	if t.Expires != "" {
		exp, err := normaliseExpires(t.Expires)
		if err != nil {
			return cfapi.TokenSpec{}, withExit(ExitSpec, fmt.Errorf("token %q: expires: %w", t.Name, err))
		}
		out.ExpiresOn = exp
	}
	for pi, p := range t.Policies {
		cfp, err := translatePolicy(p, r)
		if err != nil {
			return cfapi.TokenSpec{}, withExit(ExitSpec, fmt.Errorf("token %q: policies[%d]: %w", t.Name, pi, err))
		}
		out.Policies = append(out.Policies, cfp)
	}
	return out, nil
}

func translatePolicy(p spec.Policy, r resolver) (cfapi.TokenPolicy, error) {
	groups := make([]cfapi.PermissionGroup, 0, len(p.Permissions))
	for _, name := range p.Permissions {
		id, err := r.PermissionGroupID(name, p.Scope())
		if err != nil {
			return cfapi.TokenPolicy{}, err
		}
		groups = append(groups, cfapi.PermissionGroup{ID: id})
	}

	res, err := resolveScope(p, r)
	if err != nil {
		return cfapi.TokenPolicy{}, err
	}
	return cfapi.TokenPolicy{
		Effect:           p.NormalizedEffect(),
		Resources:        res,
		PermissionGroups: groups,
	}, nil
}

func resolveScope(p spec.Policy, r resolver) (map[string]string, error) {
	switch p.Scope() {
	case spec.ScopeZone:
		id := p.Zone
		if !idLike.MatchString(id) {
			resolved, err := r.ResolveZoneID(p.Zone)
			if err != nil {
				return nil, err
			}
			id = resolved
		}
		return map[string]string{"com.cloudflare.api.account.zone." + id: "*"}, nil

	case spec.ScopeAccount:
		// v1: require account ID. Account-name resolution would need an
		// additional API call and the dashboard already shows the ID — keep
		// it explicit until users actually ask.
		if !idLike.MatchString(p.Account) {
			return nil, fmt.Errorf("account scope requires the 32-char account ID, got %q", p.Account)
		}
		return map[string]string{"com.cloudflare.api.account." + p.Account: "*"}, nil

	case spec.ScopeUser:
		// v1: not yet supported because resolving the current user requires
		// an extra API call we have not wired up. Surface a precise error so
		// the user knows what we did not do, not just "invalid spec".
		return nil, fmt.Errorf("user scope is not supported in v1; track follow-up if you need it")

	default:
		return nil, fmt.Errorf("policy has no scope (validator should have caught this)")
	}
}

// normaliseExpires accepts the formats the spec validator allows and emits
// RFC3339 (which is what Cloudflare's expires_on field expects). A bare date
// is treated as midnight UTC.
func normaliseExpires(s string) (string, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("invalid expires %q: want RFC3339 or YYYY-MM-DD", s)
}
