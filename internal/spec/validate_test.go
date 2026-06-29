package spec

import (
	"strings"
	"testing"
	"time"
)

func validToken() Token {
	return Token{
		Name: "ok",
		Policies: []Policy{
			{Permissions: []string{"DNS Write"}, Zone: "example.com"},
		},
	}
}

func TestValidate_Cases(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Token)
		wantErr string
	}{
		{
			name:    "missing name",
			mutate:  func(t *Token) { t.Name = "" },
			wantErr: "name is required",
		},
		{
			name:    "name too long",
			mutate:  func(t *Token) { t.Name = strings.Repeat("a", 64) },
			wantErr: "exceeds 63",
		},
		{
			name:    "name not dns-1123",
			mutate:  func(t *Token) { t.Name = "BAD_NAME" },
			wantErr: "DNS-1123",
		},
		{
			name:    "no policies",
			mutate:  func(t *Token) { t.Policies = nil },
			wantErr: "at least one policy",
		},
		{
			name:    "no scope",
			mutate:  func(t *Token) { t.Policies = []Policy{{Permissions: []string{"X"}}} },
			wantErr: "exactly one of zone",
		},
		{
			name:    "two scopes",
			mutate:  func(t *Token) { t.Policies = []Policy{{Permissions: []string{"X"}, Zone: "z", Account: "a"}} },
			wantErr: "exactly one",
		},
		{
			name:    "empty permissions slice",
			mutate:  func(t *Token) { t.Policies = []Policy{{Zone: "example.com"}} },
			wantErr: "permissions must not be empty",
		},
		{
			name:    "empty permission entry",
			mutate:  func(t *Token) { t.Policies[0].Permissions = []string{""} },
			wantErr: "permissions[0] is empty",
		},
		{
			name:    "invalid effect",
			mutate:  func(t *Token) { t.Policies[0].Effect = "weird" },
			wantErr: "effect must be",
		},
		{
			name:    "bad expires",
			mutate:  func(t *Token) { t.Expires = "tomorrow" },
			wantErr: "expires",
		},
		{
			name:    "valid date expires",
			mutate:  func(t *Token) { t.Expires = "2026-12-31" },
			wantErr: "",
		},
		{
			name:    "valid rfc3339 expires",
			mutate:  func(t *Token) { t.Expires = "2026-12-31T00:00:00Z" },
			wantErr: "",
		},
		{
			name:    "valid with deny effect",
			mutate:  func(t *Token) { t.Policies[0].Effect = "deny" },
			wantErr: "",
		},
		{
			name:    "valid user scope",
			mutate:  func(t *Token) { t.Policies = []Policy{{Permissions: []string{"X"}, User: true}} },
			wantErr: "",
		},
		{
			name:    "valid account scope",
			mutate:  func(t *Token) { t.Policies = []Policy{{Permissions: []string{"X"}, Account: "acc1"}} },
			wantErr: "",
		},
		{
			name:    "happy path",
			mutate:  func(t *Token) {},
			wantErr: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tok := validToken()
			c.mutate(&tok)
			err := tok.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("got err %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil, want error containing %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err %q does not contain %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestValidateAll_DuplicateName(t *testing.T) {
	a := validToken()
	a.Name = "dup"
	b := validToken()
	b.Name = "dup"
	err := ValidateAll([]Token{a, b})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("want duplicate error, got %v", err)
	}
}

func TestValidateAll_AllValid(t *testing.T) {
	a := validToken()
	a.Name = "a"
	b := validToken()
	b.Name = "b"
	if err := ValidateAll([]Token{a, b}); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestExpiresAt(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		tok := validToken()
		got, ok, err := tok.ExpiresAt()
		if err != nil || ok || !got.IsZero() {
			t.Errorf("ExpiresAt(empty) = %v, %v, %v", got, ok, err)
		}
	})
	t.Run("rfc3339", func(t *testing.T) {
		tok := validToken()
		tok.Expires = "2026-12-31T00:00:00Z"
		got, ok, err := tok.ExpiresAt()
		if err != nil || !ok {
			t.Fatalf("ExpiresAt: %v, ok=%v, err=%v", got, ok, err)
		}
		if want := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("date", func(t *testing.T) {
		tok := validToken()
		tok.Expires = "2026-12-31"
		got, ok, err := tok.ExpiresAt()
		if err != nil || !ok {
			t.Fatalf("ExpiresAt: %v, ok=%v, err=%v", got, ok, err)
		}
		if got.Year() != 2026 || got.Month() != time.December || got.Day() != 31 {
			t.Errorf("got %v", got)
		}
	})
}

func TestPolicy_Scope(t *testing.T) {
	cases := []struct {
		p    Policy
		want string
	}{
		{Policy{Zone: "z"}, ScopeZone},
		{Policy{Account: "a"}, ScopeAccount},
		{Policy{User: true}, ScopeUser},
		{Policy{}, ""},
	}
	for _, c := range cases {
		if got := c.p.Scope(); got != c.want {
			t.Errorf("Scope(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestPolicy_NormalizedEffect(t *testing.T) {
	if got := (Policy{}.NormalizedEffect()); got != EffectAllow {
		t.Errorf("default effect = %q, want %q", got, EffectAllow)
	}
	if got := (Policy{Effect: EffectDeny}.NormalizedEffect()); got != EffectDeny {
		t.Errorf("explicit deny = %q, want %q", got, EffectDeny)
	}
}
