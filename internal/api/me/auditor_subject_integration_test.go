//go:build integration

// #2183 — an auditor signed in through local login / PKCE / the device grant
// carries cred.UserID = "user:<uuid>". Auditor assignments are keyed by the
// bare users(id), and both readers resolve to it: /v1/me/audit-period(s) and
// the authz attrs resolver that hydrates audit_period_ids. So the period the
// auditor sees in /v1/me is one they can write notes in. A machine credential
// resolves to no user, sees no period and hydrates none.

package me_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// subjectRoles is a stub authz.RolesResolver keyed on the raw subject, the
// same raw value resolveRoles receives in production.
type subjectRoles map[string][]authz.Role

func (m subjectRoles) RolesFor(_ context.Context, _, userID string) ([]authz.Role, error) {
	return m[userID], nil
}

func bearerCall(t *testing.T, method, url, bearer string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestAuditor_UserPrefixedSubject_SeesPeriodAndWritesNotes(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, "notifications", "audit_notes", "auditor_assignments", "audit_periods")
	periodID := seedNotesPeriod(t, admin, tenant)
	tid := uuid.MustParse(tenant)

	auditorUser := uuid.New()
	// Assignments are keyed by the bare users(id).
	if err := auditor.NewStore(app).Assign(dbtest.WithTenantCtx(t, tenant), auditorUser.String(), periodID, "test"); err != nil {
		t.Fatalf("assign auditor: %v", err)
	}
	// A machine credential holding an assignment row under its own raw id
	// must still not act as an auditor of that period.
	const machineSubject = "oauth_client:ci-runner"
	if err := auditor.NewStore(app).Assign(dbtest.WithTenantCtx(t, tenant), machineSubject, periodID, "test"); err != nil {
		t.Fatalf("assign machine: %v", err)
	}

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	auditorClaims := testjwt.OwnerFor(tid, []string{"auditor"})
	auditorClaims.Subject = "user:" + auditorUser.String()
	machineClaims := testjwt.OwnerFor(tid, []string{"auditor"})
	machineClaims.Subject = machineSubject
	engine, err := authz.NewEngine(context.Background(), subjectRoles{
		auditorClaims.Subject: {authz.RoleAuditor},
		machineClaims.Subject: {authz.RoleAuditor},
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	engine = engine.WithAttrsResolver(auditor.NewDBAttrsResolver(app))
	srv.AttachAuthz(engine, nil)
	auditorBearer := srv.IssueTestJWT(t, auditorClaims)
	machineBearer := srv.IssueTestJWT(t, machineClaims)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	// The user: auditor sees the period over HTTP, through the real authz
	// middleware.
	status, body := bearerCall(t, http.MethodGet, ts.URL+"/v1/me/audit-period", auditorBearer, nil)
	if status != http.StatusOK {
		t.Fatalf("auditor GET /v1/me/audit-period: status %d body=%v", status, body)
	}
	if got := body["audit_period"].(map[string]any)["audit_period_id"]; got != periodID.String() {
		t.Fatalf("auditor period = %v; want %s", got, periodID)
	}
	// The machine credential sees none.
	if status, body = bearerCall(t, http.MethodGet, ts.URL+"/v1/me/audit-period", machineBearer, nil); status == http.StatusOK {
		t.Fatalf("machine GET /v1/me/audit-period: status 200 body=%v; want no period", body)
	}

	// Writing a note in the period is the auditor.rego audit-notes write rule:
	// the hydrated audit_period_ids must contain the note's period. That is
	// decided here at the engine, with the raw subject as the authz input,
	// because the HTTP authz middleware does not yet put audit_period_id on
	// input.resource.attrs for POST /v1/audit-notes, so no auditor (bare or
	// prefixed subject) reaches the rule over HTTP today. That gap predates
	// #2183; it is reported on PR #2186 for its own ticket.
	decideWrite := func(subject string) bool {
		t.Helper()
		d, err := engine.Decide(context.Background(), authz.Input{
			User:     authz.UserInput{ID: subject, Roles: []authz.Role{authz.RoleAuditor}},
			TenantID: tenant,
			Action:   "write",
			Resource: authz.ResourceInput{
				Type:  "audit-notes",
				Attrs: map[string]interface{}{"audit_period_id": periodID.String()},
			},
		})
		if err != nil {
			t.Fatalf("Decide(%q): %v", subject, err)
		}
		return d.Allow
	}
	if !decideWrite(auditorClaims.Subject) {
		t.Errorf("auditor %q may not write notes in the assigned period; want allow", auditorClaims.Subject)
	}
	if decideWrite(machineSubject) {
		t.Errorf("machine %q may write notes in the period; want deny", machineSubject)
	}
}
