//go:build integration

// Integration tests for slice 018: FrameworkScope predicate + four-state
// workflow + intersection compute. Real Postgres only — RLS + the
// BEFORE UPDATE trigger cannot be tested against a fake DB
// (memory rule: "Never mock the DB"; canvas §5.4).
//
// Every test owns a fresh tenant id and registers cleanup so the integration
// suite is order-independent.

package frameworkscope_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

// freshTenant returns a brand-new tenant id and registers cleanup that wipes
// every row written under it (framework_scopes + framework_versions +
// frameworks). The cleanup deletes children-first to honour FKs.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM framework_scopes WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM scope_cells WHERE tenant_id = $1`,
			`DELETE FROM scope_dimensions WHERE tenant_id = $1`,
			`DELETE FROM scopes WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
			// Framework versions + frameworks are tenant-scoped (NULL = global)
			// — only cull the tenant-owned rows we created here.
			`DELETE FROM framework_versions WHERE tenant_id = $1`,
			`DELETE FROM frameworks WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedFrameworkVersion inserts a tenant-owned framework + framework_version
// so framework_scopes can FK to it. Returns the framework_version id.
func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	fwID := uuid.New()
	fvID := uuid.New()
	err := withAdminTenant(admin, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
			VALUES ($1, $2, 'Test Framework', 'test-fw', 'tester')
		`, fwID, tenant); err != nil {
			return fmt.Errorf("insert framework: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
			VALUES ($1, $2, $3, '2017', 'current')
		`, fvID, tenant, fwID); err != nil {
			return fmt.Errorf("insert framework_version: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return fvID
}

func seedScopeAndControl(t *testing.T, admin *pgxpool.Pool, app *pgxpool.Pool, tenant string, exprJSON string) (uuid.UUID, []scope.Cell) {
	t.Helper()
	store := scope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	if _, err := store.SeedTenant(ctx); err != nil {
		t.Fatalf("seed scope tenant: %v", err)
	}
	cells := make([]scope.Cell, 0, 3)
	for _, dims := range []map[string]string{
		{"business_unit": "platform", "environment": "prod", "data_classification": "restricted"},
		{"business_unit": "platform", "environment": "staging", "data_classification": "confidential"},
		{"business_unit": "platform", "environment": "dev", "data_classification": "public"},
	} {
		c, err := store.CreateCell(ctx, "", dims)
		if err != nil {
			t.Fatalf("create cell: %v", err)
		}
		cells = append(cells, c)
	}
	controlID := uuid.New()
	if err := withAdminTenant(admin, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO controls (id, tenant_id, scf_id, title, control_family, implementation_type, applicability_expr)
			VALUES ($1, $2, 'IAC-06', 'MFA', 'IAC', 'automated', $3)
		`, controlID, tenant, exprJSON)
		return err
	}); err != nil {
		t.Fatalf("insert control: %v", err)
	}
	return controlID, cells
}

// withAdminTenant runs fn inside an admin-pool transaction with the tenant
// GUC applied. The admin pool is BYPASSRLS but we set the GUC anyway for
// consistency with how application traffic sees rows.
func withAdminTenant(admin *pgxpool.Pool, tenant string, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := admin.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		return fmt.Errorf("set_config: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ----- AC-1 / AC-4 / AC-5 / AC-6 / AC-8 / AC-10 -----

// TestCreateAndLifecycle — happy path: draft -> review -> approved ->
// activated. Covers AC-5, AC-6, AC-7 (partially — no file upload), AC-8.
func TestCreateAndLifecycle(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	// AC-5: Create in draft.
	created, err := store.Create(ctx, frameworkscope.CreateRequest{
		FrameworkVersionID: fvID,
		Name:               "SOC 2 system",
		Predicate:          []byte(`{"op":"eq","dim":"environment","value":"prod"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.State != frameworkscope.StateDraft {
		t.Fatalf("initial state = %q; want draft", created.State)
	}
	if created.PredicateHash == "" {
		t.Fatalf("predicate_hash unset on create")
	}

	// AC-6: draft -> review.
	reviewed, err := store.Submit(ctx, created.ID)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if reviewed.State != frameworkscope.StateReview {
		t.Fatalf("after submit state = %q; want review", reviewed.State)
	}

	// AC-7: review -> approved.
	approverID := uuid.NewString()
	approved, err := store.Approve(ctx, frameworkscope.ApproveRequest{
		ID:             reviewed.ID,
		ApproverUserID: approverID,
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.State != frameworkscope.StateApproved {
		t.Fatalf("after approve state = %q; want approved", approved.State)
	}
	if approved.ApprovedAt == nil {
		t.Fatalf("approved_at not set")
	}
	if approved.PredicateHashAtApproval == nil || *approved.PredicateHashAtApproval != approved.PredicateHash {
		t.Fatalf("predicate_hash_at_approval != predicate_hash")
	}

	// AC-8: approved -> activated.
	effective := time.Now().UTC().Truncate(time.Second)
	activated, err := store.Activate(ctx, approved.ID, effective)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if activated.State != frameworkscope.StateActivated {
		t.Fatalf("after activate state = %q; want activated", activated.State)
	}
	if activated.EffectiveFrom == nil {
		t.Fatalf("effective_from not set on activate")
	}

	// AC-10: Activated lookup returns this row.
	got, err := store.Activated(ctx, fvID)
	if err != nil {
		t.Fatalf("Activated: %v", err)
	}
	if got.ID != activated.ID {
		t.Fatalf("Activated returned wrong id")
	}
}

// TestSupersession — AC-8: activating a new approved row atomically
// supersedes the prior activated row. Tests AC-3 (one-active invariant) by
// inspecting state of the predecessor after the second activate.
func TestSupersession(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	first := mustActivate(t, ctx, store, fvID, "v1", `{"op":"true"}`)

	// Second: a tighter predicate. Lifecycle through, then activate.
	second := mustActivate(t, ctx, store, fvID, "v2-tighter", `{"op":"eq","dim":"environment","value":"prod"}`)

	// Predecessor should now be `superseded` with superseded_by = second.id.
	prev, err := store.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get predecessor: %v", err)
	}
	if prev.State != frameworkscope.StateSuperseded {
		t.Fatalf("predecessor state = %q; want superseded", prev.State)
	}
	if prev.SupersededBy == nil || *prev.SupersededBy != second.ID {
		t.Fatalf("superseded_by mismatch: %v vs %v", prev.SupersededBy, second.ID)
	}
	if prev.SupersededAt == nil {
		t.Fatalf("superseded_at not set")
	}
}

// mustActivate runs the full lifecycle for a fresh predicate and returns the
// activated row. Used by the supersession test to set up the precondition.
func mustActivate(t *testing.T, ctx context.Context, store *frameworkscope.Store, fvID uuid.UUID, name, predicate string) frameworkscope.FrameworkScope {
	t.Helper()
	d, err := store.Create(ctx, frameworkscope.CreateRequest{
		FrameworkVersionID: fvID, Name: name, Predicate: []byte(predicate),
	})
	if err != nil {
		t.Fatalf("Create %s: %v", name, err)
	}
	if _, err := store.Submit(ctx, d.ID); err != nil {
		t.Fatalf("Submit %s: %v", name, err)
	}
	if _, err := store.Approve(ctx, frameworkscope.ApproveRequest{ID: d.ID, ApproverUserID: uuid.NewString()}); err != nil {
		t.Fatalf("Approve %s: %v", name, err)
	}
	a, err := store.Activate(ctx, d.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("Activate %s: %v", name, err)
	}
	return a
}

// TestPredicateEditBouncesApproved — AC-2 trigger test. Approve a row, edit
// its predicate, observe the row is back in `draft` with approval cols
// nulled. This is the load-bearing integration test ADR-0001 §Consequences
// mentions explicitly.
func TestPredicateEditBouncesApproved(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	d, err := store.Create(ctx, frameworkscope.CreateRequest{
		FrameworkVersionID: fvID, Name: "x", Predicate: []byte(`{"op":"true"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Submit(ctx, d.ID); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	approverID := uuid.NewString()
	approved, err := store.Approve(ctx, frameworkscope.ApproveRequest{ID: d.ID, ApproverUserID: approverID})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.State != frameworkscope.StateApproved {
		t.Fatalf("setup approved state = %q", approved.State)
	}
	if approved.ApproverUserID == nil {
		t.Fatalf("setup approver_user_id nil")
	}

	// Patch the predicate. The trigger should bounce back to draft.
	patched, invalidated, err := store.UpdatePredicate(ctx, d.ID, []byte(`{"op":"eq","dim":"environment","value":"prod"}`))
	if err != nil {
		t.Fatalf("UpdatePredicate: %v", err)
	}
	if !invalidated {
		t.Fatalf("approval_invalidated = false; want true")
	}
	if patched.State != frameworkscope.StateDraft {
		t.Fatalf("post-patch state = %q; want draft (trigger should have bounced)", patched.State)
	}
	if patched.ApproverUserID != nil ||
		patched.ApprovedAt != nil ||
		patched.PredicateHashAtApproval != nil ||
		patched.ApprovalEvidenceFileURL != nil ||
		patched.ApprovalEvidenceFileHash != nil {
		t.Fatalf("approval columns not cleared by trigger: %+v", patched)
	}
}

// TestPredicateEditOnDraftDoesNotBounce — AC-2 partial coverage: a predicate
// edit on a `draft` row updates predicate_hash but does NOT change state.
// (The trigger only fires when old.state in review/approved.)
func TestPredicateEditOnDraftDoesNotBounce(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	d, _ := store.Create(ctx, frameworkscope.CreateRequest{
		FrameworkVersionID: fvID, Name: "x", Predicate: []byte(`{"op":"true"}`),
	})
	patched, invalidated, err := store.UpdatePredicate(ctx, d.ID, []byte(`{"op":"eq","dim":"environment","value":"prod"}`))
	if err != nil {
		t.Fatalf("UpdatePredicate: %v", err)
	}
	if invalidated {
		t.Fatalf("approval_invalidated = true on a draft edit; want false")
	}
	if patched.State != frameworkscope.StateDraft {
		t.Fatalf("state = %q; want draft", patched.State)
	}
}

// TestOneActivatedAtATime — AC-3: the partial unique index rejects two
// rows simultaneously in state `activated` for the same (tenant, fv). Tests
// the constraint at the DB level by short-circuiting the supersession path.
func TestOneActivatedAtATime(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	first := mustActivate(t, ctx, store, fvID, "v1", `{"op":"true"}`)

	// Try a direct DB INSERT of a SECOND row already in state `activated`
	// (bypassing the supersession path). The partial unique index must
	// reject it.
	err := withAdminTenant(admin, tenant, func(ctx context.Context, tx pgx.Tx) error {
		// Disable the BYPASSRLS for this insert by setting the role; the
		// admin pool is BYPASSRLS so it would otherwise sail through. We
		// don't need to flip role since the partial unique index applies
		// to every row regardless of role.
		_, err := tx.Exec(ctx, `
			INSERT INTO framework_scopes (
				id, tenant_id, framework_version_id, name,
				state, predicate, predicate_hash, effective_from
			)
			VALUES ($1, $2, $3, 'second-activated', 'activated', '{"op":"true"}'::jsonb, '<placeholder>', now())
		`, uuid.New(), tenant, fvID)
		return err
	})
	if err == nil {
		t.Fatalf("expected unique-violation on second activated row; got nil")
	}
	if !strings.Contains(err.Error(), "framework_scopes_one_active") {
		t.Fatalf("expected partial-unique-index error; got %v", err)
	}

	// First row is still activated.
	got, err := store.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get first: %v", err)
	}
	if got.State != frameworkscope.StateActivated {
		t.Fatalf("first row state = %q; want activated", got.State)
	}
}

// TestRLS_OtherTenantCannotSee — AC-4: a second tenant's reads/writes never
// see this tenant's framework_scopes.
func TestRLS_OtherTenantCannotSee(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	fvA := seedFrameworkVersion(t, admin, tenantA)
	store := frameworkscope.NewStore(app)
	ctxA, _ := tenancy.WithTenant(context.Background(), tenantA)
	ctxB, _ := tenancy.WithTenant(context.Background(), tenantB)

	// Tenant A creates + activates a scope.
	mustActivate(t, ctxA, store, fvA, "A-scope", `{"op":"true"}`)

	// Tenant B's list should return nothing.
	rows, err := store.List(ctxB, frameworkscope.ListFilters{})
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("tenant B saw %d rows from tenant A; RLS bypassed", len(rows))
	}

	// Tenant B's Activated(fvA) should be ErrNotFound (the FK row is in
	// the global namespace? actually fvA is tenant-A-owned, so B can't see
	// it — but the query is by id, so the SELECT returns zero rows).
	_, err = store.Activated(ctxB, fvA)
	if !errors.Is(err, frameworkscope.ErrNotFound) {
		t.Fatalf("Activated B: want ErrNotFound; got %v", err)
	}
}

// ----- HTTP-level smoke tests (AC-5..AC-11) -----

func setupHTTPServer(t *testing.T, tenant string) (*httptest.Server, string, string) {
	t.Helper()
	app := openPool(t, appDSN(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	_, bearer, err := srv.IssueBootstrapCredential(tenant)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	_, approver, err := srv.IssueBootstrapApproverCredential(tenant)
	if err != nil {
		t.Fatalf("IssueBootstrapApproverCredential: %v", err)
	}
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		t.Fatal("HTTPHandlerForTests nil; AttachDB ineffective")
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.Close()
		app.Close()
	})
	return ts, bearer, approver
}

func doJSON(t *testing.T, method, url, bearer, body string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	bb, _ := io.ReadAll(resp.Body)
	return resp, bb
}

// TestHTTP_FullLifecycle — AC-5 through AC-8 over the HTTP wire, plus
// AC-9 (PATCH banner) and AC-10 (filter by state).
func TestHTTP_FullLifecycle(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)

	ts, bearer, approver := setupHTTPServer(t, tenant)

	// AC-5: create draft.
	body := fmt.Sprintf(`{"framework_version_id":%q,"name":"SOC 2 system","predicate":{"op":"true"}}`, fvID.String())
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/framework-scopes", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST create: status %d; body %s", resp.StatusCode, payload)
	}
	var createdResp struct {
		FrameworkScope map[string]any `json:"framework_scope"`
	}
	if err := json.Unmarshal(payload, &createdResp); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	id := createdResp.FrameworkScope["id"].(string)
	if createdResp.FrameworkScope["state"] != "draft" {
		t.Fatalf("state %v; want draft", createdResp.FrameworkScope["state"])
	}

	// AC-6: submit.
	resp, payload = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/submit", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH submit: status %d; body %s", resp.StatusCode, payload)
	}

	// AC-7: approve — bearer (non-approver) should be 403.
	resp, _ = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/approve", bearer, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("approve with non-approver: status %d; want 403", resp.StatusCode)
	}
	// Approver succeeds.
	resp, payload = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/approve", approver, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH approve: status %d; body %s", resp.StatusCode, payload)
	}

	// AC-9: PATCH the predicate while in `approved`. Server must return
	// approval_invalidated: true and state must be back to draft.
	patchBody := `{"predicate":{"op":"eq","dim":"environment","value":"prod"}}`
	resp, payload = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id, bearer, patchBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH predicate: status %d; body %s", resp.StatusCode, payload)
	}
	var patchResp struct {
		FrameworkScope      map[string]any `json:"framework_scope"`
		ApprovalInvalidated bool           `json:"approval_invalidated"`
	}
	if err := json.Unmarshal(payload, &patchResp); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if !patchResp.ApprovalInvalidated {
		t.Fatalf("approval_invalidated = false; want true")
	}
	if patchResp.FrameworkScope["state"] != "draft" {
		t.Fatalf("post-patch state = %v; want draft", patchResp.FrameworkScope["state"])
	}

	// Drive back through submit + approve + activate.
	resp, _ = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/submit", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-submit: %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/approve", approver, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-approve: %d", resp.StatusCode)
	}
	activateBody := fmt.Sprintf(`{"effective_from":%q}`, time.Now().UTC().Format(time.RFC3339))
	resp, payload = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/activate", bearer, activateBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH activate: status %d; body %s", resp.StatusCode, payload)
	}

	// AC-10: list filtered by state.
	resp, payload = doJSON(t, http.MethodGet, ts.URL+"/v1/framework-scopes?framework_version="+fvID.String()+"&state=activated", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET filtered list: %d", resp.StatusCode)
	}
	var listResp struct {
		FrameworkScopes []map[string]any `json:"framework_scopes"`
	}
	if err := json.Unmarshal(payload, &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(listResp.FrameworkScopes) != 1 || listResp.FrameworkScopes[0]["state"] != "activated" {
		t.Fatalf("filtered list = %+v; want one activated row", listResp.FrameworkScopes)
	}
}

// TestHTTP_ApproveRejectsBadFileHash — AC-7 anti-criterion: we record the
// uploaded file's hash but reject anything that isn't a 64-char hex sha256
// so clients can't pass garbage placeholders.
func TestHTTP_ApproveRejectsBadFileHash(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)

	createBody := fmt.Sprintf(`{"framework_version_id":%q,"name":"x","predicate":{"op":"true"}}`, fvID.String())
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/framework-scopes", bearer, createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	var c struct {
		FrameworkScope map[string]any `json:"framework_scope"`
	}
	_ = json.Unmarshal(payload, &c)
	id := c.FrameworkScope["id"].(string)
	_, _ = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/submit", bearer, "")

	bad := `{"approval_evidence_file_url":"s3://bucket/key","approval_evidence_file_hash":"not-a-hash"}`
	resp, _ = doJSON(t, http.MethodPatch, ts.URL+"/v1/framework-scopes/"+id+"/approve", approver, bad)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d; want 400", resp.StatusCode)
	}
}

// TestHTTP_EffectiveScope — AC-11. End-to-end intersection: control with
// applicability over 2 cells AND a framework scope predicate that narrows
// further produces only the intersection.
func TestHTTP_EffectiveScope(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)

	// Control applicability: in (prod, staging).
	controlID, _ := seedScopeAndControl(t, admin, app, tenant,
		`{"op":"in","dim":"environment","values":["prod","staging"]}`)

	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	// FrameworkScope predicate: data_classification = restricted only.
	mustActivate(t, ctx, store, fvID, "soc2", `{"op":"eq","dim":"data_classification","value":"restricted"}`)

	ts, bearer, _ := setupHTTPServer(t, tenant)
	url := fmt.Sprintf("%s/v1/controls/%s/effective-scope?framework_version=%s", ts.URL, controlID, fvID)
	resp, payload := doJSON(t, http.MethodGet, url, bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET effective-scope: status %d; body %s", resp.StatusCode, payload)
	}
	var got struct {
		EffectiveScopeCount int    `json:"effective_scope_count"`
		InScope             bool   `json:"in_scope"`
		FrameworkScopeID    string `json:"framework_scope_id"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Intersection: prod-restricted exists (yes); staging-confidential and dev-public don't have data_classification=restricted.
	// So we expect exactly 1 cell.
	if got.EffectiveScopeCount != 1 {
		t.Fatalf("effective_scope_count = %d; want 1; body=%s", got.EffectiveScopeCount, payload)
	}
	if !got.InScope {
		t.Fatalf("in_scope = false; want true")
	}
}

// TestHTTP_EffectiveScope_NoActivatedFrameworkScope — AC-11 anti-criterion:
// when no framework_scope is activated for the framework_version, the
// control is "out of scope" — empty effective_scope; coverage downstream
// will read this as `n/a`, NOT `fail`.
func TestHTTP_EffectiveScope_NoActivatedFrameworkScope(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	controlID, _ := seedScopeAndControl(t, admin, app, tenant, `{"op":"true"}`)

	ts, bearer, _ := setupHTTPServer(t, tenant)
	url := fmt.Sprintf("%s/v1/controls/%s/effective-scope?framework_version=%s", ts.URL, controlID, fvID)
	resp, payload := doJSON(t, http.MethodGet, url, bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d; body %s", resp.StatusCode, payload)
	}
	var got struct {
		EffectiveScopeCount int    `json:"effective_scope_count"`
		InScope             bool   `json:"in_scope"`
		OutOfScopeReason    string `json:"out_of_scope_reason"`
	}
	_ = json.Unmarshal(payload, &got)
	if got.EffectiveScopeCount != 0 || got.InScope {
		t.Fatalf("expected empty + out_of_scope; got count=%d in_scope=%v", got.EffectiveScopeCount, got.InScope)
	}
	if got.OutOfScopeReason == "" {
		t.Fatalf("out_of_scope_reason should explain the missing framework_scope")
	}
}

// TestHTTP_AsOf — AC-13: a historical query returns the row that was active
// at the supplied timestamp.
func TestHTTP_AsOf(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)

	// First active row from t0..t1.
	first := mustActivate(t, ctx, store, fvID, "v1", `{"op":"true"}`)
	t.Logf("first effective_from = %v", first.EffectiveFrom)
	time.Sleep(50 * time.Millisecond)
	midpoint := time.Now().UTC()
	time.Sleep(50 * time.Millisecond)

	// Second active row supersedes the first.
	second := mustActivate(t, ctx, store, fvID, "v2", `{"op":"eq","dim":"environment","value":"prod"}`)
	t.Logf("second effective_from = %v", second.EffectiveFrom)

	ts, bearer, _ := setupHTTPServer(t, tenant)

	// AsOf the midpoint should return the FIRST row.
	url := fmt.Sprintf("%s/v1/framework-scopes?framework_version=%s&as_of=%s",
		ts.URL, fvID, midpoint.Format(time.RFC3339Nano))
	resp, payload := doJSON(t, http.MethodGet, url, bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("as_of midpoint: %d; body %s", resp.StatusCode, payload)
	}
	var got struct {
		FrameworkScopes []map[string]any `json:"framework_scopes"`
	}
	_ = json.Unmarshal(payload, &got)
	if len(got.FrameworkScopes) != 1 {
		t.Fatalf("as_of returned %d rows; want 1; body=%s", len(got.FrameworkScopes), payload)
	}
	if got.FrameworkScopes[0]["id"] != first.ID.String() {
		t.Fatalf("as_of returned wrong row: %v; want %s", got.FrameworkScopes[0]["id"], first.ID)
	}
}

// TestHTTP_DefaultScopeSeed — AC-12: a SOC 2 default predicate `true`
// activated row works as the safe default. (Not a real seed of the SOC 2
// framework — the slice ships the column shape; seeding is the deploy-time
// concern handled by ops scripts. This test exercises the path.)
func TestHTTP_DefaultScopeSeed(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin, tenant)
	store := frameworkscope.NewStore(app)
	ctx, _ := tenancy.WithTenant(context.Background(), tenant)
	defaultScope := mustActivate(t, ctx, store, fvID, "soc2-default", `true`)
	if defaultScope.State != frameworkscope.StateActivated {
		t.Fatalf("default seed state = %q", defaultScope.State)
	}
	// The predicate round-trips through jsonb which may reflow whitespace.
	// The load-bearing invariant is predicate_hash: the application canon
	// is `{"op":"true"}`, hashed; the row's stored hash must equal that.
	_, wantHash, err := frameworkscope.Canonicalize([]byte(`{"op":"true"}`))
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	if defaultScope.PredicateHash != wantHash {
		t.Fatalf("default predicate_hash = %q; want %q", defaultScope.PredicateHash, wantHash)
	}
}
