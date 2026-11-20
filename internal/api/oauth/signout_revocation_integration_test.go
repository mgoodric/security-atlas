//go:build integration

package oauth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

// TestIntegrationSignOut_RevokedSessionJWTIs401 is the server half of the
// web sign-out (#2618): signOut self-revokes the session JWT (POST
// /oauth/revoke with token=<jwt> and that jwt as the bearer) before it
// deletes the cookie. A copy of the cookie taken before sign-out must then
// be refused on a protected route, by the same jti denylist the request
// middleware checks in production.
func TestIntegrationSignOut_RevokedSessionJWTIs401(t *testing.T) {
	srv, signer, revoked, _, _ := newRevokeIntrospectServer(t)
	tok, _ := mintJWTForTest(t, signer, "user:"+uuid.NewString())

	protected := httptest.NewServer(jwtmw.Middleware(signer, revoked, jwtmw.Options{
		ExpectedIssuer:   testIssuer,
		ExpectedAudience: testIssuer,
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	t.Cleanup(protected.Close)

	get := func() int {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, protected.URL+"/v1/anchors", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET protected: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if code := get(); code != http.StatusOK {
		t.Fatalf("before sign-out: status %d, want 200", code)
	}

	form := url.Values{}
	form.Set("token", tok)
	resp, body := postFormAt(t, srv.URL+oauth.PathRevoke, form, map[string]string{
		"Authorization": "Bearer " + tok,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self-revoke: status %d body=%s, want 200", resp.StatusCode, body)
	}

	if code := get(); code != http.StatusUnauthorized {
		t.Fatalf("after sign-out: status %d, want 401 for the revoked session JWT", code)
	}
}
