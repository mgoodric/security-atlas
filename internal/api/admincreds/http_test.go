package admincreds

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// #1914: a ttl_seconds whose Duration would overflow is refused with 400
// before the store is touched. The Handler has a nil store, so reaching
// it would panic.
func TestIssue_RejectsOverflowingTTL(t *testing.T) {
	t.Parallel()
	for _, ttl := range []int64{-1, maxTTLSeconds + 1, 1 << 62, math.MaxInt64} {
		body, _ := json.Marshal(IssueRequest{TTLSeconds: ttl})
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
		req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{IsAdmin: true}))
		w := httptest.NewRecorder()
		New(nil).Issue(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ttl_seconds=%d: want 400, got %d: %s", ttl, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "ttl_seconds") {
			t.Fatalf("ttl_seconds=%d: body %s does not name the field", ttl, w.Body.String())
		}
	}
}

// #1953: liveMintedBy keeps only the user's own keys that have not expired.
func TestLiveMintedBy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	const user = "6f1c2b8e-0000-4000-8000-000000000001"
	creds := []credstore.Credential{
		{ID: "key_live", MintedBy: user},
		{ID: "key_ttl_live", MintedBy: user, IssuedAt: now.Add(-time.Hour), TTL: 2 * time.Hour},
		{ID: "key_expired", MintedBy: user, IssuedAt: now.Add(-3 * time.Hour), TTL: time.Hour},
		// A rotated-out predecessor authenticates until retires_at.
		{ID: "key_in_grace", MintedBy: user, RetiresAt: now.Add(time.Hour)},
		{ID: "key_past_grace", MintedBy: user, RetiresAt: now.Add(-time.Minute)},
		{ID: "key_other", MintedBy: "6f1c2b8e-0000-4000-8000-000000000002"},
		{ID: "key_unminted"},
	}
	got := liveMintedBy(creds, user, now)
	ids := make([]string, 0, len(got))
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "key_live,key_ttl_live,key_in_grace" {
		t.Fatalf("liveMintedBy = %v; want [key_live key_ttl_live key_in_grace]", ids)
	}
}

// #1953: a minted_by that is not a uuid is refused before the store is
// touched (the Handler has a nil store, so reaching it would panic).
func TestList_RejectsMalformedMintedBy(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/credentials?minted_by=not-a-uuid", nil)
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{IsAdmin: true}))
	w := httptest.NewRecorder()
	New(nil).List(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// #2384: a label is trimmed, may be empty, and is refused over 100
// characters or with a control character.
func TestNormalizeLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
		refused  bool
	}{
		{in: "", want: ""},
		{in: "   ", want: ""},
		{in: "  CI deploy  ", want: "CI deploy"},
		{in: strings.Repeat("é", 100), want: strings.Repeat("é", 100)},
		{in: strings.Repeat("a", 101), refused: true},
		{in: "ci\ndeploy", refused: true},
		{in: "ci\x00", refused: true},
		{in: "ci\u202edeploy", refused: true},
		{in: "ci\u200bdeploy", refused: true},
		{in: "ci\u2066deploy\u2069", refused: true},
		{in: "ci' (owner: Alice)", refused: true},
	} {
		got, msg := normalizeLabel(tc.in)
		if tc.refused {
			if msg == "" {
				t.Errorf("%q: want refused, got %q", tc.in, got)
			}
			continue
		}
		if msg != "" || got != tc.want {
			t.Errorf("%q: got (%q, %q), want (%q, \"\")", tc.in, got, msg, tc.want)
		}
	}
}

// #2384: an over-long label is refused with 400 before the store is
// touched (the nil store would panic).
func TestIssue_RejectsLongLabel(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(IssueRequest{Label: strings.Repeat("x", 101)})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(body))
	req = req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{IsAdmin: true}))
	w := httptest.NewRecorder()
	New(nil).Issue(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "label") {
		t.Fatalf("want 400 naming label, got %d: %s", w.Code, w.Body.String())
	}
}

// adminLabelledKey2656 is an unbound key labelled admin: a tenant admin by
// its label (authz.IsTenantAdmin), not an admin credential.
func adminLabelledKey2656(issued time.Time, ttl time.Duration, predicate string, kinds []string) credstore.Credential {
	const id = "key_00000000-0000-4000-8000-000000002656"
	return credstore.Credential{
		ID: id, UserID: id, TenantID: "00000000-0000-4000-8000-0000000026a0",
		OwnerRoles: []string{"admin"}, IssuedAt: issued, TTL: ttl,
		ScopePredicate: predicate, Kinds: kinds,
	}
}

// #2656: a key-as-tenant-admin mints no key that outlives it, is scoped
// wider, or pushes other kinds. Admin credentials and JWT callers are not
// bound.
func TestBoundByCallerKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	const prod = `{"op":"eq","dim":"environment","value":"prod"}`
	const prodAWS = `{"op":"and","args":[` + prod + `,{"op":"eq","dim":"cloud_account","value":"aws:1"}]}`
	// Issued an hour ago with a two-hour TTL: an hour left.
	short := adminLabelledKey2656(now.Add(-time.Hour), 2*time.Hour, "{}", nil)
	scoped := adminLabelledKey2656(now, 0, prod, nil)
	kinded := adminLabelledKey2656(now, 0, "{}", []string{"sast.scan_result"})
	jwtAdmin := credstore.Credential{ID: "jwt:2656", UserID: "user:00000000-0000-4000-8000-000000002656", TenantRoles: []string{"admin"}}

	cases := []struct {
		name      string
		cred      credstore.Credential
		ttl       time.Duration
		predicate string
		kinds     []string
		refused   bool
	}{
		{"short key: no expiry", short, 0, "{}", nil, true},
		{"short key: past its expiry", short, time.Hour + time.Second, "{}", nil, true},
		{"short key: ends with it", short, time.Hour, "{}", nil, false},
		{"short key: ends before it", short, time.Minute, "{}", nil, false},
		{"scoped key: unrestricted", scoped, 0, "{}", nil, true},
		{"scoped key: other value", scoped, 0, `{"op":"eq","dim":"environment","value":"staging"}`, nil, true},
		{"scoped key: same scope", scoped, 0, prod, nil, false},
		{"scoped key: narrower", scoped, 0, prodAWS, nil, false},
		{"kinded key: every kind", kinded, 0, "{}", nil, true},
		{"kinded key: other kind", kinded, 0, "{}", []string{"sast.scan_result", "access_review.completion"}, true},
		{"kinded key: same kinds", kinded, 0, "{}", []string{"sast.scan_result"}, false},
		{"unbounded key: anything", adminLabelledKey2656(now, 0, "{}", nil), 0, "{}", nil, false},
		{"admin key is not bound", credstore.Credential{ID: "key_x", IsAdmin: true, TTL: time.Minute, IssuedAt: now, ScopePredicate: prod, Kinds: []string{"k"}}, 0, "{}", nil, false},
		{"JWT tenant admin is not bound", jwtAdmin, 0, "{}", nil, false},
	}
	for _, tc := range cases {
		msg := boundByCallerKey(tc.cred, tc.ttl, tc.predicate, tc.kinds, now)
		if (msg != "") != tc.refused {
			t.Errorf("%s: boundByCallerKey = %q, want refused=%v", tc.name, msg, tc.refused)
		}
	}
}

// #2656: Issue refuses a key-as-tenant-admin's wider key with 403 before
// the store is touched (the nil store would panic).
func TestIssue_KeyCallerCannotMintWiderKey(t *testing.T) {
	t.Parallel()
	caller := adminLabelledKey2656(time.Now().Add(-time.Minute), 10*time.Minute,
		`{"op":"eq","dim":"environment","value":"prod"}`, []string{"sast.scan_result"})
	for name, body := range map[string]IssueRequest{
		"never expires": {TTLSeconds: 0, ScopePredicate: `{"op":"eq","dim":"environment","value":"prod"}`, AllowedKinds: []string{"sast.scan_result"}},
		"no scope":      {TTLSeconds: 60, AllowedKinds: []string{"sast.scan_result"}},
		"every kind":    {TTLSeconds: 60, ScopePredicate: `{"op":"eq","dim":"environment","value":"prod"}`},
	} {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/v1/admin/credentials", bytes.NewReader(raw))
		req = req.WithContext(authctx.WithCredential(req.Context(), caller))
		w := httptest.NewRecorder()
		New(nil).Issue(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d: %s", name, w.Code, w.Body.String())
		}
	}
}
