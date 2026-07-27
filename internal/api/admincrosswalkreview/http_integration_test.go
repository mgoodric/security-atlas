//go:build integration

// Integration tests for the slice 536b admin crosswalk review/edit surface
// (GET /v1/admin/crosswalk-review, PATCH /v1/admin/crosswalk-edges/{id},
// GET /v1/admin/crosswalk-edges/{id}/audit). Requires DATABASE_URL_APP
// (atlas_app — the role holding the slice-536b widened column grant) +
// DATABASE_URL (BYPASSRLS, for seeding catalog rows). Proves against real
// Postgres:
//
//   - Every content edit writes an append-only audit row, in the same
//     transaction as the change. This is the slice's load-bearing acceptance
//     criterion, so it is asserted on the happy path AND on every refusal arm
//     (a rejected edit writes no row and changes no column).
//   - Editing a `verified` mapping demotes it to under_review through slice
//     483's state machine, writing THAT machine's audit row too (D-536b-1).
//     No second lifecycle: the tier moves only through crosswalktier.
//   - Nothing here promotes a mapping. The only path to `verified` remains
//     483's POST .../tier, driven by a human.
//   - Constitutional invariant #7 is enforced by the GRANT, not only by Go:
//     atlas_app cannot UPDATE framework_requirement_id or scf_anchor_id, so no
//     edit — and no future code path running as the app role — can re-point an
//     edge.
//   - Threat-model S/E: a non-admin caller is 403 on every route.
//
// fw_to_scf_edges and both audit tables are CATALOG tables (no tenant_id, no
// RLS), so the handlers run WITHOUT tenancymw — the gate is the admin-role
// authz check, exercised here through the real authctx credential the
// production /v1 chain injects.
package admincrosswalkreview_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/admincrosswalkreview"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/crosswalkedit"
	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping admincrosswalkreview integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, appURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New app: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	if adminURL := os.Getenv("DATABASE_URL"); adminURL != "" {
		a, aerr := pgxpool.New(ctx, adminURL)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", aerr)
			os.Exit(1)
		}
		adminPool = a
	}
	code := m.Run()
	p.Close()
	if adminPool != nil {
		adminPool.Close()
	}
	os.Exit(code)
}

type seed struct {
	FrameworkVersionID uuid.UUID
	RequirementID      uuid.UUID
	AnchorID           uuid.UUID
	EdgeID             uuid.UUID
}

// seedEdge seeds a catalog chain (framework -> version -> requirement -> anchor
// -> edge) at the given tier. Mirrors the slice-483 suite's seeder; catalog rows
// are global, so cleanup is scoped to this run's unique ids.
func seedEdge(t *testing.T, tag string, tier crosswalktier.Tier) seed {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	ctx := context.Background()
	uniq := tag + "-" + uuid.NewString()[:8]

	fwID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, 'slice536b test fw', 'test', '')
	`, fwID, uniq); err != nil {
		t.Fatalf("seed framework: %v", err)
	}

	verID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2024', 'current')
	`, verID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}

	requirementID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'CC-536B', 'slice536b requirement', '')
	`, requirementID, verID); err != nil {
		t.Fatalf("seed requirement: %v", err)
	}

	scfVerID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'scf-2024', 'current')
	`, scfVerID, fwID); err != nil {
		t.Fatalf("seed scf framework_version: %v", err)
	}
	anchorID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		VALUES ($1, $2, $3, 'IAC', 'slice536b anchor', '')
	`, anchorID, scfVerID, "IAC-"+uniq); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	edgeID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO fw_to_scf_edges
			(id, framework_requirement_id, scf_anchor_id, relationship_type, strength,
			 source_attribution, rationale, mapping_tier)
		VALUES ($1, $2, $3, 'subset_of', 0.8, 'community_draft', 'seeded rationale', $4)
	`, edgeID, requirementID, anchorID, string(tier)); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		// Both audit tables cascade on edge delete; the edge cascades on
		// requirement/anchor delete. Delete from the leaves up.
		_, _ = adminPool.Exec(c, `DELETE FROM fw_to_scf_edges WHERE id = $1`, edgeID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_requirements WHERE id = $1`, requirementID)
		_, _ = adminPool.Exec(c, `DELETE FROM scf_anchors WHERE id = $1`, anchorID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_versions WHERE id IN ($1, $2)`, verID, scfVerID)
		_, _ = adminPool.Exec(c, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return seed{FrameworkVersionID: verID, RequirementID: requirementID, AnchorID: anchorID, EdgeID: edgeID}
}

// newRouter wires the handlers behind a credential-injecting middleware exactly
// like the production /v1 chain (minus tenancymw — catalog tables, no RLS).
func newRouter(isAdmin bool, editorID string) http.Handler {
	h := admincrosswalkreview.New(
		dbx.New(appPool),
		crosswalkedit.NewStore(appPool),
		crosswalktier.NewStore(appPool),
	)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:      "key_test",
				IsAdmin: isAdmin,
				UserID:  editorID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/admin/crosswalk-review", h.Queue)
	r.Patch("/v1/admin/crosswalk-edges/{id}", h.Edit)
	r.Get("/v1/admin/crosswalk-edges/{id}/audit", h.Audit)
	return r
}

func patchEdge(t *testing.T, router http.Handler, edgeID uuid.UUID, body admincrosswalkreview.EditRequest) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/crosswalk-edges/"+edgeID.String(), bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func countContentEdits(t *testing.T, edgeID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM fw_to_scf_edge_content_edits WHERE edge_id = $1`, edgeID).Scan(&n); err != nil {
		t.Fatalf("count content edits: %v", err)
	}
	return n
}

func countTierTransitions(t *testing.T, edgeID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM fw_to_scf_edge_tier_transitions WHERE edge_id = $1`, edgeID).Scan(&n); err != nil {
		t.Fatalf("count tier transitions: %v", err)
	}
	return n
}

func readEdge(t *testing.T, edgeID uuid.UUID) (relType string, strength float64, rationale, tier, attribution string) {
	t.Helper()
	if err := adminPool.QueryRow(context.Background(), `
		SELECT relationship_type::text, strength, rationale, mapping_tier::text, source_attribution::text
		FROM fw_to_scf_edges WHERE id = $1
	`, edgeID).Scan(&relType, &strength, &rationale, &tier, &attribution); err != nil {
		t.Fatalf("read edge: %v", err)
	}
	return
}

// TestEditWritesAuditRow is the slice's load-bearing criterion: a content edit
// changes the mapping AND appends the audit row, atomically.
func TestEditWritesAuditRow(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	s := seedEdge(t, "edit-audit", crosswalktier.TierDraft)
	editor := uuid.New()
	router := newRouter(true, "user:"+editor.String())

	rec := patchEdge(t, router, s.EdgeID, admincrosswalkreview.EditRequest{
		RelationshipType: "intersects_with",
		Strength:         0.45,
		Rationale:        "only the logging clause overlaps",
		Note:             "downgraded after reading the anchor text",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var resp admincrosswalkreview.EditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.EditID == "" {
		t.Fatal("response carried no edit_id — the caller cannot prove the edit was recorded")
	}
	if resp.From.RelationshipType != "subset_of" || resp.To.RelationshipType != "intersects_with" {
		t.Fatalf("before/after wrong: %+v -> %+v", resp.From, resp.To)
	}
	// The editor is taken from the credential, never the body (threat-model T).
	if resp.EditorID != editor.String() {
		t.Fatalf("editor_id = %q; want %q", resp.EditorID, editor)
	}

	relType, strength, rationale, tier, attribution := readEdge(t, s.EdgeID)
	if relType != "intersects_with" || strength != 0.45 || rationale != "only the logging clause overlaps" {
		t.Fatalf("edge not updated: %s %v %q", relType, strength, rationale)
	}
	// A draft mapping is not demoted (it is already below verified) and its
	// provenance is untouched — provenance and trust are orthogonal (ADR 0018).
	if tier != string(crosswalktier.TierDraft) {
		t.Fatalf("tier = %q; want draft", tier)
	}
	if attribution != "community_draft" {
		t.Fatalf("source_attribution = %q; an edit must never rewrite provenance", attribution)
	}

	if got := countContentEdits(t, s.EdgeID); got != 1 {
		t.Fatalf("content-edit audit rows = %d; want 1", got)
	}
	var fromRel, toRel, note string
	var fromStrength, toStrength float64
	if err := adminPool.QueryRow(context.Background(), `
		SELECT from_relationship_type::text, to_relationship_type::text,
		       from_strength, to_strength, note
		FROM fw_to_scf_edge_content_edits WHERE edge_id = $1
	`, s.EdgeID).Scan(&fromRel, &toRel, &fromStrength, &toStrength, &note); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if fromRel != "subset_of" || toRel != "intersects_with" || fromStrength != 0.8 || toStrength != 0.45 {
		t.Fatalf("audit row did not record the full before/after: %s->%s %v->%v",
			fromRel, toRel, fromStrength, toStrength)
	}
	if note != "downgraded after reading the anchor text" {
		t.Fatalf("audit note = %q", note)
	}
}

// TestEditOfVerifiedMappingDemotesThroughSlice483 is D-536b-1: the edited
// mapping is no longer the mapping that was verified, so it re-enters review —
// through 483's state machine and its audit table, not a second lifecycle.
func TestEditOfVerifiedMappingDemotesThroughSlice483(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	s := seedEdge(t, "edit-demote", crosswalktier.TierVerified)
	editor := uuid.New()
	router := newRouter(true, "user:"+editor.String())

	rec := patchEdge(t, router, s.EdgeID, admincrosswalkreview.EditRequest{
		RelationshipType: "subset_of",
		Strength:         0.55,
		Rationale:        "seeded rationale",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp admincrosswalkreview.EditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TierDemotedFrom != "verified" || resp.TierDemotedTo != "under_review" {
		t.Fatalf("demotion not reported: %q -> %q", resp.TierDemotedFrom, resp.TierDemotedTo)
	}

	_, _, _, tier, _ := readEdge(t, s.EdgeID)
	if tier != string(crosswalktier.TierUnderReview) {
		t.Fatalf("tier after edit = %q; want under_review", tier)
	}
	if got := countContentEdits(t, s.EdgeID); got != 1 {
		t.Fatalf("content-edit rows = %d; want 1", got)
	}
	// The demotion went through 483's machine, so 483's trail records it.
	if got := countTierTransitions(t, s.EdgeID); got != 1 {
		t.Fatalf("tier-transition rows = %d; want 1", got)
	}
	var fromTier, toTier string
	var reviewer uuid.UUID
	if err := adminPool.QueryRow(context.Background(), `
		SELECT from_tier::text, to_tier::text, reviewer_id
		FROM fw_to_scf_edge_tier_transitions WHERE edge_id = $1
	`, s.EdgeID).Scan(&fromTier, &toTier, &reviewer); err != nil {
		t.Fatalf("read transition: %v", err)
	}
	if fromTier != "verified" || toTier != "under_review" {
		t.Fatalf("transition = %s -> %s", fromTier, toTier)
	}
	if reviewer != editor {
		t.Fatalf("reviewer_id = %s; want the editing admin %s", reviewer, editor)
	}
}

// TestRefusedEditsWriteNothing covers every refusal arm. A refused edit must
// leave the edge and BOTH audit tables exactly as they were — otherwise the
// trail would record edits that never happened, or an edit would slip through
// unaudited.
func TestRefusedEditsWriteNothing(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	cases := []struct {
		name       string
		tier       crosswalktier.Tier
		body       admincrosswalkreview.EditRequest
		wantStatus int
	}{
		{
			"no-op edit",
			crosswalktier.TierDraft,
			admincrosswalkreview.EditRequest{RelationshipType: "subset_of", Strength: 0.8, Rationale: "seeded rationale"},
			http.StatusUnprocessableEntity,
		},
		{
			"rejected mapping is terminal",
			crosswalktier.TierRejected,
			admincrosswalkreview.EditRequest{RelationshipType: "equal", Strength: 1.0, Rationale: "revived"},
			http.StatusUnprocessableEntity,
		},
		{
			"unknown relationship type",
			crosswalktier.TierDraft,
			admincrosswalkreview.EditRequest{RelationshipType: "sorta_equal", Strength: 0.5, Rationale: "x"},
			http.StatusBadRequest,
		},
		{
			"strength above 1",
			crosswalktier.TierDraft,
			admincrosswalkreview.EditRequest{RelationshipType: "equal", Strength: 1.5, Rationale: "x"},
			http.StatusBadRequest,
		},
		{
			"strength below 0",
			crosswalktier.TierDraft,
			admincrosswalkreview.EditRequest{RelationshipType: "equal", Strength: -0.5, Rationale: "x"},
			http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := seedEdge(t, "refuse", tc.tier)
			router := newRouter(true, "user:"+uuid.NewString())
			beforeRel, beforeStrength, beforeRationale, beforeTier, _ := readEdge(t, s.EdgeID)

			rec := patchEdge(t, router, s.EdgeID, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d; want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			afterRel, afterStrength, afterRationale, afterTier, _ := readEdge(t, s.EdgeID)
			if afterRel != beforeRel || afterStrength != beforeStrength ||
				afterRationale != beforeRationale || afterTier != beforeTier {
				t.Fatalf("a refused edit mutated the edge: %s/%v/%q/%s -> %s/%v/%q/%s",
					beforeRel, beforeStrength, beforeRationale, beforeTier,
					afterRel, afterStrength, afterRationale, afterTier)
			}
			if got := countContentEdits(t, s.EdgeID); got != 0 {
				t.Fatalf("a refused edit wrote %d audit rows; want 0", got)
			}
			if got := countTierTransitions(t, s.EdgeID); got != 0 {
				t.Fatalf("a refused edit wrote %d tier transitions; want 0", got)
			}
		})
	}
}

// TestEditUnknownEdgeIs404 pins the not-found arm.
func TestEditUnknownEdgeIs404(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	router := newRouter(true, "user:"+uuid.NewString())
	rec := patchEdge(t, router, uuid.New(), admincrosswalkreview.EditRequest{
		RelationshipType: "equal", Strength: 1.0, Rationale: "x",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestNonAdminRejected is the threat-model S/E arm: the gate is server-side on
// every route, read and write alike. A non-admin must not even see the queue —
// it exposes the conflict heuristics and reviewer-facing rationale.
func TestNonAdminRejected(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	s := seedEdge(t, "authz", crosswalktier.TierDraft)
	router := newRouter(false, "user:"+uuid.NewString())

	rec := patchEdge(t, router, s.EdgeID, admincrosswalkreview.EditRequest{
		RelationshipType: "equal", Strength: 1.0, Rationale: "unauthorised",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PATCH status = %d; want 403", rec.Code)
	}
	if got := countContentEdits(t, s.EdgeID); got != 0 {
		t.Fatalf("a 403 wrote %d audit rows; want 0", got)
	}

	for _, path := range []string{
		"/v1/admin/crosswalk-review?framework_version_id=" + s.FrameworkVersionID.String(),
		"/v1/admin/crosswalk-edges/" + s.EdgeID.String() + "/audit",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d; want 403", path, rec.Code)
		}
	}
}

// TestAppRoleCannotRepointAnEdge is the invariant-#7 pin at the DATABASE layer.
// The Go code never writes the endpoint columns, but the stronger guarantee is
// that it could not even if it tried: atlas_app holds no UPDATE privilege on
// framework_requirement_id or scf_anchor_id, so no code path running as the app
// role can turn a requirement -> anchor edge into anything else.
func TestAppRoleCannotRepointAnEdge(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	s := seedEdge(t, "invariant7", crosswalktier.TierDraft)
	ctx := context.Background()

	for _, col := range []string{"framework_requirement_id", "scf_anchor_id", "source_attribution"} {
		var stmt string
		var arg any
		switch col {
		case "source_attribution":
			stmt = `UPDATE fw_to_scf_edges SET source_attribution = $2 WHERE id = $1`
			arg = "scf_official"
		default:
			stmt = fmt.Sprintf(`UPDATE fw_to_scf_edges SET %s = $2 WHERE id = $1`, col)
			arg = uuid.New()
		}
		if _, err := appPool.Exec(ctx, stmt, s.EdgeID, arg); err == nil {
			t.Fatalf("atlas_app was able to UPDATE %s — the slice-536b grant is too wide", col)
		}
	}

	// The three columns the grant DOES cover still work as the app role, so the
	// narrowing above is a real boundary and not a broken grant.
	if _, err := appPool.Exec(ctx,
		`UPDATE fw_to_scf_edges SET relationship_type = 'equal', strength = 1.0, rationale = 'ok', updated_at = now() WHERE id = $1`,
		s.EdgeID); err != nil {
		t.Fatalf("atlas_app cannot write the granted content columns: %v", err)
	}
}

// TestQueueSurfacesConflictsAndTrail exercises the read surfaces end to end: the
// queue joins the slice-536a findings onto real catalog rows, and the audit view
// returns both trails after an edit.
func TestQueueSurfacesConflictsAndTrail(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	s := seedEdge(t, "queue", crosswalktier.TierDraft)
	editor := uuid.New()
	router := newRouter(true, "user:"+editor.String())

	// Make the seeded edge self-contradictory: `no_relationship` at positive
	// strength is 536a's high-severity per-edge rule.
	rec := patchEdge(t, router, s.EdgeID, admincrosswalkreview.EditRequest{
		RelationshipType: "no_relationship",
		Strength:         0.8,
		Rationale:        "seeded rationale",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d (body=%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/crosswalk-review?framework_version_id="+s.FrameworkVersionID.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET queue status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var queue admincrosswalkreview.ReviewQueueResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &queue); err != nil {
		t.Fatalf("decode queue: %v", err)
	}
	if len(queue.Requirements) != 1 {
		t.Fatalf("requirements = %d; want 1", len(queue.Requirements))
	}
	if queue.ConflictCount == 0 {
		t.Fatalf("no conflict surfaced for a no_relationship edge at strength 0.8: %+v", queue.Requirements[0])
	}
	if queue.Total != 1 {
		t.Fatalf("total = %d; want 1", queue.Total)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/v1/admin/crosswalk-edges/"+s.EdgeID.String()+"/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET audit status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var audit admincrosswalkreview.AuditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &audit); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if audit.ContentEditCount != 1 || len(audit.ContentEdits) != 1 {
		t.Fatalf("audit trail = %+v; want one content edit", audit)
	}
	if audit.ContentEdits[0].EditorID != editor.String() {
		t.Fatalf("trail editor = %q; want %q", audit.ContentEdits[0].EditorID, editor)
	}
	if audit.CurrentTier != string(crosswalktier.TierDraft) {
		t.Fatalf("current_tier = %q; a content edit must not promote a mapping", audit.CurrentTier)
	}
}
