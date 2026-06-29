package cfapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// newTestClient stands a Client up against an httptest.Server. We disable
// retries here so failure-mode tests stay fast; retry-specific tests
// override this explicitly.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New("test-token", WithBaseURL(srv.URL), WithMaxRetries(0))
}

// writeEnvelope emits Cloudflare's standard JSON wrapper so the SDK's
// generic decoder accepts the response.
func writeEnvelope(t *testing.T, w http.ResponseWriter, status int, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := struct {
		Success bool `json:"success"`
		Result  any  `json:"result"`
	}{
		Success: status >= 200 && status < 300,
		Result:  result,
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatal(err)
	}
}

// writePagedEnvelope serves a single page of a V4PagePaginationArray
// response. The SDK's auto-pager keeps calling with page=N until Result is
// empty, so callers MUST switch on the requested page to terminate the
// loop — otherwise the test hangs.
func writePagedEnvelope(t *testing.T, w http.ResponseWriter, result any, page, perPage int) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	body := struct {
		Success    bool `json:"success"`
		Result     any  `json:"result"`
		ResultInfo struct {
			Page    int `json:"page"`
			PerPage int `json:"per_page"`
			Count   int `json:"count"`
		} `json:"result_info"`
	}{
		Success: true,
		Result:  result,
	}
	body.ResultInfo.Page = page
	body.ResultInfo.PerPage = perPage
	if items, ok := result.([]map[string]string); ok {
		body.ResultInfo.Count = len(items)
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Fatal(err)
	}
}

// pageFromQuery reads the SDK-supplied `page` query parameter, defaulting
// to 1 when absent (which matches the SDK's first call).
func pageFromQuery(r *http.Request) int {
	s := r.URL.Query().Get("page")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func TestClient_BearerHeaderAndContentType(t *testing.T) {
	var seenAuth, seenContentType string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenContentType = r.Header.Get("Content-Type")
		writeEnvelope(t, w, 200, map[string]any{
			"id":     "x",
			"name":   "n",
			"status": "active",
		})
	})
	if _, err := c.CreateToken(context.Background(), TokenSpec{
		Name:     "n",
		Policies: []TokenPolicy{{Effect: "allow"}},
	}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if seenAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q", seenAuth)
	}
	if seenContentType != "application/json" {
		t.Errorf("Content-Type = %q", seenContentType)
	}
}

// The SDK's NewClient inherits CLOUDFLARE_* auth/base-URL settings from the
// environment (DefaultClientOptions). New must neutralise them: legacy
// X-Auth-* headers must not ride along with the Bearer token, the bearer must
// stay the explicitly-passed token, and CLOUDFLARE_BASE_URL must not redirect
// requests (the test's WithBaseURL is applied after New's pin, mirroring how
// it overrides for tests while env vars cannot).
func TestNew_IgnoresEnvAuthAndBaseURL(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "env-token")
	t.Setenv("CLOUDFLARE_API_KEY", "env-key")
	t.Setenv("CLOUDFLARE_EMAIL", "env@example.com")
	t.Setenv("CLOUDFLARE_API_USER_SERVICE_KEY", "env-service-key")
	t.Setenv("CLOUDFLARE_BASE_URL", "https://attacker.invalid")

	var seen http.Header
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		writeEnvelope(t, w, 200, map[string]any{"id": "abc", "status": "active"})
	})
	if _, err := c.Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := seen.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want explicit token to win over env", got)
	}
	for _, h := range []string{"X-Auth-Key", "X-Auth-Email", "X-Auth-User-Service-Key"} {
		if got := seen.Get(h); got != "" {
			t.Errorf("%s = %q, want header suppressed", h, got)
		}
	}
}

func TestVerify_Success(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/user/tokens/verify" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, 200, map[string]any{
			"id":     "abc",
			"status": "active",
		})
	})
	tok, err := c.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.ID != "abc" || tok.Status != "active" {
		t.Errorf("got %+v", tok)
	}
}

func TestCreateToken_TranslatesPolicies(t *testing.T) {
	type body struct {
		Name     string `json:"name"`
		Policies []struct {
			Effect           string            `json:"effect"`
			Resources        map[string]string `json:"resources"`
			PermissionGroups []struct {
				ID string `json:"id"`
			} `json:"permission_groups"`
		} `json:"policies"`
		ExpiresOn string `json:"expires_on,omitempty"`
	}
	var got body
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/user/tokens" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode body: %v\n%s", err, raw)
		}
		writeEnvelope(t, w, 200, map[string]any{
			"id":     "id1",
			"name":   "n",
			"status": "active",
			"value":  "secret",
		})
	})
	resp, err := c.CreateToken(context.Background(), TokenSpec{
		Name: "n",
		Policies: []TokenPolicy{{
			Effect:    "allow",
			Resources: map[string]string{"com.cloudflare.api.account.zone.z1": "*"},
			PermissionGroups: []PermissionGroup{
				{ID: "pg-1"}, {ID: "pg-2"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if resp.Value != "secret" || resp.ID != "id1" {
		t.Errorf("CreatedToken = %+v", resp)
	}
	if got.Name != "n" || len(got.Policies) != 1 {
		t.Fatalf("server received: %+v", got)
	}
	p := got.Policies[0]
	if p.Effect != "allow" {
		t.Errorf("effect = %q", p.Effect)
	}
	if p.Resources["com.cloudflare.api.account.zone.z1"] != "*" {
		t.Errorf("resources = %+v", p.Resources)
	}
	if len(p.PermissionGroups) != 2 || p.PermissionGroups[0].ID != "pg-1" {
		t.Errorf("permission groups = %+v", p.PermissionGroups)
	}
}

func TestUpdateToken_PUT(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/user/tokens/the-id" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, 200, map[string]any{
			"id":     "the-id",
			"name":   "n2",
			"status": "active",
		})
	})
	got, err := c.UpdateToken(context.Background(), "the-id", TokenSpec{
		Name:     "n2",
		Policies: []TokenPolicy{{Effect: "allow"}},
	})
	if err != nil {
		t.Fatalf("UpdateToken: %v", err)
	}
	if got.Name != "n2" {
		t.Errorf("got %+v", got)
	}
}

func TestRollToken_ReturnsString(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/user/tokens/abc/value" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		writeEnvelope(t, w, 200, "new-secret-value")
	})
	got, err := c.RollToken(context.Background(), "abc")
	if err != nil {
		t.Fatalf("RollToken: %v", err)
	}
	if got != "new-secret-value" {
		t.Errorf("got %q", got)
	}
}

func TestDeleteToken_NotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"success":false,"errors":[{"code":1001,"message":"not found"}],"result":null}`)
	})
	err := c.DeleteToken(context.Background(), "missing")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if !apiErr.NotFound() {
		t.Errorf("NotFound() = false, want true")
	}
	if !apiErr.HasCode(1001) {
		t.Errorf("HasCode(1001) = false, codes=%v", apiErr.Codes)
	}
}

func TestError_MessageFallback(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		// Non-JSON body, e.g. an upstream gateway error. The SDK still
		// surfaces this as *cloudflare.Error; our mapError fills Message
		// with the SDK's stringified error so the user sees the status.
		w.WriteHeader(502)
		_, _ = io.WriteString(w, "Bad Gateway")
	})
	_, err := c.Verify(context.Background())
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if e.Status != 502 || (e.Message == "" && len(e.Messages) == 0) {
		t.Errorf("got %+v / %q", e, e.Error())
	}
}

// TestListPermissionGroups_ReturnsResults verifies the basic happy path.
// cloudflare-go/v7 models /user/tokens/permission_groups with the SDK's
// SinglePage iterator, which makes exactly one request to the endpoint and
// returns whatever the server responds with — Cloudflare's permission
// group list is small enough (~150 entries) that the API returns it in one
// shot. If Cloudflare ever paginates this endpoint and the SDK keeps
// SinglePage, we'd surface the same bug we had on the hand-rolled client
// and would need to file an upstream issue; this test would still pass.
func TestListPermissionGroups_ReturnsResults(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/tokens/permission_groups" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeEnvelope(t, w, 200, []map[string]any{
			{"id": "id-a", "name": "DNS Write", "scopes": []string{ScopeZone}},
			{"id": "id-b", "name": "Zone Read"},
		})
	})
	groups, err := c.ListPermissionGroups(context.Background())
	if err != nil {
		t.Fatalf("ListPermissionGroups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(groups), groups)
	}
	if len(groups[0].Scopes) != 1 || groups[0].Scopes[0] != ScopeZone {
		t.Errorf("scopes not carried through: %+v", groups[0])
	}
	idx, err := PermissionGroupIndex(groups)
	if err != nil {
		t.Fatalf("PermissionGroupIndex: %v", err)
	}
	if idx[ScopeZone]["DNS Write"] != "id-a" {
		t.Errorf("idx[zone] = %v", idx[ScopeZone])
	}
	// No scopes in the API response → resolvable via the unknown bucket.
	if idx[ScopeUnknown]["Zone Read"] != "id-b" {
		t.Errorf("idx[unknown] = %v", idx[ScopeUnknown])
	}
}

// Cloudflare reuses display names across scopes (the account- and
// zone-scoped "Access: Apps and Policies Read" groups have different IDs).
// The index must keep both resolvable instead of failing on the duplicate.
func TestPermissionGroupIndex_SameNameAcrossScopesIsOK(t *testing.T) {
	idx, err := PermissionGroupIndex([]PermissionGroup{
		{ID: "acct-id", Name: "Access: Apps and Policies Read", Scopes: []string{ScopeAccount}},
		{ID: "zone-id", Name: "Access: Apps and Policies Read", Scopes: []string{ScopeZone}},
	})
	if err != nil {
		t.Fatalf("PermissionGroupIndex: %v", err)
	}
	if idx[ScopeAccount]["Access: Apps and Policies Read"] != "acct-id" {
		t.Errorf("account idx = %v", idx[ScopeAccount])
	}
	if idx[ScopeZone]["Access: Apps and Policies Read"] != "zone-id" {
		t.Errorf("zone idx = %v", idx[ScopeZone])
	}
}

func TestPermissionGroupIndex_DuplicateNameError(t *testing.T) {
	// Same name + same scope + different IDs is genuinely ambiguous.
	_, err := PermissionGroupIndex([]PermissionGroup{
		{ID: "1", Name: "X", Scopes: []string{ScopeAccount}},
		{ID: "2", Name: "X", Scopes: []string{ScopeAccount}},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("err = %v", err)
	}
}

func TestPermissionGroupIndex_SameIDDuplicateIsOK(t *testing.T) {
	// Cloudflare can occasionally return the same entry twice in the list.
	idx, err := PermissionGroupIndex([]PermissionGroup{
		{ID: "1", Name: "X"},
		{ID: "1", Name: "X"},
	})
	if err != nil || idx[ScopeUnknown]["X"] != "1" {
		t.Errorf("idx=%v err=%v", idx, err)
	}
}

// TestResolveZoneID exercises the V4PagePaginationArray pager — the SDK
// keeps incrementing `page` until the server returns an empty result, so
// the fixture MUST serve page 2 as empty or the test hangs.
func TestResolveZoneID_ExactMatch(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("name"); got != "example.com" {
			t.Errorf("name query = %q", got)
		}
		switch pageFromQuery(r) {
		case 1:
			writePagedEnvelope(t, w, []map[string]string{
				{"id": "z1", "name": "example.com"},
			}, 1, 50)
		default:
			writePagedEnvelope(t, w, []map[string]string{}, 2, 50)
		}
	})
	id, err := c.ResolveZoneID(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ResolveZoneID: %v", err)
	}
	if id != "z1" {
		t.Errorf("id = %q", id)
	}
}

func TestResolveZoneID_NoMatch(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writePagedEnvelope(t, w, []map[string]string{}, 1, 50)
	})
	_, err := c.ResolveZoneID(context.Background(), "absent.example")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

// TestResolveZoneID_PostFiltersToExactName verifies our defence against
// Cloudflare's `name` query occasionally widening to substring matches:
// we post-filter the SDK iterator results to entries whose Name matches
// exactly.
func TestResolveZoneID_PostFiltersToExactName(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch pageFromQuery(r) {
		case 1:
			writePagedEnvelope(t, w, []map[string]string{
				{"id": "z-near", "name": "near-example.com"},
				{"id": "z-exact", "name": "example.com"},
			}, 1, 50)
		default:
			writePagedEnvelope(t, w, []map[string]string{}, 2, 50)
		}
	})
	id, err := c.ResolveZoneID(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ResolveZoneID: %v", err)
	}
	if id != "z-exact" {
		t.Errorf("id = %q, want z-exact", id)
	}
}

func TestResolveZoneID_MultipleExactErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch pageFromQuery(r) {
		case 1:
			writePagedEnvelope(t, w, []map[string]string{
				{"id": "1", "name": "x.example"},
				{"id": "2", "name": "x.example"},
			}, 1, 50)
		default:
			writePagedEnvelope(t, w, []map[string]string{}, 2, 50)
		}
	})
	_, err := c.ResolveZoneID(context.Background(), "x.example")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("err = %v", err)
	}
}

// TestListTokens_DrainsPages exercises V4 auto-pagination on the tokens
// endpoint. This is the pagination correctness guarantee we got for free
// by moving to the SDK — the hand-rolled client only ever read page 1.
func TestListTokens_DrainsPages(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		switch pageFromQuery(r) {
		case 1:
			writePagedEnvelope(t, w, []map[string]string{
				{"id": "t1", "name": "alpha", "status": "active"},
				{"id": "t2", "name": "bravo", "status": "active"},
			}, 1, 2)
		case 2:
			writePagedEnvelope(t, w, []map[string]string{
				{"id": "t3", "name": "charlie", "status": "active"},
			}, 2, 2)
		default:
			writePagedEnvelope(t, w, []map[string]string{}, 3, 2)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("tok", WithBaseURL(srv.URL), WithMaxRetries(0))

	tokens, err := c.ListTokens(context.Background())
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(tokens) != 3 {
		t.Errorf("got %d tokens across pages, want 3", len(tokens))
	}
	if calls < 2 {
		t.Errorf("expected at least 2 page requests, got %d", calls)
	}
}

func TestError_Format(t *testing.T) {
	e := &Error{Status: 400, Codes: []int{1, 2}, Messages: []string{"a", "b"}}
	s := e.Error()
	if !strings.Contains(s, "400") || !strings.Contains(s, "1 a") || !strings.Contains(s, "2 b") {
		t.Errorf("Error() = %q", s)
	}
}

// TestWithHTTPClient_FlowsThrough verifies the option plumbs through to
// the SDK. The fake transport rejects everything; we only care that the
// rejection surfaces.
func TestWithHTTPClient_FlowsThrough(t *testing.T) {
	c := New("tok",
		WithBaseURL("http://unused.example"),
		WithMaxRetries(0),
		WithHTTPClient(&http.Client{Transport: refusingTransport{}}),
	)
	_, err := c.Verify(context.Background())
	if err == nil {
		t.Fatal("expected error from refusing transport")
	}
}

type refusingTransport struct{}

func (refusingTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("refused")
}
