//go:build integration

// OE-663 — integration tests for the personnel-security HTTP API.
// Real Postgres, real RLS, real JWT auth through the same middleware
// chain the production binary mounts (the policyacks harness pattern).
//
// Covers the issue's INTEGRATION acceptance criteria:
//
//   - Full lifecycle over HTTP: create-manual → list (filters) → get
//     with items → complete every item → checklist status flips to
//     completed.
//   - Completing an item over HTTP produces the evidence record (kind
//     personnel_security.workflow.v1) exactly as the OE-630 store test
//     asserts: kind, control binding, payload workflow_kind/item_slug.
//   - Cross-tenant access returns not-found: tenant B's bearer against
//     tenant A's checklist id (GET) and item id (complete) — and tenant
//     B's list never surfaces tenant A rows (RLS isolation; no roster
//     leakage).
//   - The handler-level role guard 403s a bare credential (no
//     program-read signal) before it can read person PII.

package personnelsecurity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
)

// ----- harness -----

type harness struct {
	server    *httptest.Server
	apiServer *api.Server
	app       *pgxpool.Pool
	migrate   *pgxpool.Pool
}

func setup(t *testing.T) *harness {
	t.Helper()
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	apiServer := api.New(api.Config{RotationGrace: time.Hour})
	apiServer.AttachDB(app)
	// Wire the slice-190 JWT validator BEFORE building the router — the
	// jwtmw mount in buildRouter is gated on the signer being present
	// at build time (IssueTestJWT lazy-wires it on first call).
	_ = apiServer.IssueTestJWT(t, testjwt.AdminFor(uuid.New()))
	h := apiServer.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &harness{server: ts, apiServer: apiServer, app: app, migrate: migrate}
}

func (h *harness) freshTenant(t *testing.T) (tenant string, bearer string) {
	t.Helper()
	tenant = dbtest.SeedTenant(t, h.migrate,
		"evidence_audit_log",
		"evidence_records",
		"personnel_security_checklist_items",
		"personnel_security_checklists",
		"controls",
	)
	return tenant, h.apiServer.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
}

func seedAccessControl(t *testing.T, migrate *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, scf_id, title, control_family,
			implementation_type, lifecycle_state, bundle_id
		) VALUES (
			$1, $2, 'CC1-PS', 'Personnel security access control', 'CC1',
			'manual_attested', 'active', $3
		)
	`, id, tenant, "personnel-security-api-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func (h *harness) do(t *testing.T, method, path, bearer string, body any, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status %d (want %d) body=%s", method, path, resp.StatusCode, wantStatus, raw)
	}
	return raw
}

// wire shapes decoded by the tests — deliberately declared here (not
// shared with the handler) so a wire-shape drift breaks the test.
type itemWire struct {
	ID               string     `json:"id"`
	ChecklistID      string     `json:"checklist_id"`
	Slug             string     `json:"slug"`
	SortOrder        int32      `json:"sort_order"`
	CompletedAt      *time.Time `json:"completed_at"`
	CompletedBy      string     `json:"completed_by"`
	EvidenceRecordID *string    `json:"evidence_record_id"`
	EvidenceURI      string     `json:"evidence_uri"`
	Notes            string     `json:"notes"`
}

type checklistWire struct {
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	Source           string     `json:"source"`
	PersonExternalID string     `json:"person_external_id"`
	ControlID        *string    `json:"control_id"`
	DueAt            time.Time  `json:"due_at"`
	Status           string     `json:"status"`
	Items            []itemWire `json:"items"`
}

type checklistEnvelope struct {
	Checklist checklistWire `json:"checklist"`
}

type listEnvelope struct {
	Checklists []checklistWire `json:"checklists"`
	Count      int             `json:"count"`
}

// ----- tests -----

func TestChecklistLifecycleOverHTTP(t *testing.T) {
	h := setup(t)
	tenant, bearer := h.freshTenant(t)
	controlID := seedAccessControl(t, h.migrate, tenant)

	// Create a manual onboarding checklist.
	raw := h.do(t, http.MethodPost, "/v1/personnel-security/checklists", bearer, map[string]any{
		"kind":               "onboarding",
		"person_external_id": "worker-123",
		"person_work_email":  "newhire@example.com",
		"control_id":         controlID.String(),
		"created_by":         "security-admin",
	}, http.StatusCreated)
	var created checklistEnvelope
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	c := created.Checklist
	if c.Kind != "onboarding" || c.Source != "manual" || c.Status != "open" || len(c.Items) != 3 {
		t.Fatalf("created checklist = %+v", c)
	}
	if c.ControlID == nil || *c.ControlID != controlID.String() {
		t.Fatalf("created control binding = %v", c.ControlID)
	}

	// The index surfaces it; the kind filter narrows; a non-matching
	// filter excludes it.
	var list listEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists?kind=onboarding&status=open", bearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 1 || list.Checklists[0].ID != c.ID {
		t.Fatalf("filtered list = %+v", list)
	}
	if len(list.Checklists[0].Items) != 0 {
		t.Fatalf("index read must not carry items: %+v", list.Checklists[0].Items)
	}
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists?kind=offboarding", bearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 0 {
		t.Fatalf("offboarding filter matched an onboarding checklist: %+v", list)
	}
	h.do(t, http.MethodGet, "/v1/personnel-security/checklists?kind=sabbatical", bearer, nil, http.StatusBadRequest)

	// Get-with-items returns the three template items in sort order.
	var got checklistEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists/"+c.ID, bearer, nil, http.StatusOK), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if len(got.Checklist.Items) != 3 || got.Checklist.Items[1].Slug != "provision_access" {
		t.Fatalf("get-with-items = %+v", got.Checklist.Items)
	}

	// Complete the provision_access item; the response carries the
	// completion fields + evidence binding.
	target := got.Checklist.Items[1]
	raw = h.do(t, http.MethodPost, "/v1/personnel-security/checklist-items/"+target.ID+"/complete", bearer, map[string]any{
		"completed_by": "security-admin",
		"evidence_uri": "atlas://evidence/access-grant/worker-123",
		"notes":        "Access grant ticket approved and applied.",
	}, http.StatusOK)
	var completed struct {
		Item itemWire `json:"item"`
	}
	if err := json.Unmarshal(raw, &completed); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if completed.Item.CompletedAt == nil || completed.Item.CompletedBy != "security-admin" || completed.Item.EvidenceRecordID == nil {
		t.Fatalf("completed item = %+v", completed.Item)
	}

	// The evidence record exists with the exact shape the OE-630 store
	// test asserts: kind personnel_security.workflow.v1, bound to the
	// checklist's control, payload carrying workflow_kind + item_slug.
	var evidenceKind, evidenceControl string
	var payloadRaw []byte
	err := h.migrate.QueryRow(context.Background(), `
		SELECT evidence_kind, control_id::text, payload
		FROM evidence_records
		WHERE tenant_id = $1 AND id = $2
	`, tenant, *completed.Item.EvidenceRecordID).Scan(&evidenceKind, &evidenceControl, &payloadRaw)
	if err != nil {
		t.Fatalf("load evidence record: %v", err)
	}
	if evidenceKind != personnelsecurity.EvidenceKind {
		t.Fatalf("evidence kind = %q, want %q", evidenceKind, personnelsecurity.EvidenceKind)
	}
	if evidenceControl != controlID.String() {
		t.Fatalf("evidence control = %s, want %s", evidenceControl, controlID)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["workflow_kind"] != "onboarding" || payload["item_slug"] != "provision_access" || payload["completed_by"] != "security-admin" {
		t.Fatalf("unexpected evidence payload: %#v", payload)
	}

	// Completing an already-completed item is a 404 (store semantics).
	h.do(t, http.MethodPost, "/v1/personnel-security/checklist-items/"+target.ID+"/complete", bearer, map[string]any{
		"completed_by": "security-admin",
	}, http.StatusNotFound)

	// Complete the remaining items: the checklist flips to completed
	// and the ?status=completed filter finds it.
	for _, item := range []itemWire{got.Checklist.Items[0], got.Checklist.Items[2]} {
		h.do(t, http.MethodPost, "/v1/personnel-security/checklist-items/"+item.ID+"/complete", bearer, map[string]any{
			"completed_by": "security-admin",
		}, http.StatusOK)
	}
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists/"+c.ID, bearer, nil, http.StatusOK), &got); err != nil {
		t.Fatalf("decode get after completion: %v", err)
	}
	if got.Checklist.Status != "completed" {
		t.Fatalf("checklist status = %q, want completed", got.Checklist.Status)
	}
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists?status=completed", bearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode completed list: %v", err)
	}
	if list.Count != 1 {
		t.Fatalf("completed list = %+v", list)
	}
}

func TestOverdueFilter(t *testing.T) {
	h := setup(t)
	_, bearer := h.freshTenant(t)

	// One offboarding checklist already past due, one onboarding due
	// far in the future.
	past := time.Now().UTC().Add(-48 * time.Hour)
	future := time.Now().UTC().Add(14 * 24 * time.Hour)
	var overdueID string
	for _, c := range []struct {
		kind string
		due  time.Time
	}{{"offboarding", past}, {"onboarding", future}} {
		raw := h.do(t, http.MethodPost, "/v1/personnel-security/checklists", bearer, map[string]any{
			"kind":               c.kind,
			"person_external_id": "worker-" + c.kind,
			"due_at":             c.due.Format(time.RFC3339),
			"created_by":         "security-admin",
		}, http.StatusCreated)
		var env checklistEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if c.kind == "offboarding" {
			overdueID = env.Checklist.ID
		}
	}

	var list listEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists?overdue=true", bearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode overdue list: %v", err)
	}
	if list.Count != 1 || list.Checklists[0].ID != overdueID {
		t.Fatalf("overdue list = %+v, want only %s", list, overdueID)
	}
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists", bearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode unfiltered list: %v", err)
	}
	if list.Count != 2 {
		t.Fatalf("unfiltered list = %+v, want both", list)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	h := setup(t)
	tenantA, bearerA := h.freshTenant(t)
	_, bearerB := h.freshTenant(t)
	controlID := seedAccessControl(t, h.migrate, tenantA)

	raw := h.do(t, http.MethodPost, "/v1/personnel-security/checklists", bearerA, map[string]any{
		"kind":               "offboarding",
		"person_external_id": "tenant-a-worker",
		"person_work_email":  "leaver@tenant-a.example.com",
		"control_id":         controlID.String(),
		"created_by":         "security-admin",
	}, http.StatusCreated)
	var created checklistEnvelope
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	// Tenant B's list never surfaces tenant A's roster (no PII leak).
	var list listEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists", bearerB, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 0 {
		t.Fatalf("tenant B saw tenant A checklists: %+v", list)
	}

	// Tenant B against tenant A's checklist id: indistinguishable from
	// a missing checklist.
	h.do(t, http.MethodGet, "/v1/personnel-security/checklists/"+created.Checklist.ID, bearerB, nil, http.StatusNotFound)

	// Tenant B against tenant A's item id: the complete verb is a 404
	// too, and no evidence record lands for either tenant.
	var got checklistEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/personnel-security/checklists/"+created.Checklist.ID, bearerA, nil, http.StatusOK), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	h.do(t, http.MethodPost, "/v1/personnel-security/checklist-items/"+got.Checklist.Items[0].ID+"/complete", bearerB, map[string]any{
		"completed_by": "tenant-b-intruder",
	}, http.StatusNotFound)
	var evidenceCount int
	if err := h.migrate.QueryRow(context.Background(), `
		SELECT count(*) FROM evidence_records WHERE tenant_id = $1 AND evidence_kind = $2
	`, tenantA, personnelsecurity.EvidenceKind).Scan(&evidenceCount); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceCount != 0 {
		t.Fatalf("cross-tenant complete attempt produced %d evidence records", evidenceCount)
	}
}

func TestBareCredentialForbidden(t *testing.T) {
	h := setup(t)
	tenant, _ := h.freshTenant(t)

	// A viewer bearer carries no program-read signal (no admin flag, no
	// approver flag, no owner roles) — the handler-level guard 403s it
	// before any person PII is read.
	viewer := h.apiServer.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	h.do(t, http.MethodGet, "/v1/personnel-security/checklists", viewer, nil, http.StatusForbidden)
	h.do(t, http.MethodPost, "/v1/personnel-security/checklists", viewer, map[string]any{
		"kind":               "onboarding",
		"person_external_id": "worker-x",
	}, http.StatusForbidden)
}
