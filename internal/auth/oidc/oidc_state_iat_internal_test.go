// Unit tests for the state issue time and the HKDF-derived state key
// (#2298).
package oidc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/bearer"
)

var errIatReachedIdp = errors.New("callback reached the IdP lookup")

type iatReachedIdpResolver struct{}

func (iatReachedIdpResolver) ResolveIdp(context.Context, uuid.UUID, string) (IdpConfig, error) {
	return IdpConfig{}, errIatReachedIdp
}

// TestHandleCallbackRefusesStateOutsideItsLifetime covers #2298: the
// callback trusts the state's issue time only after the MAC verifies, and
// refuses a state issued more than FlowCookieMaxAge (plus skew) ago, one
// dated in the future beyond the skew, one whose iat was altered, and one
// with no iat. Reaching the IdP lookup is the pass signal.
func TestHandleCallbackRefusesStateOutsideItsLifetime(t *testing.T) {
	now := time.Unix(1783200000, 0)
	tenant := uuid.New()
	const idp, nonce, stateRand = "primary", "flow-nonce", "flow-random"

	a := New(iatReachedIdpResolver{}, testStateKey)
	a.now = func() time.Time { return now }

	signed := func(issued time.Time) string {
		iat := strconv.FormatInt(issued.Unix(), 10)
		return stateRand + "." + iat + "." + a.stateMAC(stateRand, iat, tenant.String(), idp, nonce)
	}
	fresh := signed(now)
	freshMAC := fresh[strings.LastIndex(fresh, ".")+1:]

	cases := map[string]struct {
		state string
		want  error
	}{
		"fresh":                      {state: fresh, want: errIatReachedIdp},
		"at the max age":             {state: signed(now.Add(-FlowCookieMaxAge)), want: errIatReachedIdp},
		"within the skew past max":   {state: signed(now.Add(-FlowCookieMaxAge - StateClockSkew)), want: errIatReachedIdp},
		"within the skew in future":  {state: signed(now.Add(StateClockSkew)), want: errIatReachedIdp},
		"expired":                    {state: signed(now.Add(-FlowCookieMaxAge - StateClockSkew - time.Second)), want: ErrStateMismatch},
		"expired long ago":           {state: signed(now.Add(-24 * time.Hour)), want: ErrStateMismatch},
		"future beyond the skew":     {state: signed(now.Add(StateClockSkew + time.Second)), want: ErrStateMismatch},
		"tampered iat":               {state: stateRand + "." + strconv.FormatInt(now.Unix()+1, 10) + "." + freshMAC, want: ErrStateMismatch},
		"missing iat":                {state: stateRand + ".." + freshMAC, want: ErrStateMismatch},
		"non-numeric iat, valid MAC": {state: stateRand + ".x." + a.stateMAC(stateRand, "x", tenant.String(), idp, nonce), want: ErrStateMismatch},
		"pre-#2298 two-part state":   {state: stateRand + "." + a.stateMAC(stateRand, tenant.String(), idp, nonce), want: ErrStateMismatch},
		"extra part":                 {state: fresh + ".x", want: ErrStateMismatch},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=c&state="+url.QueryEscape(tc.state), nil)
			for n, v := range map[string]string{
				StateCookie: tc.state, VerifierCookie: "verifier", IdpCookie: idp,
				NonceCookie: nonce, TenantCookie: tenant.String(),
			} {
				r.AddCookie(&http.Cookie{Name: n, Value: v})
			}
			_, err := a.HandleCallback(context.Background(), r, tenant, false)
			if !errors.Is(err, tc.want) {
				t.Fatalf("HandleCallback err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestDeriveStateKeyIsNotABearerHash covers #2298: the derived key is HKDF,
// 32 bytes, and equals neither the v1 derivation nor a bearer hash of its
// label, with or without the HKDF counter byte.
func TestDeriveStateKeyIsNotABearerHash(t *testing.T) {
	root := []byte("bearer-hash-key-0123456789abcdef0123")
	got := DeriveStateKey(root)
	if len(got) != 32 {
		t.Fatalf("derived key is %d bytes, want 32", len(got))
	}
	h, err := bearer.NewHasher(root)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	for _, label := range []string{"atlas-oidc-state-key-v1", stateKeyInfo, stateKeyInfo + "\x01"} {
		if hmac.Equal(got, h.Hash(label)) {
			t.Fatalf("derived key equals bearer.Hash(%q)", label)
		}
	}
	m := hmac.New(sha256.New, root)
	m.Write([]byte(stateKeyInfo + "\x01"))
	if hmac.Equal(got, m.Sum(nil)) {
		t.Fatalf("derived key is HKDF-Expand keyed by the root, not extract-then-expand")
	}
	if hmac.Equal(got, DeriveStateKey([]byte("another-root-key-0123456789abcdef0"))) {
		t.Fatalf("derived key does not depend on the root")
	}
}
