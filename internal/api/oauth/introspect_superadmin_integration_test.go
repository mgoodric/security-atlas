//go:build integration

package oauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/oauth"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/superadmin"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// TestIntegrationIntrospect_SuperAdminClaimIsLive (#1870): a token
// minted with atlas:super_admin=true introspects with the LIVE
// super_admins state, not the mint-time snapshot. A demoted subject,
// a checker error and a missing checker all report false (fail
// closed); only a still-current super_admin reports true.
func TestIntegrationIntrospect_SuperAdminClaimIsLive(t *testing.T) {
	stillAdmin := superadmin.CheckerFunc(func(context.Context, uuid.UUID) (bool, error) { return true, nil })
	demoted := superadmin.CheckerFunc(func(context.Context, uuid.UUID) (bool, error) { return false, nil })
	broken := superadmin.CheckerFunc(func(context.Context, uuid.UUID) (bool, error) {
		return true, errors.New("db down")
	})

	cases := []struct {
		name    string
		checker superadmin.Checker
		want    bool
	}{
		{"still super_admin", stillAdmin, true},
		{"demoted since mint", demoted, false},
		{"checker error fails closed", broken, false},
		{"no checker fails closed", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := openTokenIntegrationPool(t)
			ks, err := fsstore.Open(t.TempDir())
			if err != nil {
				t.Fatalf("fsstore.Open: %v", err)
			}
			signer := tokensign.New(ks)
			clients := oauthclient.New(pool)
			ep := oauth.NewIntrospectionEndpoint(signer, revocation.New(pool), clients, oauth.IntrospectionEndpointConfig{
				Issuer:      testIssuer,
				SuperAdmins: tc.checker,
			})
			h := oauth.New(ks, oauth.Config{Issuer: testIssuer})
			h.AttachIntrospectionEndpoint(ep)
			srv := httptest.NewServer(newRouter(h))
			t.Cleanup(srv.Close)

			clientID, clientSecret := issueClientForTest(t, clients)
			_, claims := mintJWTForTest(t, signer, "user:"+uuid.NewString())
			claims.SuperAdmin = true
			tok, err := signer.Sign(context.Background(), claims)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}

			form := url.Values{}
			form.Set("token", tok)
			form.Set("client_id", clientID)
			form.Set("client_secret", clientSecret)
			resp, body := postFormAt(t, srv.URL+oauth.PathIntrospect, form, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d body = %s", resp.StatusCode, body)
			}
			var out map[string]any
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if act, _ := out["active"].(bool); !act {
				t.Fatalf("active = %v, want true; body = %s", out["active"], body)
			}
			if got, _ := out["atlas:super_admin"].(bool); got != tc.want {
				t.Fatalf("atlas:super_admin = %v, want %v; body = %s", out["atlas:super_admin"], tc.want, body)
			}
		})
	}
}
