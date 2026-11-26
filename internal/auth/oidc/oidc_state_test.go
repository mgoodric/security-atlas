// Unit tests for the signed flow state and the __Host- flow cookies
// (#2177).
package oidc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
)

var allFlowCookies = []string{oidc.StateCookie, oidc.VerifierCookie, oidc.IdpCookie, oidc.NonceCookie, oidc.TenantCookie}

// errReachedIdp is what reachedIdpResolver answers: the callback passed
// every cookie and state check and went on to look up the IdP.
var errReachedIdp = errors.New("callback reached the IdP lookup")

type reachedIdpResolver struct{}

func (reachedIdpResolver) ResolveIdp(context.Context, uuid.UUID, string) (oidc.IdpConfig, error) {
	return oidc.IdpConfig{}, errReachedIdp
}

// issuedFlow runs a real BeginLogin and returns the flow's tenant and its
// cookies keyed by base name.
func issuedFlow(t *testing.T, secure bool) (uuid.UUID, map[string]*http.Cookie) {
	t.Helper()
	idp := newDiscoveryOnlyIDP(t)
	tenantID := uuid.New()
	a := oidc.New(unitStubResolver{cfg: oidc.IdpConfig{
		TenantID: tenantID, Name: "primary", IssuerURL: idp.URL,
		ClientID: "test-client", RedirectURL: "http://localhost:9999/auth/oidc/callback",
	}}, testStateKey, loopbackIdP)
	res, err := a.BeginLogin(context.Background(), oidc.LoginInput{TenantID: tenantID, IdpName: "primary"}, secure)
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	cookies := map[string]*http.Cookie{}
	for _, base := range allFlowCookies {
		c := findCookie(res.Cookies, oidc.CookieName(base, secure))
		if c == nil {
			t.Fatalf("BeginLogin did not emit %q", oidc.CookieName(base, secure))
		}
		cookies[base] = c
	}
	u, err := url.Parse(res.AuthURL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	if got := u.Query().Get("state"); got != cookies[oidc.StateCookie].Value {
		t.Fatalf("authorize URL state = %q, state cookie = %q", got, cookies[oidc.StateCookie].Value)
	}
	// #2298: state = rand "." iat "." mac, iat the issue time in unix seconds.
	parts := strings.Split(cookies[oidc.StateCookie].Value, ".")
	if len(parts) != 3 {
		t.Fatalf("state %q is not rand.iat.mac", cookies[oidc.StateCookie].Value)
	}
	iat, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Since(time.Unix(iat, 0)).Abs() > time.Minute {
		t.Fatalf("state iat = %q, want the current unix time", parts[1])
	}
	return tenantID, cookies
}

func callbackRequest(state string, cookies map[string]*http.Cookie, secure bool) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=c&state="+url.QueryEscape(state), nil)
	for base, c := range cookies {
		r.AddCookie(&http.Cookie{Name: oidc.CookieName(base, secure), Value: c.Value})
	}
	return r
}

// TestHandleCallbackVerifiesSignedState covers #2177: the callback accepts
// only a state the server signed over the flow's tenant, IdP and nonce. The
// callback runs on a second Authenticator holding the same key, as another
// replica would; reaching the IdP lookup is the pass signal.
func TestHandleCallbackVerifiesSignedState(t *testing.T) {
	for _, secure := range []bool{false, true} {
		cases := map[string]struct {
			mutate func(tenant *uuid.UUID, cookies map[string]*http.Cookie)
			key    []byte
			want   error
		}{
			"issued flow, same key on another replica": {want: errReachedIdp},
			"forged state": {
				mutate: func(_ *uuid.UUID, c map[string]*http.Cookie) { c[oidc.StateCookie].Value = "attacker.forged" },
				want:   oidc.ErrStateMismatch,
			},
			"unsigned state": {
				mutate: func(_ *uuid.UUID, c map[string]*http.Cookie) { c[oidc.StateCookie].Value = "plainrandom" },
				want:   oidc.ErrStateMismatch,
			},
			"swapped tenant": {
				mutate: func(tenant *uuid.UUID, c map[string]*http.Cookie) {
					*tenant = uuid.New()
					c[oidc.TenantCookie].Value = tenant.String()
				},
				want: oidc.ErrStateMismatch,
			},
			"swapped idp": {
				mutate: func(_ *uuid.UUID, c map[string]*http.Cookie) { c[oidc.IdpCookie].Value = "other-idp" },
				want:   oidc.ErrStateMismatch,
			},
			"swapped nonce": {
				mutate: func(_ *uuid.UUID, c map[string]*http.Cookie) { c[oidc.NonceCookie].Value = "other-nonce" },
				want:   oidc.ErrStateMismatch,
			},
			"replica with a different key": {
				key:  []byte("another-replica-key-0123456789abcdef"),
				want: oidc.ErrStateMismatch,
			},
		}
		for name, tc := range cases {
			t.Run(name+secureSuffix(secure), func(t *testing.T) {
				tenant, cookies := issuedFlow(t, secure)
				if tc.mutate != nil {
					tc.mutate(&tenant, cookies)
				}
				key := testStateKey
				if tc.key != nil {
					key = tc.key
				}
				r := callbackRequest(cookies[oidc.StateCookie].Value, cookies, secure)
				_, err := oidc.New(reachedIdpResolver{}, key, loopbackIdP).HandleCallback(context.Background(), r, tenant, secure)
				if !errors.Is(err, tc.want) {
					t.Fatalf("HandleCallback err = %v, want %v", err, tc.want)
				}
			})
		}
	}
}

func secureSuffix(secure bool) string {
	if secure {
		return " (secure)"
	}
	return " (http)"
}

// TestHandleCallbackIgnoresUnprefixedCookiesWhenSecure covers #2177: with
// secure cookies the callback reads only the __Host- names, so bare cookies
// a sibling subdomain planted are not a flow.
func TestHandleCallbackIgnoresUnprefixedCookiesWhenSecure(t *testing.T) {
	tenant, cookies := issuedFlow(t, false)
	r := callbackRequest(cookies[oidc.StateCookie].Value, cookies, false)
	_, err := oidc.New(reachedIdpResolver{}, testStateKey, loopbackIdP).HandleCallback(context.Background(), r, tenant, true)
	if !errors.Is(err, oidc.ErrStateMismatch) {
		t.Fatalf("HandleCallback err = %v, want ErrStateMismatch", err)
	}
}

// TestBeginLoginSecureFlowCookiesUseHostPrefix covers #2177: every flow
// cookie is __Host- prefixed, Secure, Path=/ and has no Domain, which is
// what lets the browser refuse one set by a sibling subdomain.
func TestBeginLoginSecureFlowCookiesUseHostPrefix(t *testing.T) {
	_, cookies := issuedFlow(t, true)
	for base, c := range cookies {
		if !strings.HasPrefix(c.Name, oidc.HostPrefix) || !c.Secure || c.Path != "/" || c.Domain != "" || !c.HttpOnly {
			t.Errorf("%s cookie = %+v, want a __Host- cookie", base, c)
		}
	}
}

// TestBeginLoginHTTPFlowCookiesKeepBareNames covers the plain-HTTP dev
// fallback: no prefix (a browser drops a non-Secure __Host- cookie), path
// scoped to /auth/oidc.
func TestBeginLoginHTTPFlowCookiesKeepBareNames(t *testing.T) {
	_, cookies := issuedFlow(t, false)
	for base, c := range cookies {
		if c.Name != base || c.Secure || c.Path != oidc.FlowCookiePath {
			t.Errorf("%s cookie = %+v, want the bare HTTP flow cookie", base, c)
		}
	}
}

// TestClearFlowCookiesMatchesSetShape: clearing uses the same names and
// path as setting, or the browser keeps the original cookie.
func TestClearFlowCookiesMatchesSetShape(t *testing.T) {
	for _, secure := range []bool{false, true} {
		rec := httptest.NewRecorder()
		oidc.ClearFlowCookies(rec, secure)
		got := rec.Result().Cookies()
		for _, base := range allFlowCookies {
			c := findCookie(got, oidc.CookieName(base, secure))
			want := oidc.FlowCookiePath
			if secure {
				want = "/"
			}
			if c == nil || c.MaxAge >= 0 || c.Path != want || c.Secure != secure {
				t.Errorf("secure=%t: %s not expired with path %s: %+v", secure, base, want, c)
			}
		}
	}
}

// TestAuthenticatorWithoutStateKeyFailsClosed: no key, no flow.
func TestAuthenticatorWithoutStateKeyFailsClosed(t *testing.T) {
	a := oidc.New(panicResolver{t}, []byte("short"))
	if _, err := a.BeginLogin(context.Background(), oidc.LoginInput{TenantID: uuid.New(), IdpName: "x"}, false); !errors.Is(err, oidc.ErrStateKeyInvalid) {
		t.Fatalf("BeginLogin err = %v, want ErrStateKeyInvalid", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=c&state=s", nil)
	if _, err := a.HandleCallback(context.Background(), r, uuid.New(), false); !errors.Is(err, oidc.ErrStateKeyInvalid) {
		t.Fatalf("HandleCallback err = %v, want ErrStateKeyInvalid", err)
	}
}

// TestStateKeyFromEnv covers the key source: a dedicated key when set, a
// key derived from the shared root otherwise, and an error (never a
// fallback) for a short one.
func TestStateKeyFromEnv(t *testing.T) {
	root := []byte("bearer-hash-key-0123456789abcdef0123")

	t.Setenv(oidc.StateKeyEnv, "")
	derived, err := oidc.StateKeyFromEnv(root)
	if err != nil {
		t.Fatalf("derived: %v", err)
	}
	again, _ := oidc.StateKeyFromEnv(root)
	if string(derived) != string(again) || string(derived) == string(root) || len(derived) < oidc.StateKeyMinBytes {
		t.Fatalf("derived key must be stable, distinct from the root and at least %d bytes", oidc.StateKeyMinBytes)
	}
	if _, err := oidc.StateKeyFromEnv([]byte("short")); err == nil {
		t.Fatalf("short root accepted")
	}

	dedicated := "dedicated-oidc-state-key-0123456789"
	t.Setenv(oidc.StateKeyEnv, dedicated)
	got, err := oidc.StateKeyFromEnv(root)
	if err != nil || string(got) != dedicated {
		t.Fatalf("dedicated key = %q, %v; want %q", got, err, dedicated)
	}

	t.Setenv(oidc.StateKeyEnv, "too-short")
	if _, err := oidc.StateKeyFromEnv(root); err == nil {
		t.Fatalf("short %s accepted", oidc.StateKeyEnv)
	}
}
