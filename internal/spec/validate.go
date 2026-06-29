package spec

import (
	"fmt"
	"regexp"
	"time"
)

// DNS-1123 label: lowercase alphanumeric or '-', starts/ends alphanumeric.
var nameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Validate checks t for semantic correctness.
func (t Token) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("name is required")
	}
	if len(t.Name) > nameMaxLen {
		return fmt.Errorf("name %q exceeds %d characters", t.Name, nameMaxLen)
	}
	if !nameRe.MatchString(t.Name) {
		return fmt.Errorf("name %q must match DNS-1123 (lowercase alphanumeric or '-', starts/ends alphanumeric)", t.Name)
	}
	if len(t.Policies) == 0 {
		return fmt.Errorf("at least one policy is required")
	}
	for i, p := range t.Policies {
		if err := p.validate(); err != nil {
			return fmt.Errorf("policies[%d]: %w", i, err)
		}
	}
	if t.Expires != "" {
		if _, err := parseExpires(t.Expires); err != nil {
			return fmt.Errorf("expires: %w", err)
		}
	}
	return nil
}

func (p Policy) validate() error {
	if len(p.Permissions) == 0 {
		return fmt.Errorf("permissions must not be empty")
	}
	for i, perm := range p.Permissions {
		if perm == "" {
			return fmt.Errorf("permissions[%d] is empty", i)
		}
	}
	scopes := 0
	if p.Zone != "" {
		scopes++
	}
	if p.Account != "" {
		scopes++
	}
	if p.User {
		scopes++
	}
	switch scopes {
	case 0:
		return fmt.Errorf("must specify exactly one of zone, account, user")
	case 1:
	default:
		return fmt.Errorf("must specify exactly one of zone, account, user (got %d)", scopes)
	}
	switch p.Effect {
	case "", EffectAllow, EffectDeny:
	default:
		return fmt.Errorf("effect must be %q or %q, got %q", EffectAllow, EffectDeny, p.Effect)
	}
	return nil
}

// ValidateAll validates each token and ensures Names are unique across the set.
func ValidateAll(tokens []Token) error {
	seen := make(map[string]int, len(tokens))
	for i, t := range tokens {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("tokens[%d] (%q): %w", i, t.Name, err)
		}
		if prev, ok := seen[t.Name]; ok {
			return fmt.Errorf("duplicate name %q at tokens[%d] (previously at tokens[%d])", t.Name, i, prev)
		}
		seen[t.Name] = i
	}
	return nil
}

// ExpiresAt returns the parsed expiry. The second return is false when Expires
// is empty.
func (t Token) ExpiresAt() (time.Time, bool, error) {
	if t.Expires == "" {
		return time.Time{}, false, nil
	}
	parsed, err := parseExpires(t.Expires)
	if err != nil {
		return time.Time{}, false, err
	}
	return parsed, true, nil
}

func parseExpires(s string) (time.Time, error) {
	formats := []string{time.RFC3339, time.RFC3339Nano, "2006-01-02"}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid %q: want RFC3339 or YYYY-MM-DD", s)
}
