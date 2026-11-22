// Package spec defines the on-disk YAML schema for cft tokens and provides
// parsing and validation.
package spec

// Policy scopes, as returned by Policy.Scope.
const (
	ScopeZone    = "zone"
	ScopeAccount = "account"
	ScopeUser    = "user"
)

// Policy effects accepted in Policy.Effect.
const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)

// nameMaxLen is the DNS-1123 label limit token names are held to.
const nameMaxLen = 63

// Token is one Cloudflare API token spec, the unit of `cft apply`.
type Token struct {
	Name     string   `json:"name"              yaml:"name"`
	Policies []Policy `json:"policies"          yaml:"policies"`
	Expires  string   `json:"expires,omitempty" yaml:"expires,omitempty"`
}

// Policy is one entry in a Token's policy list.
//
// Exactly one of Zone / Account / User must be set; this is enforced by
// Validate, not by the type system, because YAML cannot express "one of".
type Policy struct {
	Permissions []string `json:"permissions"       yaml:"permissions"`
	Zone        string   `json:"zone,omitempty"    yaml:"zone,omitempty"`
	Account     string   `json:"account,omitempty" yaml:"account,omitempty"`
	User        bool     `json:"user,omitempty"    yaml:"user,omitempty"`
	Effect      string   `json:"effect,omitempty"  yaml:"effect,omitempty"`
}

// Scope returns the resource scope name set on this policy: ScopeZone,
// ScopeAccount, ScopeUser, or empty if none is set.
func (p Policy) Scope() string {
	switch {
	case p.Zone != "":
		return ScopeZone
	case p.Account != "":
		return ScopeAccount
	case p.User:
		return ScopeUser
	}
	return ""
}

// NormalizedEffect returns Effect, defaulting to EffectAllow when unset.
func (p Policy) NormalizedEffect() string {
	if p.Effect == "" {
		return EffectAllow
	}
	return p.Effect
}
