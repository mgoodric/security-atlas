//go:build integration

// #2609: PATCH /v1/admin/sso writes a tenant_sso_config_update audit row in
// the change's own transaction, with the before and after of every field,
// the client secret recorded only as secret_changed, and the actor.
package adminsso_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminsso"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// auditRouter serves PATCH /v1/admin/sso as a signed-in user of tenant.
func auditRouter(t *testing.T, tenant, user uuid.UUID, h *adminsso.Handler) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "jwt:sso-audit",
				TenantID: tenant.String(),
				IsAdmin:  true,
				UserID:   "user:" + user.String(),
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/sso", h.Get)
	r.Patch("/v1/admin/sso", h.Patch)
	return r
}

func patchSSO(t *testing.T, r http.Handler, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/sso", bytes.NewReader(b)))
	return rec
}

type ssoAuditRow struct {
	UserID    uuid.UUID
	Principal *string
	Before    map[string]any
	After     map[string]any
	Raw       string
}

// ssoAuditRows reads tenant's tenant_sso_config_update rows, oldest first,
// as the app role under the tenant's RLS context.
func ssoAuditRows(t *testing.T, tenant uuid.UUID) []ssoAuditRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `
		SELECT user_id, actor_principal, before, after, to_jsonb(me_audit_log)::text
		FROM me_audit_log
		WHERE action = $1
		ORDER BY occurred_at, id`, adminsso.AuditActionSSOUpdate)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []ssoAuditRow
	for rows.Next() {
		var r ssoAuditRow
		var before, after []byte
		if err := rows.Scan(&r.UserID, &r.Principal, &before, &after, &r.Raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(before, &r.Before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(after, &r.After); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// captureLogs routes the default slog logger into a buffer for the test.
// Not parallel-safe; the tests that use it do not call t.Parallel.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestPatchSSOWritesAuditRowWithBeforeAndAfter(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	cleanupSSO(t, tenant)
	logs := captureLogs(t)
	r := auditRouter(t, tenant, user, adminsso.New(appPool).WithSecretCrypter(testCrypter(t)))

	const firstSecret, secondSecret = "first-secret-2609-xyz", "second-secret-2609-abc"
	rec := patchSSO(t, r, map[string]any{
		"issuer_url":            "https://idp-one.example.com",
		"client_id":             "client-one",
		"client_secret":         firstSecret,
		"redirect_url":          "https://atlas.example.com/auth/oidc/callback",
		"allowed_email_domains": []string{"example.com"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("first PATCH = %d; %s", rec.Code, rec.Body.String())
	}
	responses := rec.Body.String()

	// Change every field, flip the trust flag, rotate the secret.
	rec = patchSSO(t, r, map[string]any{
		"issuer_url":                      "https://idp-two.example.com",
		"client_id":                       "client-two",
		"client_secret":                   secondSecret,
		"redirect_url":                    "https://atlas2.example.com/auth/oidc/callback",
		"allowed_email_domains":           []string{"example.com", "example.org"},
		"trust_unverified_email_for_link": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("second PATCH = %d; %s", rec.Code, rec.Body.String())
	}
	responses += rec.Body.String()

	// Only the trust flag flips back; the secret is left as is.
	rec = patchSSO(t, r, map[string]any{
		"issuer_url":            "https://idp-two.example.com",
		"client_id":             "client-two",
		"redirect_url":          "https://atlas2.example.com/auth/oidc/callback",
		"allowed_email_domains": []string{"example.com", "example.org"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("third PATCH = %d; %s", rec.Code, rec.Body.String())
	}
	responses += rec.Body.String()

	// The same PATCH again changes nothing and writes no row.
	rec = patchSSO(t, r, map[string]any{
		"issuer_url":            "https://idp-two.example.com",
		"client_id":             "client-two",
		"redirect_url":          "https://atlas2.example.com/auth/oidc/callback",
		"allowed_email_domains": []string{"example.com", "example.org"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op PATCH = %d; %s", rec.Code, rec.Body.String())
	}
	responses += rec.Body.String()

	rows := ssoAuditRows(t, tenant)
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want 3 (the no-op PATCH writes none)", len(rows))
	}
	for i, row := range rows {
		if row.UserID != user {
			t.Errorf("row %d user_id = %s, want %s", i, row.UserID, user)
		}
		if row.Principal == nil || *row.Principal != user.String() {
			t.Errorf("row %d actor_principal = %v, want %s", i, row.Principal, user)
		}
	}

	first := rows[0]
	if first.Before["configured"] != false {
		t.Errorf("first row before.configured = %v, want false", first.Before["configured"])
	}
	wantAfter := map[string]any{
		"configured":                      true,
		"issuer_url":                      "https://idp-one.example.com",
		"client_id":                       "client-one",
		"redirect_url":                    "https://atlas.example.com/auth/oidc/callback",
		"trust_unverified_email_for_link": false,
		"secret_changed":                  true,
	}
	assertFields(t, "first after", first.After, wantAfter)

	second := rows[1]
	assertFields(t, "second before", second.Before, map[string]any{
		"configured":                      true,
		"issuer_url":                      "https://idp-one.example.com",
		"client_id":                       "client-one",
		"redirect_url":                    "https://atlas.example.com/auth/oidc/callback",
		"trust_unverified_email_for_link": false,
	})
	assertFields(t, "second after", second.After, map[string]any{
		"issuer_url":                      "https://idp-two.example.com",
		"client_id":                       "client-two",
		"redirect_url":                    "https://atlas2.example.com/auth/oidc/callback",
		"trust_unverified_email_for_link": true,
		"secret_changed":                  true,
	})
	if got := second.After["allowed_email_domains"]; !jsonEqual(got, []any{"example.com", "example.org"}) {
		t.Errorf("second after.allowed_email_domains = %v", got)
	}
	if got := second.Before["allowed_email_domains"]; !jsonEqual(got, []any{"example.com"}) {
		t.Errorf("second before.allowed_email_domains = %v", got)
	}

	third := rows[2]
	assertFields(t, "third before", third.Before, map[string]any{"trust_unverified_email_for_link": true})
	assertFields(t, "third after", third.After, map[string]any{
		"trust_unverified_email_for_link": false,
		"secret_changed":                  false,
	})
	if _, ok := third.Before["secret_changed"]; ok {
		t.Error("before value carries secret_changed; only the after value should")
	}

	// The secret appears nowhere: not in a row (any column), not in a
	// response, not in a log line.
	for _, secret := range []string{firstSecret, secondSecret} {
		for i, row := range rows {
			if strings.Contains(row.Raw, secret) {
				t.Errorf("audit row %d contains the client secret: %s", i, row.Raw)
			}
		}
		if strings.Contains(responses, secret) {
			t.Error("a PATCH response contains the client secret")
		}
		if strings.Contains(logs.String(), secret) {
			t.Error("the server log contains the client secret")
		}
	}
	for i, row := range rows {
		for _, k := range []string{"client_secret", "client_secret_enc", "secret_hash", "secret_length"} {
			if strings.Contains(row.Raw, `"`+k+`"`) {
				t.Errorf("audit row %d carries a %s key: %s", i, k, row.Raw)
			}
		}
	}
}

// A failed audit write rolls the SSO change back: no change commits
// without its audit row.
func TestPatchSSOAuditFailureRollsBackChange(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	cleanupSSO(t, tenant)
	base := adminsso.New(appPool).WithSecretCrypter(testCrypter(t))
	r := auditRouter(t, tenant, user, base)

	rec := patchSSO(t, r, map[string]any{
		"issuer_url":    "https://idp-one.example.com",
		"client_id":     "client-one",
		"client_secret": "s1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("seed PATCH = %d; %s", rec.Code, rec.Body.String())
	}

	failing := auditRouter(t, tenant, user, base.WithAuditInsert(
		func(context.Context, *dbx.Queries, dbx.InsertMeAuditLogParams) error {
			return errors.New("audit write refused")
		}))
	rec = patchSSO(t, failing, map[string]any{
		"issuer_url":                      "https://idp-evil.example.com",
		"client_id":                       "client-evil",
		"trust_unverified_email_for_link": true,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PATCH with failing audit = %d, want 500; %s", rec.Code, rec.Body.String())
	}

	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/v1/admin/sso", nil))
	var got adminsso.GetResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("GET body: %v; %s", err, getRec.Body.String())
	}
	if got.IssuerURL != "https://idp-one.example.com" || got.ClientID != "client-one" || got.TrustUnverifiedEmailForLink {
		t.Errorf("config changed despite the failed audit write: %+v", got)
	}
	if n := len(ssoAuditRows(t, tenant)); n != 1 {
		t.Errorf("audit rows = %d, want 1 (the seed only)", n)
	}
}

// Another tenant cannot read the audit row.
func TestPatchSSOAuditRowIsTenantIsolated(t *testing.T) {
	tenantA, tenantB, user := uuid.New(), uuid.New(), uuid.New()
	cleanupSSO(t, tenantA)
	r := auditRouter(t, tenantA, user, adminsso.New(appPool).WithSecretCrypter(testCrypter(t)))
	rec := patchSSO(t, r, map[string]any{
		"issuer_url":    "https://idp-one.example.com",
		"client_id":     "client-one",
		"client_secret": "s1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d; %s", rec.Code, rec.Body.String())
	}
	if n := len(ssoAuditRows(t, tenantA)); n != 1 {
		t.Fatalf("tenant A audit rows = %d, want 1", n)
	}
	if n := len(ssoAuditRows(t, tenantB)); n != 0 {
		t.Errorf("tenant B sees %d of tenant A's SSO audit rows", n)
	}
}

func assertFields(t *testing.T, label string, got, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s.%s = %v, want %v", label, k, got[k], v)
		}
	}
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}
