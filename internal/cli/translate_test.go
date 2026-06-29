package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/spec"
)

// stubResolver implements resolver with in-memory maps and a tally so tests
// can assert which lookups happened. Lookups are name-keyed regardless of
// scope; the scopes actually passed are recorded in groupScopes so tests can
// assert translatePolicy forwards the policy scope.
type stubResolver struct {
	groups                map[string]string
	zones                 map[string]string
	groupCalls, zoneCalls int
	groupScopes           []string
}

func (s *stubResolver) PermissionGroupID(name, specScope string) (string, error) {
	s.groupCalls++
	s.groupScopes = append(s.groupScopes, specScope)
	id, ok := s.groups[name]
	if !ok {
		return "", errors.New("group not found")
	}
	return id, nil
}

func (s *stubResolver) ResolveZoneID(name string) (string, error) {
	s.zoneCalls++
	id, ok := s.zones[name]
	if !ok {
		return "", errors.New("zone not found")
	}
	return id, nil
}

func TestTranslateToken_ZoneByName(t *testing.T) {
	r := &stubResolver{
		groups: map[string]string{"DNS Write": "pg-1"},
		zones:  map[string]string{"example.com": "zone-id-123"},
	}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"DNS Write"}, Zone: "example.com"},
		},
	}
	got, err := translateToken(tok, r)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.Name != "n" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.Policies) != 1 {
		t.Fatalf("policies len = %d", len(got.Policies))
	}
	p := got.Policies[0]
	if p.Effect != "allow" {
		t.Errorf("Effect = %q, want allow (default)", p.Effect)
	}
	if want := "com.cloudflare.api.account.zone.zone-id-123"; p.Resources[want] != "*" {
		t.Errorf("resources = %v, want key %q", p.Resources, want)
	}
	if len(p.PermissionGroups) != 1 || p.PermissionGroups[0].ID != "pg-1" {
		t.Errorf("PermissionGroups = %+v", p.PermissionGroups)
	}
}

func TestTranslateToken_ZoneByID_SkipsResolution(t *testing.T) {
	zoneID := strings.Repeat("a", 32)
	r := &stubResolver{
		groups: map[string]string{"DNS Read": "pg-2"},
		zones:  map[string]string{}, // empty — must not be called
	}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"DNS Read"}, Zone: zoneID},
		},
	}
	got, err := translateToken(tok, r)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if r.zoneCalls != 0 {
		t.Errorf("ResolveZoneID called %d times; ID-like string should bypass", r.zoneCalls)
	}
	if want := "com.cloudflare.api.account.zone." + zoneID; got.Policies[0].Resources[want] != "*" {
		t.Errorf("resources = %v", got.Policies[0].Resources)
	}
}

func TestTranslateToken_AccountByID(t *testing.T) {
	accID := strings.Repeat("b", 32)
	r := &stubResolver{groups: map[string]string{"X": "id1"}}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"X"}, Account: accID, Effect: spec.EffectDeny},
		},
	}
	got, err := translateToken(tok, r)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if got.Policies[0].Effect != "deny" {
		t.Errorf("Effect = %q", got.Policies[0].Effect)
	}
	if want := "com.cloudflare.api.account." + accID; got.Policies[0].Resources[want] != "*" {
		t.Errorf("resources = %v", got.Policies[0].Resources)
	}
}

func TestTranslateToken_AccountByName_Rejected(t *testing.T) {
	r := &stubResolver{groups: map[string]string{"X": "id1"}}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"X"}, Account: "My Account"},
		},
	}
	_, err := translateToken(tok, r)
	if err == nil || !strings.Contains(err.Error(), "account ID") {
		t.Errorf("err = %v", err)
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitSpec {
		t.Errorf("exit code = %v, want ExitSpec", err)
	}
}

func TestTranslateToken_UserScope_NotSupported(t *testing.T) {
	r := &stubResolver{groups: map[string]string{"X": "id1"}}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"X"}, User: true},
		},
	}
	_, err := translateToken(tok, r)
	if err == nil || !strings.Contains(err.Error(), "user scope") {
		t.Errorf("err = %v", err)
	}
}

func TestTranslateToken_UnknownPermission(t *testing.T) {
	r := &stubResolver{
		groups: map[string]string{}, // nothing
		zones:  map[string]string{"e.com": "z1"},
	}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"Typo Group"}, Zone: "e.com"},
		},
	}
	_, err := translateToken(tok, r)
	if err == nil {
		t.Fatal("expected error")
	}
	var ec exitCoder
	if !errors.As(err, &ec) || ec.ExitCode() != ExitSpec {
		t.Errorf("err = %v, want ExitSpec", err)
	}
}

func TestTranslateToken_NormalisesExpires(t *testing.T) {
	r := &stubResolver{
		groups: map[string]string{"X": "id1"},
		zones:  map[string]string{"e.com": "z1"},
	}
	cases := []struct {
		in   string
		want string
	}{
		{"2026-12-31", "2026-12-31T00:00:00Z"},
		{"2026-12-31T15:00:00Z", "2026-12-31T15:00:00Z"},
		{"2026-12-31T15:00:00+09:00", "2026-12-31T06:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			tok := spec.Token{
				Name:    "n",
				Expires: c.in,
				Policies: []spec.Policy{
					{Permissions: []string{"X"}, Zone: "e.com"},
				},
			}
			got, err := translateToken(tok, r)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if got.ExpiresOn != c.want {
				t.Errorf("ExpiresOn = %q, want %q", got.ExpiresOn, c.want)
			}
		})
	}
}

// Ensure the cfapi spec type carries through to a representation we can
// JSON-marshal without an interface{} blow-up.
func TestTranslateToken_OutputIsCfapiTokenSpec(t *testing.T) {
	r := &stubResolver{
		groups: map[string]string{"X": "id1"},
		zones:  map[string]string{"e.com": "z1"},
	}
	tok := spec.Token{
		Name: "n",
		Policies: []spec.Policy{
			{Permissions: []string{"X"}, Zone: "e.com"},
		},
	}
	got, err := translateToken(tok, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}
