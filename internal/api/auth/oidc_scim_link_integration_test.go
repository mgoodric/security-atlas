//go:build integration

// Handler-level integration tests for #1944: the first OIDC sign-in of a
// SCIM-provisioned user links to the SCIM row by email instead of colliding
// with users_email_per_tenant_unique, and never links to a row bound to
// another IdP principal or on an unverified email.
//
// Run via: go test -tags=integration ./internal/api/auth/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package auth_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/sessions"
)

// insertSCIMUser seeds a row the way CreateSCIMUser leaves it (scim_managed,
// idp tuple as given; empty for a never-signed-in user). The email differs in
// case from the fake IdP's, so the link is proven case-insensitive.
func (h harness) insertSCIMUser(t *testing.T, tenantID uuid.UUID, issuer, subject string, active bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	status := "active"
	if !active {
		status = "disabled"
	}
	if _, err := h.admin.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status, active,
		                    idp_issuer, idp_subject, scim_external_id, scim_managed)
		 VALUES ($1, $2, 'Founder@Example.com', 'SCIM Founder', $3, $4, $5, $6, 'ext-1', true)`,
		id, tenantID, status, active, issuer, subject,
	); err != nil {
		t.Fatalf("insert SCIM user: %v", err)
	}
	return id
}

func (h harness) idpTuple(t *testing.T, userID uuid.UUID) (string, string) {
	t.Helper()
	var iss, sub string
	if err := h.admin.QueryRow(context.Background(),
		`SELECT idp_issuer, idp_subject FROM users WHERE id = $1`, userID,
	).Scan(&iss, &sub); err != nil {
		t.Fatalf("read idp tuple: %v", err)
	}
	return iss, sub
}

// SCIM-create, then OIDC sign-in: the callback links the SCIM row (no second
// user, no 500) and opens a session for it. A repeat sign-in reuses the row.
func TestOIDCCallback_SCIMUser_LinksByEmail(t *testing.T) {
	cases := []struct {
		name     string
		verified any
	}{
		{name: "claim absent", verified: nil},
		{name: "verified true", verified: true},
		{name: "verified string", verified: "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tenant := uuid.New()
			h.insertTenant(t, tenant, "SCIM Org", "scim-org", true)
			userID := h.insertSCIMUser(t, tenant, "", "", true)
			idp := newFakeIdP(t)
			idp.emailVerified = tc.verified

			for i := range 2 {
				rec := h.callbackVia(t, idp, tenant)
				if rec.Code != http.StatusFound {
					t.Fatalf("sign-in %d: status = %d, want 302; body %s", i+1, rec.Code, rec.Body.String())
				}
				if cookieNamed(rec, sessions.CookieName) == nil {
					t.Fatalf("sign-in %d: no %s cookie", i+1, sessions.CookieName)
				}
			}
			if n := h.count(t, `SELECT COUNT(*) FROM users WHERE tenant_id = $1`, tenant); n != 1 {
				t.Errorf("users in tenant = %d, want 1 (the linked SCIM row)", n)
			}
			if iss, sub := h.idpTuple(t, userID); iss != idp.srv.URL || sub != "oidc-founder" {
				t.Errorf("SCIM row idp tuple = (%q, %q), want (%q, oidc-founder)", iss, sub, idp.srv.URL)
			}
			if n := h.count(t, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID); n != 2 {
				t.Errorf("sessions for the SCIM row = %d, want 2", n)
			}
		})
	}
}

// Each refusal is the 401 the #1912 deactivation check returns, with no
// session and no second user.
func TestOIDCCallback_SCIMUser_Refusals(t *testing.T) {
	cases := []struct {
		name            string
		issuer, subject string
		active          bool
		verified        any
		wantLinked      bool
	}{
		// SCIM deprovisioned: the row links, then the deactivation check refuses.
		{name: "deprovisioned", active: false, wantLinked: true},
		// Bound to another principal: never re-linked.
		{name: "different idp tuple", issuer: "https://other-idp.example", subject: "someone-else", active: true},
		// The IdP says the email is unverified: no link.
		{name: "email unverified", active: true, verified: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tenant := uuid.New()
			h.insertTenant(t, tenant, "SCIM Org", "scim-org", true)
			userID := h.insertSCIMUser(t, tenant, tc.issuer, tc.subject, tc.active)
			idp := newFakeIdP(t)
			idp.emailVerified = tc.verified

			rec := h.callbackVia(t, idp, tenant)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
			}
			if cookieNamed(rec, sessions.CookieName) != nil {
				t.Errorf("refused sign-in set a %s cookie", sessions.CookieName)
			}
			if n := h.count(t, `SELECT COUNT(*) FROM sessions WHERE tenant_id = $1`, tenant); n != 0 {
				t.Errorf("sessions = %d, want 0", n)
			}
			if n := h.count(t, `SELECT COUNT(*) FROM users WHERE tenant_id = $1`, tenant); n != 1 {
				t.Errorf("users in tenant = %d, want 1", n)
			}
			wantIss, wantSub := tc.issuer, tc.subject
			if tc.wantLinked {
				wantIss, wantSub = idp.srv.URL, "oidc-founder"
			}
			if iss, sub := h.idpTuple(t, userID); iss != wantIss || sub != wantSub {
				t.Errorf("idp tuple = (%q, %q), want (%q, %q)", iss, sub, wantIss, wantSub)
			}
		})
	}
}
