package opauth_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

func setAll(t *testing.T, events, id, secret string) {
	t.Helper()
	t.Setenv(opauth.EnvEventsToken, events)
	t.Setenv(opauth.EnvUsersClientID, id)
	t.Setenv(opauth.EnvUsersClientSecret, secret)
}

func TestResolve_FromEnv(t *testing.T) {
	setAll(t, " test-events-token ", "test-client-id", "test-client-secret")
	c, err := opauth.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://events.example.invalid", nil)
	c.ApplyEvents(req)
	if got := req.Header.Get("Authorization"); got != "Bearer test-events-token" {
		t.Errorf("Authorization = %q; want trimmed events bearer", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	tok, _ := http.NewRequest(http.MethodPost, "https://api.example.invalid/v1/oauth/token", nil)
	c.ApplyUsersClient(tok)
	id, secret, ok := tok.BasicAuth()
	if !ok || id != "test-client-id" || secret != "test-client-secret" {
		t.Errorf("BasicAuth = %q/%q/%v; want the client credentials", id, secret, ok)
	}
}

func TestResolve_NamesEveryMissingVariable(t *testing.T) {
	setAll(t, "", "test-client-id", "")
	_, err := opauth.Resolve()
	if err == nil {
		t.Fatal("expected error when secrets are missing")
	}
	for _, v := range []string{opauth.EnvEventsToken, opauth.EnvUsersClientSecret} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("err %q does not name %s", err, v)
		}
	}
	if strings.Contains(err.Error(), "test-client-id") {
		t.Errorf("err %q leaks a value", err)
	}
}

func TestCredentials_StringRedacts(t *testing.T) {
	setAll(t, "test-events-secret-value", "test-client-id-value", "test-client-secret-value")
	c, err := opauth.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, verb := range []string{"%s", "%v", "%+v", "%#v"} {
		out := fmt.Sprintf(verb, c)
		for _, secret := range []string{"test-events-secret-value", "test-client-id-value", "test-client-secret-value"} {
			if strings.Contains(out, secret) {
				t.Errorf("%s leaks %q: %s", verb, secret, out)
			}
		}
		if !strings.Contains(out, "redacted") {
			t.Errorf("%s output lacks redaction marker: %s", verb, out)
		}
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCheckRedirect(t *testing.T) {
	via := []*http.Request{{URL: mustURL(t, "https://events.example.test/api/v2/auth/introspect")}}
	cases := map[string]struct {
		to   string
		want error
	}{
		"same origin":   {"https://events.example.test/other", nil},
		"another host":  {"https://evil.example/api", opauth.ErrUnsafeRedirect},
		"another port":  {"https://events.example.test:8443/api", opauth.ErrUnsafeRedirect},
		"scheme change": {"http://events.example.test/api", opauth.ErrUnsafeRedirect},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := opauth.CheckRedirect(&http.Request{URL: mustURL(t, tc.to)}, via)
			if tc.want == nil && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v; want %v", err, tc.want)
			}
		})
	}
	long := make([]*http.Request, 10)
	for i := range long {
		long[i] = via[0]
	}
	if err := opauth.CheckRedirect(&http.Request{URL: via[0].URL}, long); err == nil {
		t.Error("expected a stop after 10 redirects")
	}
}

// Anti-criterion P0: documented scopes must be read-only. Reject any
// keyword that implies write/manage/admin access, and the Users API's two
// write scopes by name.
func TestDocumentedScopes_NoWriteOrManageOrAdmin(t *testing.T) {
	scopes := opauth.DocumentedScopes()
	if len(scopes) == 0 {
		t.Fatal("DocumentedScopes returned empty")
	}
	for _, sc := range scopes {
		access := strings.ToLower(sc.Access)
		for _, banned := range []string{"write", "manage", "admin", "delete"} {
			if strings.Contains(access, banned) {
				t.Errorf("scope %q has Access %q containing banned keyword %q",
					sc.Name, sc.Access, banned)
			}
		}
		nameLower := strings.ToLower(sc.Name)
		for _, banned := range []string{"write_items", "manage_vault", "admin", "users.suspend", "users.reactivate"} {
			if strings.Contains(nameLower, banned) {
				t.Errorf("scope name %q contains banned scope identifier %q", sc.Name, banned)
			}
		}
	}
}

func TestDocumentedScopes_CoversOrgPolicyKind(t *testing.T) {
	for _, s := range opauth.DocumentedScopes() {
		if !strings.Contains(s.Gates, "1password.org_policy.v2") {
			t.Errorf("scope %q does not gate 1password.org_policy.v2: %q", s.Name, s.Gates)
		}
	}
}
