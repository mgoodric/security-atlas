//go:build integration

// OE-670 — integration tests for the access-review HTTP API.
// Real Postgres, real RLS, real JWT auth through the same middleware
// chain the production binary mounts (the OE-663 harness pattern).
//
// Covers the issue's acceptance criteria:
//
//   - Full certification over HTTP: multipart-CSV create → list → get
//     (rollup + items) → attest every item as the assigned reviewer →
//     revoke-list CSV download whose rows match the store's RevokeList
//     → complete → the CC6.3 evidence record exists.
//   - Attestation by a non-assigned reviewer returns 403; completing
//     with pending items returns 409.
//   - Cross-tenant requests return no data: tenant B's bearer against
//     tenant A's campaign gets 404s (get / attest / CSV / complete)
//     and an empty list (RLS isolation at the API layer).
//   - The handler-level role guard 403s a bare credential before it
//     can read entitlement PII.
//   - The JSON (SCIM-sourced) create branch snapshots the tenant's
//     live users.

package accessreviews_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/accessreview"
	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
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

func (h *harness) freshTenant(t *testing.T) (tenant string, adminBearer string) {
	t.Helper()
	tenant = dbtest.SeedTenant(t, h.migrate,
		"notifications",
		"access_review_items",
		"access_review_reviewer_assignments",
		"access_review_campaigns",
		"evidence_records",
		"scim_group_members",
		"scim_groups",
		"users",
	)
	return tenant, h.apiServer.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
}

// do sends a request with an optional JSON body.
func (h *harness) do(t *testing.T, method, path, bearer string, body any, wantStatus int) []byte {
	t.Helper()
	var reader io.Reader
	contentType := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
		contentType = "application/json"
	}
	return h.doRaw(t, method, path, bearer, contentType, reader, wantStatus)
}

// doRaw sends a request with an arbitrary body + content type and
// returns the response body after asserting the status.
func (h *harness) doRaw(t *testing.T, method, path, bearer, contentType string, body io.Reader, wantStatus int) []byte {
	t.Helper()
	raw, _ := h.doRawWithHeader(t, method, path, bearer, contentType, body, wantStatus)
	return raw
}

func (h *harness) doRawWithHeader(t *testing.T, method, path, bearer, contentType string, body io.Reader, wantStatus int) ([]byte, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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
	return raw, resp.Header
}

// multipartCreate builds a multipart create body: form fields plus the
// entitlement CSV in the `file` part.
func multipartCreate(t *testing.T, fields map[string]string, csvBody string) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	fw, err := mw.CreateFormFile("file", "entitlements.csv")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte(csvBody)); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func seedUser(t *testing.T, pool *pgxpool.Pool, tenant string, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, active)
		VALUES ($1, $2, $3, $4, 'active', true)
	`, id, tenant, email, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// wire shapes decoded by the tests — deliberately declared here (not
// shared with the handler) so a wire-shape drift breaks the test.
type itemWire struct {
	ID              string     `json:"id"`
	CampaignID      string     `json:"campaign_id"`
	System          string     `json:"system"`
	Entitlement     string     `json:"entitlement"`
	PrincipalUserID string     `json:"principal_user_id"`
	PrincipalEmail  string     `json:"principal_email"`
	ReviewerID      string     `json:"reviewer_id"`
	Status          string     `json:"status"`
	Decision        *string    `json:"decision"`
	Reason          string     `json:"reason"`
	AttestedAt      *time.Time `json:"attested_at"`
}

type campaignWire struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Source           string  `json:"source"`
	Status           string  `json:"status"`
	CreatedBy        string  `json:"created_by"`
	EvidenceRecordID *string `json:"evidence_record_id"`
}

type rollupWire struct {
	CampaignID       string  `json:"campaign_id"`
	TotalItems       int     `json:"total_items"`
	PendingItems     int     `json:"pending_items"`
	KeepDecisions    int     `json:"keep_decisions"`
	RevokeDecisions  int     `json:"revoke_decisions"`
	ReviewerCount    int     `json:"reviewer_count"`
	EvidenceRecordID *string `json:"evidence_record_id"`
}

type createEnvelope struct {
	Campaign campaignWire `json:"campaign"`
	Items    []itemWire   `json:"items"`
	Count    int          `json:"count"`
}

type getEnvelope struct {
	Campaign campaignWire `json:"campaign"`
	Rollup   rollupWire   `json:"rollup"`
	Items    []itemWire   `json:"items"`
}

type listEnvelope struct {
	Campaigns []campaignWire `json:"campaigns"`
	Count     int            `json:"count"`
}

const lifecycleCSV = "system,entitlement,user_id,email\n" +
	"prod-db,admin,u-1,alice@example.test\n" +
	"prod-db,admin,u-2,bob@example.test\n" +
	"vpn,standard,u-1,alice@example.test\n"

// ----- tests -----

func TestCampaignLifecycleOverHTTP(t *testing.T) {
	h := setup(t)
	tenant, adminBearer := h.freshTenant(t)
	tenantID := uuid.MustParse(tenant)
	// The assigned reviewer is a distinct, program-capable (owner-role)
	// credential; its user id is the testjwt OwnerFor subject.
	reviewerBearer := h.apiServer.IssueTestJWT(t, testjwt.OwnerFor(tenantID, []string{"control_owner"}))
	reviewerID := "test-owner:" + tenant

	// Create via multipart CSV upload.
	body, contentType := multipartCreate(t, map[string]string{
		"name":      "Q3 production recertification",
		"due_at":    time.Now().UTC().Add(14 * 24 * time.Hour).Format(time.RFC3339),
		"reviewers": reviewerID,
	}, lifecycleCSV)
	var created createEnvelope
	if err := json.Unmarshal(h.doRaw(t, http.MethodPost, "/v1/access-reviews", adminBearer, contentType, body, http.StatusCreated), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	c := created.Campaign
	if c.Source != "manual_csv" || c.Status != "active" || created.Count != 3 || len(created.Items) != 3 {
		t.Fatalf("created campaign = %+v count=%d items=%d", c, created.Count, len(created.Items))
	}
	if c.CreatedBy != "test-admin:"+tenant {
		t.Fatalf("created_by = %q, want the credential identity", c.CreatedBy)
	}
	for _, item := range created.Items {
		if item.ReviewerID != reviewerID || item.Status != "pending" {
			t.Fatalf("item = %+v, want reviewer %q pending", item, reviewerID)
		}
	}

	// The index surfaces it; the status filter narrows; a bogus status
	// is a 400, not a silently-ignored filter.
	var list listEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/access-reviews?status=active", adminBearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 1 || list.Campaigns[0].ID != c.ID {
		t.Fatalf("active list = %+v", list)
	}
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/access-reviews?status=completed", adminBearer, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 0 {
		t.Fatalf("completed filter matched an active campaign: %+v", list)
	}
	h.do(t, http.MethodGet, "/v1/access-reviews?status=bogus", adminBearer, nil, http.StatusBadRequest)

	// Get returns rollup + items.
	var got getEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/access-reviews/"+c.ID, adminBearer, nil, http.StatusOK), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Rollup.TotalItems != 3 || got.Rollup.PendingItems != 3 || got.Rollup.ReviewerCount != 1 || len(got.Items) != 3 {
		t.Fatalf("get = %+v", got)
	}

	// Completing with pending items is a 409.
	h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/complete", adminBearer, nil, http.StatusConflict)

	// Attestation by a non-assigned reviewer (the admin) is a 403.
	target := got.Items[0]
	h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/items/"+target.ID+"/attest", adminBearer, map[string]any{
		"decision": "keep", "reason": "still needed",
	}, http.StatusForbidden)

	// Validation by the assigned reviewer: bad decision / missing
	// reason are 422s.
	h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/items/"+target.ID+"/attest", reviewerBearer, map[string]any{
		"decision": "maybe", "reason": "hmm",
	}, http.StatusUnprocessableEntity)
	h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/items/"+target.ID+"/attest", reviewerBearer, map[string]any{
		"decision": "keep",
	}, http.StatusUnprocessableEntity)

	// Attest all three as the assigned reviewer: keep, revoke, keep.
	decisions := []struct {
		decision, reason string
	}{
		{"keep", "still on the on-call rotation"},
		{"revoke", "left the platform team"},
		{"keep", "standard baseline access"},
	}
	for i, item := range got.Items {
		raw := h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/items/"+item.ID+"/attest", reviewerBearer, map[string]any{
			"decision": decisions[i].decision, "reason": decisions[i].reason,
		}, http.StatusOK)
		var attested struct {
			Item itemWire `json:"item"`
		}
		if err := json.Unmarshal(raw, &attested); err != nil {
			t.Fatalf("decode attest: %v", err)
		}
		if attested.Item.Status != "attested" || attested.Item.Decision == nil || *attested.Item.Decision != decisions[i].decision || attested.Item.AttestedAt == nil {
			t.Fatalf("attested item = %+v, want %s", attested.Item, decisions[i].decision)
		}
	}

	// The served revoke-list CSV matches the store's RevokeList.
	raw, header := h.doRawWithHeader(t, http.MethodGet, "/v1/access-reviews/"+c.ID+"/revoke-list.csv", reviewerBearer, "", nil, http.StatusOK)
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("revoke CSV content type = %q", ct)
	}
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("parse served CSV: %v", err)
	}
	store := accessreview.NewStore(h.app)
	storeRevokes, err := store.RevokeList(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(c.ID))
	if err != nil {
		t.Fatalf("store RevokeList: %v", err)
	}
	if len(storeRevokes) != 1 || len(rows) != len(storeRevokes)+1 {
		t.Fatalf("served CSV rows = %d, store revokes = %d", len(rows)-1, len(storeRevokes))
	}
	wantHeader := []string{"system", "entitlement", "principal_user_id", "principal_email", "reviewer_id", "reason", "attested_at"}
	for i, col := range wantHeader {
		if rows[0][i] != col {
			t.Fatalf("CSV header = %v, want %v", rows[0], wantHeader)
		}
	}
	for i, d := range storeRevokes {
		row := rows[i+1]
		want := []string{d.System, d.Entitlement, d.PrincipalUserID, d.PrincipalEmail, d.ReviewerID, d.Reason, d.AttestedAt.UTC().Format(time.RFC3339)}
		for j := range want {
			if row[j] != want[j] {
				t.Fatalf("CSV row %d = %v, want %v", i, row, want)
			}
		}
	}

	// Complete: the rollup carries the evidence id; the ledger row is
	// the registered CC6.3 completion kind.
	var completed struct {
		Rollup rollupWire `json:"rollup"`
	}
	if err := json.Unmarshal(h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/complete", adminBearer, nil, http.StatusOK), &completed); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	r1 := completed.Rollup
	if r1.PendingItems != 0 || r1.KeepDecisions != 2 || r1.RevokeDecisions != 1 || r1.EvidenceRecordID == nil {
		t.Fatalf("completion rollup = %+v", r1)
	}
	var kind, schemaVersion, controlRef string
	if err := h.migrate.QueryRow(context.Background(), `
		SELECT evidence_kind, schema_version, control_ref
		FROM evidence_records
		WHERE tenant_id = $1 AND id = $2
	`, tenant, *r1.EvidenceRecordID).Scan(&kind, &schemaVersion, &controlRef); err != nil {
		t.Fatalf("load evidence record: %v", err)
	}
	if kind != accessreview.EvidenceKind || schemaVersion != accessreview.EvidenceSchemaVersion || controlRef != accessreview.CC6ControlRef {
		t.Fatalf("evidence = kind %q schema %q ref %q", kind, schemaVersion, controlRef)
	}

	// Completing again is an idempotent 200 with the same evidence id.
	if err := json.Unmarshal(h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/complete", adminBearer, nil, http.StatusOK), &completed); err != nil {
		t.Fatalf("decode re-complete: %v", err)
	}
	if completed.Rollup.EvidenceRecordID == nil || *completed.Rollup.EvidenceRecordID != *r1.EvidenceRecordID {
		t.Fatalf("re-complete evidence id = %v, want %s", completed.Rollup.EvidenceRecordID, *r1.EvidenceRecordID)
	}

	// The campaign reads back completed.
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/access-reviews/"+c.ID, adminBearer, nil, http.StatusOK), &got); err != nil {
		t.Fatalf("decode get after complete: %v", err)
	}
	if got.Campaign.Status != "completed" || got.Campaign.EvidenceRecordID == nil {
		t.Fatalf("campaign after complete = %+v", got.Campaign)
	}
}

func TestSCIMSourcedCreateOverHTTP(t *testing.T) {
	h := setup(t)
	tenant, bearer := h.freshTenant(t)
	seedUser(t, h.migrate, tenant, "alice@example.test")
	seedUser(t, h.migrate, tenant, "bob@example.test")

	raw := h.do(t, http.MethodPost, "/v1/access-reviews", bearer, map[string]any{
		"name":      "SCIM quarterly review",
		"due_at":    time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339),
		"reviewers": []string{"test-admin:" + tenant},
	}, http.StatusCreated)
	var created createEnvelope
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Campaign.Source != "scim" || created.Count != 2 {
		t.Fatalf("SCIM create = %+v count=%d", created.Campaign, created.Count)
	}

	// manual_csv over JSON is rejected — the CSV rides multipart.
	h.do(t, http.MethodPost, "/v1/access-reviews", bearer, map[string]any{
		"name":      "wrong shape",
		"source":    "manual_csv",
		"due_at":    time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		"reviewers": []string{"r1"},
	}, http.StatusBadRequest)

	// A CSV missing a required column is a 422; a header-only CSV
	// yields zero items, also a 422.
	for _, csvBody := range []string{
		"system,user_id\nprod-db,u-1\n",
		"system,entitlement,user_id,email\n",
	} {
		body, contentType := multipartCreate(t, map[string]string{
			"name":      "bad csv",
			"due_at":    time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			"reviewers": "r1",
		}, csvBody)
		h.doRaw(t, http.MethodPost, "/v1/access-reviews", bearer, contentType, body, http.StatusUnprocessableEntity)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	h := setup(t)
	tenantA, bearerA := h.freshTenant(t)
	_, bearerB := h.freshTenant(t)

	body, contentType := multipartCreate(t, map[string]string{
		"name":      "tenant A recert",
		"due_at":    time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		"reviewers": "test-admin:" + tenantA,
	}, lifecycleCSV)
	var created createEnvelope
	if err := json.Unmarshal(h.doRaw(t, http.MethodPost, "/v1/access-reviews", bearerA, contentType, body, http.StatusCreated), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	c := created.Campaign

	// Tenant B's list never surfaces tenant A's campaigns (no
	// entitlement-roster leakage).
	var list listEnvelope
	if err := json.Unmarshal(h.do(t, http.MethodGet, "/v1/access-reviews", bearerB, nil, http.StatusOK), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Count != 0 {
		t.Fatalf("tenant B saw tenant A campaigns: %+v", list)
	}

	// Every tenant-B request against tenant A's ids is indistinguishable
	// from a missing campaign — 404, never 403.
	h.do(t, http.MethodGet, "/v1/access-reviews/"+c.ID, bearerB, nil, http.StatusNotFound)
	h.do(t, http.MethodGet, "/v1/access-reviews/"+c.ID+"/revoke-list.csv", bearerB, nil, http.StatusNotFound)
	h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/complete", bearerB, nil, http.StatusNotFound)
	h.do(t, http.MethodPost, "/v1/access-reviews/"+c.ID+"/items/"+created.Items[0].ID+"/attest", bearerB, map[string]any{
		"decision": "revoke", "reason": "intrusion attempt",
	}, http.StatusNotFound)

	// The cross-tenant attest attempt left no trace on tenant A.
	var attested int
	if err := h.migrate.QueryRow(context.Background(), `
		SELECT count(*) FROM access_review_items
		WHERE tenant_id = $1 AND status = 'attested'
	`, tenantA).Scan(&attested); err != nil {
		t.Fatalf("count attested: %v", err)
	}
	if attested != 0 {
		t.Fatalf("cross-tenant attest attempt attested %d items", attested)
	}
}

func TestBareCredentialForbidden(t *testing.T) {
	h := setup(t)
	tenant, _ := h.freshTenant(t)

	// A viewer bearer carries no program role signal (no admin flag, no
	// approver flag, no owner roles) — the handler-level guard 403s it
	// before any entitlement PII is read or written.
	viewer := h.apiServer.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	h.do(t, http.MethodGet, "/v1/access-reviews", viewer, nil, http.StatusForbidden)
	h.do(t, http.MethodGet, "/v1/access-reviews/"+uuid.NewString(), viewer, nil, http.StatusForbidden)
	h.do(t, http.MethodGet, "/v1/access-reviews/"+uuid.NewString()+"/revoke-list.csv", viewer, nil, http.StatusForbidden)
	h.do(t, http.MethodPost, "/v1/access-reviews", viewer, map[string]any{"name": "nope"}, http.StatusForbidden)
	h.do(t, http.MethodPost, "/v1/access-reviews/"+uuid.NewString()+"/items/"+uuid.NewString()+"/attest", viewer, map[string]any{
		"decision": "keep", "reason": "nope",
	}, http.StatusForbidden)
	h.do(t, http.MethodPost, "/v1/access-reviews/"+uuid.NewString()+"/complete", viewer, nil, http.StatusForbidden)
}
