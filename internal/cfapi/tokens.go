package cfapi

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/shared"
	"github.com/cloudflare/cloudflare-go/v7/user"
)

// TokenPolicy is the policy shape cli/translate.go builds. The SDK uses a
// `param.Field`-wrapped equivalent; the conversion happens at the API
// boundary so callers stay free of generic Field types.
type TokenPolicy struct {
	Effect           string            // "allow" | "deny"
	Resources        map[string]string // e.g. "com.cloudflare.api.account.zone.<id>": "*"
	PermissionGroups []PermissionGroup // by ID; Name optional
}

// TokenSpec is the request body for CreateToken / UpdateToken.
type TokenSpec struct {
	Name      string
	Policies  []TokenPolicy
	ExpiresOn string // RFC3339, optional
}

// Token is the trimmed Cloudflare-side token record cft cares about.
type Token struct {
	ID        string
	Name      string
	Status    string
	ExpiresOn string // RFC3339, empty when unset
}

// CreatedToken is what CreateToken returns: the persisted Token plus its
// freshly-generated value (only ever returned at create time).
type CreatedToken struct {
	Token
	Value string
}

// RolledToken is the response from rolling a token's value.
type RolledToken struct {
	Value string
}

// Verify confirms the configured bearer token is active.
func (c *Client) Verify(ctx context.Context) (*Token, error) {
	resp, err := c.cf.User.Tokens.Verify(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return &Token{
		ID:        resp.ID,
		Status:    string(resp.Status),
		ExpiresOn: rfc3339OrEmpty(resp.ExpiresOn),
	}, nil
}

// CreateToken issues a new API token and returns its value.
func (c *Client) CreateToken(ctx context.Context, s TokenSpec) (*CreatedToken, error) {
	params, err := buildNewParams(s)
	if err != nil {
		return nil, err
	}
	resp, err := c.cf.User.Tokens.New(ctx, params)
	if err != nil {
		return nil, mapError(err)
	}
	return &CreatedToken{
		Token: Token{
			ID:        resp.ID,
			Name:      resp.Name,
			Status:    string(resp.Status),
			ExpiresOn: rfc3339OrEmpty(resp.ExpiresOn),
		},
		Value: string(resp.Value),
	}, nil
}

// UpdateToken replaces the policy of an existing token. The token value is
// not changed; use RollToken for that.
func (c *Client) UpdateToken(ctx context.Context, id string, s TokenSpec) (*Token, error) {
	params, err := buildUpdateParams(s)
	if err != nil {
		return nil, err
	}
	resp, err := c.cf.User.Tokens.Update(ctx, id, params)
	if err != nil {
		return nil, mapError(err)
	}
	return &Token{
		ID:        resp.ID,
		Name:      resp.Name,
		Status:    string(resp.Status),
		ExpiresOn: rfc3339OrEmpty(resp.ExpiresOn),
	}, nil
}

// RollToken re-issues the token value and returns the new value.
func (c *Client) RollToken(ctx context.Context, id string) (string, error) {
	// The Cloudflare endpoint takes no body; the SDK still requires the
	// Body field on TokenValueUpdateParams, so an empty object is the
	// canonical way to issue the call.
	v, err := c.cf.User.Tokens.Value.Update(ctx, id, user.TokenValueUpdateParams{
		Body: struct{}{},
	})
	if err != nil {
		return "", mapError(err)
	}
	if v == nil {
		return "", fmt.Errorf("cfapi: roll token returned empty value")
	}
	return string(*v), nil
}

// ListTokens returns all tokens owned by the authenticated user. Uses
// auto-pagination so a user with many tokens does not silently get only the
// first page.
func (c *Client) ListTokens(ctx context.Context) ([]Token, error) {
	iter := c.cf.User.Tokens.ListAutoPaging(ctx, user.TokenListParams{})
	var out []Token
	for iter.Next() {
		t := iter.Current()
		out = append(out, Token{
			ID:        t.ID,
			Name:      t.Name,
			Status:    string(t.Status),
			ExpiresOn: rfc3339OrEmpty(t.ExpiresOn),
		})
	}
	if err := iter.Err(); err != nil {
		return nil, mapError(err)
	}
	return out, nil
}

// GetToken fetches a single token by ID.
func (c *Client) GetToken(ctx context.Context, id string) (*Token, error) {
	resp, err := c.cf.User.Tokens.Get(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return &Token{
		ID:        resp.ID,
		Name:      resp.Name,
		Status:    string(resp.Status),
		ExpiresOn: rfc3339OrEmpty(resp.ExpiresOn),
	}, nil
}

// DeleteToken removes a token by ID. A 404 is returned as *Error with
// NotFound() == true so callers can treat it as already gone.
func (c *Client) DeleteToken(ctx context.Context, id string) error {
	_, err := c.cf.User.Tokens.Delete(ctx, id)
	return mapError(err)
}

// buildNewParams translates a TokenSpec into the SDK's request shape.
func buildNewParams(s TokenSpec) (user.TokenNewParams, error) {
	policies, err := translatePolicies(s.Policies)
	if err != nil {
		return user.TokenNewParams{}, err
	}
	p := user.TokenNewParams{
		Name:     cloudflare.F(s.Name),
		Policies: cloudflare.F(policies),
	}
	if s.ExpiresOn != "" {
		t, err := time.Parse(time.RFC3339, s.ExpiresOn)
		if err != nil {
			return user.TokenNewParams{}, fmt.Errorf("cfapi: invalid expires_on %q: %w", s.ExpiresOn, err)
		}
		p.ExpiresOn = cloudflare.F(t)
	}
	return p, nil
}

// buildUpdateParams is the Update twin of buildNewParams. The SDK requires
// the spec wrapped in a TokenParam.
func buildUpdateParams(s TokenSpec) (user.TokenUpdateParams, error) {
	policies, err := translatePolicies(s.Policies)
	if err != nil {
		return user.TokenUpdateParams{}, err
	}
	tp := shared.TokenParam{
		Name:     cloudflare.F(s.Name),
		Policies: cloudflare.F(policies),
	}
	if s.ExpiresOn != "" {
		t, err := time.Parse(time.RFC3339, s.ExpiresOn)
		if err != nil {
			return user.TokenUpdateParams{}, fmt.Errorf("cfapi: invalid expires_on %q: %w", s.ExpiresOn, err)
		}
		tp.ExpiresOn = cloudflare.F(t)
	}
	return user.TokenUpdateParams{Token: tp}, nil
}

func translatePolicies(in []TokenPolicy) ([]shared.TokenPolicyParam, error) {
	out := make([]shared.TokenPolicyParam, 0, len(in))
	for i, p := range in {
		if p.Effect == "" {
			return nil, fmt.Errorf("cfapi: policies[%d]: effect is required", i)
		}
		groups := make([]shared.TokenPolicyPermissionGroupParam, 0, len(p.PermissionGroups))
		for _, g := range p.PermissionGroups {
			groups = append(groups, shared.TokenPolicyPermissionGroupParam{
				ID: cloudflare.F(g.ID),
			})
		}
		// Cloudflare's policy `resources` is a discriminated union; for cft
		// we always emit the flat "scope-string → '*'" form (e.g.
		// "com.cloudflare.api.account.zone.<id>": "*"). The matching SDK
		// type is TokenPolicyResourcesIAMResourcesTypeObjectStringParam.
		resources := shared.TokenPolicyResourcesIAMResourcesTypeObjectStringParam(p.Resources)
		out = append(out, shared.TokenPolicyParam{
			Effect:           cloudflare.F(shared.TokenPolicyEffect(p.Effect)),
			PermissionGroups: cloudflare.F(groups),
			Resources:        cloudflare.F[shared.TokenPolicyResourcesUnionParam](resources),
		})
	}
	return out, nil
}

func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
