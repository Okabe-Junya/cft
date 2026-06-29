package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/Okabe-Junya/cft/internal/cfapi"
	"github.com/Okabe-Junya/cft/internal/spec"
)

// Cloudflare reuses permission group names across scopes; the resolver must
// pick the ID matching the policy's scope, fall back to the unknown bucket
// for groups the API listed without scopes, and point at the available
// scope when the name exists only elsewhere.
func TestAPIResolver_ScopeAwarePermissionGroups(t *testing.T) {
	fc := &fakeClient{
		permissionGroups: []cfapi.PermissionGroup{
			{ID: "acct-id", Name: "Access: Apps and Policies Read", Scopes: []string{cfapi.ScopeAccount}},
			{ID: "zone-id", Name: "Access: Apps and Policies Read", Scopes: []string{cfapi.ScopeZone}},
			{ID: "dns-id", Name: "DNS Write", Scopes: []string{cfapi.ScopeZone}},
			{ID: "legacy-id", Name: "Legacy Group"}, // no scopes from the API
		},
	}
	r, err := newAPIResolver(context.Background(), fc)
	if err != nil {
		t.Fatalf("newAPIResolver: %v", err)
	}

	if id, err := r.PermissionGroupID("Access: Apps and Policies Read", spec.ScopeAccount); err != nil || id != "acct-id" {
		t.Errorf("account lookup = %q, %v; want acct-id", id, err)
	}
	if id, err := r.PermissionGroupID("Access: Apps and Policies Read", spec.ScopeZone); err != nil || id != "zone-id" {
		t.Errorf("zone lookup = %q, %v; want zone-id", id, err)
	}
	if id, err := r.PermissionGroupID("Legacy Group", spec.ScopeAccount); err != nil || id != "legacy-id" {
		t.Errorf("unknown-bucket fallback = %q, %v; want legacy-id", id, err)
	}

	// Zone-only group requested from an account policy: targeted error.
	if _, err := r.PermissionGroupID("DNS Write", spec.ScopeAccount); err == nil ||
		!strings.Contains(err.Error(), cfapi.ScopeZone) {
		t.Errorf("scope mismatch err = %v; want mention of %s", err, cfapi.ScopeZone)
	}
	// Truly unknown name keeps the spelling hint.
	if _, err := r.PermissionGroupID("No Such Group", spec.ScopeZone); err == nil ||
		!strings.Contains(err.Error(), "unknown permission group") {
		t.Errorf("unknown err = %v", err)
	}
}
