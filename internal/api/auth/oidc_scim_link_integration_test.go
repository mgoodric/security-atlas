//go:build integration

// Handler-level integration tests for #1944: the first OIDC sign-in of a
// SCIM-provisioned user links to the SCIM row by email instead of colliding
// with users_email_per_tenant_unique, and never links to a row bound to
// another IdP principal, a local-password or super_admin row, or on an
// unverified email.
//
// Run via: go test -tags=integration ./internal/api/auth/...
// (needs DATABASE_URL = BYPASSRLS migrate role, DATABASE_URL_APP = atlas_app).
package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/sessions"
)

// seedUser is the existing row a sign-in may or may not link to. The zero
// value is a CreateSCIMUser row: active, scim_managed, empty idp tuple.
type seedUser struct {
	issuer, subject string
	inactive        bool
	notSCIM         bool // a CreateLocal-style row (scim_managed=false)
	password        bool // has a local_credentials row
	superAdmin      bool // has a super_admins grant
}

// insertUser seeds u in tenantID. The email differs in case from the fake
// IdP's, so a link is proven case-insensitive.
func (h harness) insertUser(t *testing.T, tenantID uuid.UUID, u seedUser) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	status := "active"
	if u.inactive {
		status = "disabled"
	}
	var externalID any = "ext-1"
	if u.notSCIM {
		externalID = nil
	}
	if _, err := h.admin.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status, active,
		                    idp_issuer, idp_subject, scim_external_id, scim_managed)
		 VALUES ($1, $2, 'Founder@Example.com', 'Founder', $3, $4, $5, $6, $7, $8)`,
		id, tenantID, status, !u.inactive, u.issuer, u.subject, externalID, !u.notSCIM,
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if u.password {
		if _, err := h.admin.Exec(ctx,
			`INSERT INTO local_credentials (user_id, tenant_id, password_hash) VALUES ($1, $2, 'x')`,
			id, tenantID,
		); err != nil {
			t.Fatalf("insert local credential: %v", err)
		}
	}
	if u.superAdmin {
		if _, err := h.admin.Exec(ctx,
			`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, 'manual_grant')`, id,
		); err != nil {
			t.Fatalf("insert super_admin: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = h.admin.Exec(context.Background(), `DELETE FROM me_audit_log WHERE user_id = $1`, id)
	})
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

func (h harness) linkAuditRows(t *testing.T, userID uuid.UUID) []string {
	t.Helper()
	rows, err := h.admin.Query(context.Background(),
		`SELECT after::text FROM me_audit_log WHERE user_id = $1 AND action = 'oidc_identity_link'`, userID)
	if err != nil {
		t.Fatalf("read link audit: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var after string
		if err := rows.Scan(&after); err != nil {
			t.Fatalf("scan link audit: %v", err)
		}
		out = append(out, after)
	}
	return out
}

// SCIM-create, then OIDC sign-in: the callback links the SCIM row (no second
// user, no 500), audits the link once, and opens a session for it. A repeat
// sign-in reuses the row.
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
			userID := h.insertUser(t, tenant, seedUser{})
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

			audits := h.linkAuditRows(t, userID)
			if len(audits) != 1 {
				t.Fatalf("oidc_identity_link audit rows = %d, want 1 (first sign-in only)", len(audits))
			}
			var after map[string]string
			if err := json.Unmarshal([]byte(audits[0]), &after); err != nil {
				t.Fatalf("audit after %q: %v", audits[0], err)
			}
			if after["idp_issuer"] != idp.srv.URL {
				t.Errorf("audit idp_issuer = %q, want %q", after["idp_issuer"], idp.srv.URL)
			}
			if _, err := uuid.Parse(after["idp_config_id"]); err != nil {
				t.Errorf("audit idp_config_id = %q, want a uuid", after["idp_config_id"])
			}
			if strings.Contains(strings.ToLower(audits[0]), "founder@example.com") {
				t.Errorf("audit row carries the email: %s", audits[0])
			}
		})
	}
}

// Each refusal is the 401 the #1912 deactivation check returns, with no
// session, no second user, and no link audit row.
func TestOIDCCallback_SCIMUser_Refusals(t *testing.T) {
	cases := []struct {
		name       string
		seed       seedUser
		verified   any
		wantLinked bool
	}{
		// SCIM deprovisioned: the row links, then the deactivation check refuses.
		{name: "deprovisioned", seed: seedUser{inactive: true}, wantLinked: true},
		// Bound to another principal: never re-linked.
		{name: "different idp tuple", seed: seedUser{issuer: "https://other-idp.example", subject: "someone-else"}},
		// The IdP says the email is unverified: no link.
		{name: "email unverified", verified: false},
		// B1 (#2007 review): local accounts are never claimed by email.
		{name: "local password user", seed: seedUser{notSCIM: true, password: true}},
		{name: "local super_admin", seed: seedUser{notSCIM: true, password: true, superAdmin: true}},
		{name: "SCIM user with a local password", seed: seedUser{password: true}},
		{name: "SCIM user granted super_admin", seed: seedUser{superAdmin: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tenant := uuid.New()
			h.insertTenant(t, tenant, "SCIM Org", "scim-org", true)
			userID := h.insertUser(t, tenant, tc.seed)
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
			wantIss, wantSub := tc.seed.issuer, tc.seed.subject
			if tc.wantLinked {
				wantIss, wantSub = idp.srv.URL, "oidc-founder"
			}
			if iss, sub := h.idpTuple(t, userID); iss != wantIss || sub != wantSub {
				t.Errorf("idp tuple = (%q, %q), want (%q, %q)", iss, sub, wantIss, wantSub)
			}
			wantAudits := 0
			if tc.wantLinked {
				wantAudits = 1
			}
			if n := len(h.linkAuditRows(t, userID)); n != wantAudits {
				t.Errorf("oidc_identity_link audit rows = %d, want %d", n, wantAudits)
			}
		})
	}
}
