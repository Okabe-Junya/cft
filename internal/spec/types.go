// Package spec defines the on-disk YAML schema for cft tokens and provides
// parsing and validation. See docs/design.md §4.
package spec

const (
	ScopeZone    = "zone"
	ScopeAccount = "account"
	ScopeUser    = "user"

	EffectAllow = "allow"
	EffectDeny  = "deny"

	nameMaxLen = 63
)

// Token is one Cloudflare API token spec, the unit of `cft apply`.
type Token struct {
	Name     string   `yaml:"name" json:"name"`
	Policies []Policy `yaml:"policies" json:"policies"`
	Expires  string   `yaml:"expires,omitempty" json:"expires,omitempty"`
}

// Policy is one entry in a Token's policy list.
//
// Exactly one of Zone / Account / User must be set; this is enforced by
// Validate, not by the type system, because YAML cannot express "one of".
type Policy struct {
	Permissions []string `yaml:"permissions" json:"permissions"`
	Zone        string   `yaml:"zone,omitempty" json:"zone,omitempty"`
	Account     string   `yaml:"account,omitempty" json:"account,omitempty"`
	User        bool     `yaml:"user,omitempty" json:"user,omitempty"`
	Effect      string   `yaml:"effect,omitempty" json:"effect,omitempty"`
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
