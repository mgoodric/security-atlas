//go:build integration

package scfimport_test

// #1776: a person decides SCF remap proposals. The fixtures are
// testdata/remap-decide-release-{a,b}.json: against release A, release B
// renumbers DEC-01 and DEC-05 with their titles (unchanged), renames DEC-02
// (renamed), splits DEC-03 (split) and drops DEC-04 (no_successor). None of
// them keeps its code, so the #1971 D3 upgrade moves nothing and every
// proposal starts open.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	apicontrols "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	decideReleaseA = "test-decide-a"
	decideReleaseB = "test-decide-b"
)

var decideCodes = []string{"DEC-01", "DEC-02", "DEC-03", "DEC-04", "DEC-05"}

// decideFixture is one tenant's controls on release A, with B imported and
// the remap queue built.
type decideFixture struct {
	admin, app *pgxpool.Pool
	tenant     string
	controls   map[string]uuid.UUID // by legacy code
	proposals  map[string]string    // proposal id by legacy code
}

func decideBundleYAML(code string) string {
	return `bundle_schema_version: "1"
bundle_id: decide_1776_` + strings.ToLower(strings.ReplaceAll(code, "-", "_")) + `
title: "Control on ` + code + `"
scf_anchor_id: ` + code + `
implementation_type: manual_attested
`
}

func withTenant(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedDecideControls uploads one unpinned control per release-A code
// through the control store, so each has a real, re-parseable manifest.
func seedDecideControls(t *testing.T, app *pgxpool.Pool, tenant string) map[string]uuid.UUID {
	t.Helper()
	store := control.NewStore(app)
	out := map[string]uuid.UUID{}
	for _, code := range decideCodes {
		b, err := control.FinalizeBundleForHTTP([]byte(decideBundleYAML(code)))
		if err != nil {
			t.Fatalf("bundle %s: %v", code, err)
		}
		res, err := store.Upload(withTenant(t, tenant), b, "seed-1776")
		if err != nil {
			t.Fatalf("upload %s: %v", code, err)
		}
		out[code] = res.ControlID
	}
	return out
}

func proposalsByCode(t *testing.T, admin *pgxpool.Pool, tenant string) map[string]string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT legacy_scf_id, id::text FROM scf_remap_proposals WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("read proposals: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, id string
		if err := rows.Scan(&code, &id); err != nil {
			t.Fatalf("scan proposal: %v", err)
		}
		out[code] = id
	}
	return out
}

// setupDecide loads both releases and seeds the given tenants (the first is
// the fixture's own). It returns one fixture per tenant.
func setupDecide(t *testing.T, tenants int) []decideFixture {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	truncateCatalog(t, admin)

	cat := func(name string) *scfimport.Catalog {
		c, err := scfimport.Load("testdata/remap-decide-release-" + name + ".json")
		if err != nil {
			t.Fatalf("Load %s: %v", name, err)
		}
		return c
	}
	importRelease(t, admin, cat("a"))
	var fx []decideFixture
	for i := 0; i < tenants; i++ {
		tenant := dbtest.SeedTenant(t, admin, "controls")
		fx = append(fx, decideFixture{admin: admin, app: app, tenant: tenant, controls: seedDecideControls(t, app, tenant)})
	}
	importRelease(t, admin, cat("b"))
	if _, err := scfimport.RebindControls(context.Background(), admin); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	for i := range fx {
		fx[i].proposals = proposalsByCode(t, admin, fx[i].tenant)
		if len(fx[i].proposals) != len(decideCodes) {
			t.Fatalf("tenant %d has %d proposals; want %d", i, len(fx[i].proposals), len(decideCodes))
		}
	}
	return fx
}

type proposalState struct {
	status string
	target *uuid.UUID
}

func decideProposal(t *testing.T, admin *pgxpool.Pool, id string) proposalState {
	t.Helper()
	var s proposalState
	if err := admin.QueryRow(context.Background(),
		`SELECT status, target_anchor_id FROM scf_remap_proposals WHERE id = $1`, id).Scan(&s.status, &s.target); err != nil {
		t.Fatalf("read proposal %s: %v", id, err)
	}
	return s
}

// activeControl returns the active version of a bundle: its id, version and
// anchor.
func activeControl(t *testing.T, admin *pgxpool.Pool, tenant string, legacyID uuid.UUID) (uuid.UUID, int32, uuid.UUID) {
	t.Helper()
	var id, anchor uuid.UUID
	var version int32
	if err := admin.QueryRow(context.Background(), `
		SELECT c.id, c.version, c.scf_anchor_id FROM controls c
		WHERE c.tenant_id = $1 AND c.superseded_by IS NULL
		  AND c.bundle_id = (SELECT bundle_id FROM controls WHERE id = $2)`,
		tenant, legacyID).Scan(&id, &version, &anchor); err != nil {
		t.Fatalf("active control for %s: %v", legacyID, err)
	}
	return id, version, anchor
}

func decisionCount(t *testing.T, admin *pgxpool.Pool, proposalID string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM scf_remap_decisions WHERE proposal_id = $1`, proposalID).Scan(&n); err != nil {
		t.Fatalf("count decisions: %v", err)
	}
	return n
}

func TestRemapDecide_ApproveUnchangedReanchorsAsNewRevisionWithHistory(t *testing.T) {
	f := setupDecide(t, 1)[0]
	store := control.NewStore(f.app)
	ctx := withTenant(t, f.tenant)
	approver := uuid.NewString()
	legacy := f.controls["DEC-01"]
	legacyAnchor := anchorID(t, f.admin, decideReleaseA, "DEC-01")
	newAnchor := anchorID(t, f.admin, decideReleaseB, "DEC-10")

	d, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals["DEC-01"], DecidedBy: approver})
	if err != nil {
		t.Fatalf("ApproveRemap: %v", err)
	}

	// A superseding revision, not an in-place edit: the legacy row keeps
	// its anchor and points at the new version.
	activeID, version, anchor := activeControl(t, f.admin, f.tenant, legacy)
	if activeID == legacy || version != 2 || anchor != newAnchor {
		t.Fatalf("active version = %s v%d on %s; want a new v2 row on %s", activeID, version, anchor, newAnchor)
	}
	var supersededBy uuid.UUID
	var oldAnchor uuid.UUID
	if err := f.admin.QueryRow(context.Background(),
		`SELECT superseded_by, scf_anchor_id FROM controls WHERE id = $1`, legacy).Scan(&supersededBy, &oldAnchor); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if supersededBy != activeID || oldAnchor != legacyAnchor {
		t.Errorf("legacy row superseded_by=%s anchor=%s; want %s on %s", supersededBy, oldAnchor, activeID, legacyAnchor)
	}
	var manifest string
	if err := f.admin.QueryRow(context.Background(),
		`SELECT bundle_manifest_yaml FROM controls WHERE id = $1`, activeID).Scan(&manifest); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(manifest, "scf_anchor_id: DEC-10") || !strings.Contains(manifest, "scf_release: "+decideReleaseB) {
		t.Errorf("new manifest does not name DEC-10 in %s:\n%s", decideReleaseB, manifest)
	}

	if p := decideProposal(t, f.admin, f.proposals["DEC-01"]); p.status != "approved" || p.target == nil || *p.target != newAnchor {
		t.Errorf("proposal = %+v; want approved targeting %s", p, newAnchor)
	}

	// The anchor history: old release and anchor, new release and anchor,
	// the new revision, approver and time.
	var h struct {
		decision, legacyRelease, newRelease, newSCF, by    string
		legacyAnchor, newAnchor, legacyControl, newControl uuid.UUID
		bulk, hasTime                                      bool
	}
	if err := f.admin.QueryRow(context.Background(), `
		SELECT decision, legacy_release, new_release, new_scf_id, decided_by,
		       legacy_anchor_id, new_anchor_id, legacy_control_id, new_control_id,
		       bulk, decided_at IS NOT NULL
		FROM scf_remap_decisions WHERE proposal_id = $1`, f.proposals["DEC-01"]).Scan(
		&h.decision, &h.legacyRelease, &h.newRelease, &h.newSCF, &h.by,
		&h.legacyAnchor, &h.newAnchor, &h.legacyControl, &h.newControl, &h.bulk, &h.hasTime); err != nil {
		t.Fatalf("read history: %v", err)
	}
	if h.decision != "approved" || h.legacyRelease != decideReleaseA || h.newRelease != decideReleaseB ||
		h.newSCF != "DEC-10" || h.by != approver || h.legacyAnchor != legacyAnchor || h.newAnchor != newAnchor ||
		h.legacyControl != legacy || h.newControl != activeID || h.bulk || !h.hasTime {
		t.Errorf("history = %+v", h)
	}
	if d.DecidedBy != approver || d.NewControlID != activeID.String() || d.DecidedAt == "" {
		t.Errorf("decision response = %+v", d)
	}

	// Approval is final.
	if _, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals["DEC-01"], DecidedBy: approver}); !errors.Is(err, control.ErrRemapProposalDecided) {
		t.Errorf("second approve: err = %v; want ErrRemapProposalDecided", err)
	}
	if _, err := store.RejectRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-01"], DecidedBy: approver}); !errors.Is(err, control.ErrRemapProposalDecided) {
		t.Errorf("reject after approve: err = %v; want ErrRemapProposalDecided", err)
	}
}

func TestRemapDecide_SplitNeedsAChosenAnchor(t *testing.T) {
	f := setupDecide(t, 1)[0]
	store := control.NewStore(f.app)
	ctx := withTenant(t, f.tenant)
	approver := uuid.NewString()
	legacy := f.controls["DEC-03"]
	legacyAnchor := anchorID(t, f.admin, decideReleaseA, "DEC-03")

	for _, code := range []string{"DEC-03", "DEC-04"} { // split, no_successor
		_, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals[code], DecidedBy: approver})
		if !errors.Is(err, control.ErrRemapAnchorRequired) {
			t.Errorf("%s approve without an anchor: err = %v; want ErrRemapAnchorRequired", code, err)
		}
	}
	if _, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals["DEC-03"], SCFID: "DEC-99", DecidedBy: approver}); !errors.Is(err, control.ErrRemapAnchorNotInRelease) {
		t.Errorf("approve with a code outside release B: err = %v; want ErrRemapAnchorNotInRelease", err)
	}
	if id, v, a := activeControl(t, f.admin, f.tenant, legacy); id != legacy || v != 1 || a != legacyAnchor {
		t.Fatalf("failed approvals moved the control: %s v%d on %s", id, v, a)
	}
	if p := decideProposal(t, f.admin, f.proposals["DEC-03"]); p.status != "open" || decisionCount(t, f.admin, f.proposals["DEC-03"]) != 0 {
		t.Fatalf("failed approvals changed the proposal: %+v", p)
	}

	d, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals["DEC-03"], SCFID: "DEC-31", DecidedBy: approver})
	if err != nil {
		t.Fatalf("approve split with DEC-31: %v", err)
	}
	want := anchorID(t, f.admin, decideReleaseB, "DEC-31")
	if _, v, a := activeControl(t, f.admin, f.tenant, legacy); v != 2 || a != want {
		t.Errorf("split approval left the control v%d on %s; want v2 on DEC-31 %s", v, a, want)
	}
	if d.NewSCFID != "DEC-31" || d.NewRelease != decideReleaseB {
		t.Errorf("decision = %+v", d)
	}
}

func TestRemapDecide_BulkApprovesOnlyOpenUnchanged(t *testing.T) {
	f := setupDecide(t, 1)[0]
	store := control.NewStore(f.app)
	ctx := withTenant(t, f.tenant)
	approver := uuid.NewString()

	// A person rejected DEC-05's unchanged proposal; bulk must not undo it.
	if _, err := store.RejectRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-05"], DecidedBy: approver}); err != nil {
		t.Fatalf("reject DEC-05: %v", err)
	}

	// While another transaction holds an unchanged proposal's lock, bulk
	// approve refuses rather than queueing.
	hold, err := f.admin.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := hold.Exec(context.Background(), `SELECT 1 FROM scf_remap_proposals WHERE id = $1 FOR UPDATE`, f.proposals["DEC-01"]); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	if _, err := store.BulkApproveUnchangedRemaps(ctx, approver); !errors.Is(err, control.ErrRemapBulkRunning) {
		t.Errorf("bulk while locked: err = %v; want ErrRemapBulkRunning", err)
	}
	_ = hold.Rollback(context.Background())

	res, err := store.BulkApproveUnchangedRemaps(ctx, approver)
	if err != nil {
		t.Fatalf("bulk approve: %v", err)
	}
	if res.ApprovedCount != 1 || len(res.Approved) != 1 || res.Approved[0].ProposalID != f.proposals["DEC-01"] || !res.Approved[0].Bulk {
		t.Fatalf("bulk result = %+v; want DEC-01 only", res)
	}
	wantStatus := map[string]string{"DEC-01": "approved", "DEC-02": "open", "DEC-03": "open", "DEC-04": "open", "DEC-05": "rejected"}
	for code, want := range wantStatus {
		if got := decideProposal(t, f.admin, f.proposals[code]).status; got != want {
			t.Errorf("%s: status %s; want %s", code, got, want)
		}
		if code == "DEC-01" {
			continue
		}
		if id, _, a := activeControl(t, f.admin, f.tenant, f.controls[code]); id != f.controls[code] || a != anchorID(t, f.admin, decideReleaseA, code) {
			t.Errorf("%s: bulk moved the control to %s on %s", code, id, a)
		}
	}

	// Idempotent: a second run finds nothing to approve.
	again, err := store.BulkApproveUnchangedRemaps(ctx, approver)
	if err != nil || again.ApprovedCount != 0 {
		t.Errorf("second bulk: %+v, %v; want nothing approved", again, err)
	}
	if n := decisionCount(t, f.admin, f.proposals["DEC-01"]); n != 1 {
		t.Errorf("DEC-01 has %d history rows; want 1", n)
	}
}

func TestRemapDecide_RejectIsRevisitableKeepLegacyIsFinal(t *testing.T) {
	f := setupDecide(t, 1)[0]
	store := control.NewStore(f.app)
	ctx := withTenant(t, f.tenant)
	approver := uuid.NewString()
	legacyAnchor := anchorID(t, f.admin, decideReleaseA, "DEC-02")

	d, err := store.RejectRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-02"], Reason: "the rename changes scope", DecidedBy: approver})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if d.Decision != "rejected" || d.DecidedBy != approver || d.Reason != "the rename changes scope" {
		t.Errorf("reject decision = %+v", d)
	}
	if id, v, a := activeControl(t, f.admin, f.tenant, f.controls["DEC-02"]); id != f.controls["DEC-02"] || v != 1 || a != legacyAnchor {
		t.Errorf("reject moved the control: %s v%d on %s", id, v, a)
	}
	if p := decideProposal(t, f.admin, f.proposals["DEC-02"]); p.status != "rejected" || p.target != nil {
		t.Errorf("proposal after reject = %+v", p)
	}
	if _, err := store.RejectRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-02"], DecidedBy: approver}); !errors.Is(err, control.ErrRemapProposalDecided) {
		t.Errorf("second reject: err = %v; want ErrRemapProposalDecided", err)
	}

	// Revisited: a rejected proposal can still be approved...
	if _, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals["DEC-02"], DecidedBy: approver}); err != nil {
		t.Fatalf("approve after reject: %v", err)
	}
	if n := decisionCount(t, f.admin, f.proposals["DEC-02"]); n != 2 {
		t.Errorf("DEC-02 has %d history rows; want reject + approve", n)
	}

	// ...or kept legacy, which is final.
	if _, err := store.RejectRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-04"], DecidedBy: approver}); err != nil {
		t.Fatalf("reject DEC-04: %v", err)
	}
	if _, err := store.KeepLegacyRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-04"], DecidedBy: approver}); err != nil {
		t.Fatalf("keep legacy after reject: %v", err)
	}
	if p := decideProposal(t, f.admin, f.proposals["DEC-04"]); p.status != "kept_legacy" {
		t.Errorf("DEC-04 status %s; want kept_legacy", p.status)
	}
	for name, err := range map[string]error{
		"approve": func() error {
			_, err := store.ApproveRemap(ctx, control.RemapApproveInput{ProposalID: f.proposals["DEC-04"], SCFID: "DEC-30", DecidedBy: approver})
			return err
		}(),
		"reject": func() error {
			_, err := store.RejectRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-04"], DecidedBy: approver})
			return err
		}(),
		"keep legacy": func() error {
			_, err := store.KeepLegacyRemap(ctx, control.RemapCloseInput{ProposalID: f.proposals["DEC-04"], DecidedBy: approver})
			return err
		}(),
	} {
		if !errors.Is(err, control.ErrRemapProposalDecided) {
			t.Errorf("%s after keep legacy: err = %v; want ErrRemapProposalDecided", name, err)
		}
	}
	if id, v, a := activeControl(t, f.admin, f.tenant, f.controls["DEC-04"]); id != f.controls["DEC-04"] || v != 1 || a != anchorID(t, f.admin, decideReleaseA, "DEC-04") {
		t.Errorf("keep legacy moved the control: %s v%d on %s", id, v, a)
	}
}

// decideAPI serves the decision and list routes as tenant's caller.
func decideAPI(t *testing.T, app *pgxpool.Pool, cred credstore.Credential) http.Handler {
	t.Helper()
	store := control.NewStore(app)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), cred)
			ctx, err := tenancy.WithTenant(ctx, cred.TenantID)
			if err != nil {
				t.Errorf("WithTenant: %v", err)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/controls/remap-proposals", apicontrols.NewRemapProposalsHandler(store).List)
	apicontrols.NewRemapDecisionsHandler(store).RegisterRoutes(r)
	return r
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func personCred(tenant string) credstore.Credential {
	return credstore.Credential{ID: "key_" + uuid.NewString(), TenantID: tenant, UserID: uuid.NewString()}
}

func TestRemapDecideAPI_OtherTenantCannotSeeOrActOnProposals(t *testing.T) {
	fx := setupDecide(t, 2)
	mine, theirs := fx[0], fx[1]
	other := decideAPI(t, mine.app, personCred(theirs.tenant))

	// The other tenant's list holds only its own proposals.
	rr := call(t, other, http.MethodGet, "/v1/controls/remap-proposals", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var list struct {
		Proposals []struct {
			ID string `json:"id"`
		} `json:"proposals"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	minesIDs := map[string]bool{}
	for _, id := range mine.proposals {
		minesIDs[id] = true
	}
	for _, p := range list.Proposals {
		if minesIDs[p.ID] {
			t.Errorf("other tenant lists proposal %s", p.ID)
		}
	}

	// Every decision on another tenant's proposal is a 404 and changes
	// nothing.
	for _, c := range []struct{ path, body string }{
		{"/v1/controls/remap-proposals/" + mine.proposals["DEC-01"] + "/approve", ""},
		{"/v1/controls/remap-proposals/" + mine.proposals["DEC-03"] + "/approve", `{"scf_id":"DEC-30"}`},
		{"/v1/controls/remap-proposals/" + mine.proposals["DEC-02"] + "/reject", `{"reason":"x"}`},
		{"/v1/controls/remap-proposals/" + mine.proposals["DEC-04"] + "/keep-legacy", ""},
	} {
		if rr := call(t, other, http.MethodPost, c.path, c.body); rr.Code != http.StatusNotFound {
			t.Errorf("POST %s as other tenant: %d %s; want 404", c.path, rr.Code, rr.Body.String())
		}
	}
	// The other tenant's bulk approve approves its own, never mine.
	rr = call(t, other, http.MethodPost, "/v1/controls/remap-proposals/approve-unchanged", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("other bulk: %d %s", rr.Code, rr.Body.String())
	}
	var bulk control.RemapBulkResult
	if err := json.Unmarshal(rr.Body.Bytes(), &bulk); err != nil {
		t.Fatalf("decode bulk: %v", err)
	}
	for _, d := range bulk.Approved {
		if minesIDs[d.ProposalID] {
			t.Errorf("other tenant's bulk approved my proposal %s", d.ProposalID)
		}
	}
	if bulk.ApprovedCount != 2 {
		t.Errorf("other tenant's bulk approved %d; want its own 2", bulk.ApprovedCount)
	}
	for code, id := range mine.proposals {
		if p := decideProposal(t, mine.admin, id); p.status != "open" || decisionCount(t, mine.admin, id) != 0 {
			t.Errorf("%s: my proposal is %s after the other tenant's calls; want open, no history", code, p.status)
		}
		if cid, _, _ := activeControl(t, mine.admin, mine.tenant, mine.controls[code]); cid != mine.controls[code] {
			t.Errorf("%s: my control was re-versioned by the other tenant", code)
		}
	}

	// RLS, not only the handler: as the other tenant, the app role cannot
	// update my proposal nor write history into my tenant.
	ctx := context.Background()
	tx, err := mine.app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant', $1, true)`, theirs.tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE scf_remap_proposals SET status = 'kept_legacy' WHERE id = $1`, mine.proposals["DEC-05"])
	if err != nil || tag.RowsAffected() != 0 {
		t.Errorf("cross-tenant UPDATE: rows=%d err=%v; want 0 rows", tag.RowsAffected(), err)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO scf_remap_decisions (tenant_id, proposal_id, decision, legacy_control_id,
			legacy_anchor_id, legacy_scf_id, legacy_release, decided_by)
		VALUES ($1, $2, 'kept_legacy', $3, $4, 'DEC-05', $5, 'intruder')`,
		mine.tenant, mine.proposals["DEC-05"], mine.controls["DEC-05"],
		anchorID(t, mine.admin, decideReleaseA, "DEC-05"), decideReleaseA)
	if err == nil {
		t.Error("cross-tenant INSERT into scf_remap_decisions succeeded; want an RLS violation")
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT s`); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
	// History is append-only for the app role.
	if _, err := tx.Exec(ctx, `DELETE FROM scf_remap_decisions`); err == nil {
		t.Error("app role DELETE on scf_remap_decisions succeeded; want permission denied")
	}
}

func TestRemapDecideAPI_DecisionsNeedAPerson(t *testing.T) {
	f := setupDecide(t, 1)[0]
	machine := decideAPI(t, f.app, credstore.Credential{ID: "key_machine", TenantID: f.tenant, UserID: "key_machine"})
	for _, path := range []string{
		"/v1/controls/remap-proposals/" + f.proposals["DEC-01"] + "/approve",
		"/v1/controls/remap-proposals/" + f.proposals["DEC-02"] + "/reject",
		"/v1/controls/remap-proposals/" + f.proposals["DEC-04"] + "/keep-legacy",
		"/v1/controls/remap-proposals/approve-unchanged",
	} {
		if rr := call(t, machine, http.MethodPost, path, ""); rr.Code != http.StatusForbidden {
			t.Errorf("machine POST %s: %d; want 403", path, rr.Code)
		}
	}

	person := personCred(f.tenant)
	api := decideAPI(t, f.app, person)
	if rr := call(t, api, http.MethodPost, "/v1/controls/remap-proposals/"+f.proposals["DEC-03"]+"/approve", ""); rr.Code != http.StatusBadRequest {
		t.Errorf("split approve without scf_id: %d; want 400", rr.Code)
	}
	rr := call(t, api, http.MethodPost, "/v1/controls/remap-proposals/"+f.proposals["DEC-03"]+"/approve", `{"scf_id":"DEC-30"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("split approve: %d %s", rr.Code, rr.Body.String())
	}
	var d control.RemapDecision
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// PrincipalOf a person is the bare users(id).
	if d.DecidedBy != person.UserID {
		t.Errorf("decided_by = %q; want the person %q", d.DecidedBy, person.UserID)
	}
	if rr := call(t, api, http.MethodPost, "/v1/controls/remap-proposals/"+f.proposals["DEC-03"]+"/reject", ""); rr.Code != http.StatusConflict {
		t.Errorf("reject an approved proposal: %d; want 409", rr.Code)
	}
	if rr := call(t, api, http.MethodGet, "/v1/controls/remap-proposals?status=approved", ""); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), f.proposals["DEC-03"]) {
		t.Errorf("list ?status=approved: %d %s", rr.Code, rr.Body.String())
	}
}
