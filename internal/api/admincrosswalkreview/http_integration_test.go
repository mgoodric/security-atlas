//go:build integration

// Integration tests for the slice 536b-1 admin crosswalk-review surface
// (GET /v1/admin/crosswalk-review + PATCH /v1/admin/crosswalk-edges/{id}).
// Requires DATABASE_URL_APP (atlas_app, holding the D-536b-2 column-level
// UPDATE (relationship_type, strength, rationale) grant) + DATABASE_URL
// (BYPASSRLS, for seeding catalog rows). Proves the issue's INTEGRATION
// acceptance criteria against real Postgres:
//
//   - A content edit writes the append-only before/after audit row in the
//     SAME transaction as the UPDATE, and NO edit path bypasses it: the
//     non-admin, tier-gated, invalid, and no-op arms each leave BOTH the edge
//     content and the audit table untouched.
//   - No mapping is auto-approved: an edit never changes mapping_tier, and
//     the only route to verified remains slice 483's tier state machine.
//   - The verified -> under_review demotion edge (D-536b-1) composes with the
//     edit gate: a verified edge rejects edits 409, demotes through 483's
//     store, then edits cleanly.
//   - Conflicts from the slice-536a module are present in the review-list
//     response, computed over the framework version's full edge set.
//
// fw_to_scf_edges + fw_to_scf_edge_content_edits are CATALOG tables (no
// tenant_id, no RLS), so the handlers run WITHOUT tenancymw — the gate is the
// admin-role authz check, exercised here through the real authctx credential
// the production /v1 chain injects (same harness as the slice-483 suite).
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

// fixture is one seeded catalog chain: a framework version with one
// requirement and n edges to distinct anchors, all community_draft/draft.
type fixture struct {
	versionID     uuid.UUID
	requirementID uuid.UUID
	edgeIDs       []uuid.UUID
}

// seedChain seeds framework -> version -> requirement -> n anchors -> n edges
// (mapping_tier defaults to 'draft'; source_attribution community_draft — the
// agent-authored draft case the review surface curates). relTypes/strengths
// parameterize the edges so conflict-shaped fixtures are expressible.
// Cleanup deletes from the leaves up; both audit tables cascade on edge
// delete.
func seedChain(t *testing.T, tag string, relTypes []string, strengths []float64) fixture {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	if len(relTypes) != len(strengths) {
		t.Fatalf("seedChain: relTypes/strengths length mismatch")
	}
	ctx := context.Background()
	uniq := tag + "-" + uuid.NewString()[:8]

	fwID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, 'slice536b1 test fw', 'test', '')
	`, fwID, uniq); err != nil {
		t.Fatalf("seed framework: %v", err)
	}

	verID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2026', 'current')
	`, verID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}

	requirementID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'CC-536', 'slice536b1 requirement', '')
	`, requirementID, verID); err != nil {
		t.Fatalf("seed requirement: %v", err)
	}

	scfVerID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'scf-2026', 'current')
	`, scfVerID, fwID); err != nil {
		t.Fatalf("seed scf framework_version: %v", err)
	}

	anchorIDs := make([]uuid.UUID, 0, len(relTypes))
	edgeIDs := make([]uuid.UUID, 0, len(relTypes))
	for i := range relTypes {
		anchorID := uuid.New()
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
			VALUES ($1, $2, $3, 'IAC', 'slice536b1 anchor', '')
		`, anchorID, scfVerID, fmt.Sprintf("IAC-%s-%d", uniq, i)); err != nil {
			t.Fatalf("seed anchor %d: %v", i, err)
		}
		anchorIDs = append(anchorIDs, anchorID)

		edgeID := uuid.New()
		if _, err := adminPool.Exec(ctx, `
			INSERT INTO fw_to_scf_edges
				(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
			VALUES ($1, $2, $3, $4, $5, 'community_draft', 'slice536b1 test edge')
		`, edgeID, requirementID, anchorIDs[i], relTypes[i], strengths[i]); err != nil {
			t.Fatalf("seed edge %d: %v", i, err)
		}
		edgeIDs = append(edgeIDs, edgeID)
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, id := range edgeIDs {
			_, _ = adminPool.Exec(c, `DELETE FROM fw_to_scf_edges WHERE id = $1`, id)
		}
		_, _ = adminPool.Exec(c, `DELETE FROM framework_requirements WHERE id = $1`, requirementID)
		for _, id := range anchorIDs {
			_, _ = adminPool.Exec(c, `DELETE FROM scf_anchors WHERE id = $1`, id)
		}
		_, _ = adminPool.Exec(c, `DELETE FROM framework_versions WHERE id IN ($1, $2)`, verID, scfVerID)
		_, _ = adminPool.Exec(c, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return fixture{versionID: verID, requirementID: requirementID, edgeIDs: edgeIDs}
}

// newRouter wires both handlers behind a credential-injecting middleware
// exactly like the production /v1 chain (minus tenancymw — catalog tables, no
// RLS). editorID is the acting admin's user id (JWT subject form
// "user:<uuid>").
func newRouter(isAdmin bool, editorID string) http.Handler {
	handler := admincrosswalkreview.New(appPool, crosswalkedit.NewStore(appPool))
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
	r.Get("/v1/admin/crosswalk-review", handler.Review)
	r.Patch("/v1/admin/crosswalk-edges/{id}", handler.EditContent)
	return r
}

func patchEdge(t *testing.T, router http.Handler, edgeID uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/crosswalk-edges/"+edgeID.String(), bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func getReview(t *testing.T, router http.Handler, versionID uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/crosswalk-review?framework_version_id="+versionID.String(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// readEdgeContent reads the edge's live content + tier via the app pool.
func readEdgeContent(t *testing.T, edgeID uuid.UUID) (relType string, strength float64, rationale, tier string) {
	t.Helper()
	if err := appPool.QueryRow(context.Background(), `
		SELECT relationship_type, strength, rationale, mapping_tier
		FROM fw_to_scf_edges WHERE id = $1
	`, edgeID).Scan(&relType, &strength, &rationale, &tier); err != nil {
		t.Fatalf("read edge content: %v", err)
	}
	return relType, strength, rationale, tier
}

func countAuditRows(t *testing.T, edgeID uuid.UUID) int {
	t.Helper()
	var n int
	if err := appPool.QueryRow(context.Background(), `
		SELECT count(*) FROM fw_to_scf_edge_content_edits WHERE edge_id = $1
	`, edgeID).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// TestEditWritesAuditRow is the load-bearing AC: a content edit rewrites the
// STRM columns AND appends the before/after audit row atomically, without
// touching mapping_tier (no auto-approve) or source_attribution.
func TestEditWritesAuditRow(t *testing.T) {
	fx := seedChain(t, "edit", []string{"equal"}, []float64{1.0})
	edgeID := fx.edgeIDs[0]
	editor := uuid.New()
	router := newRouter(true, "user:"+editor.String())

	rec := patchEdge(t, router, edgeID, map[string]any{
		"relationship_type": "subset_of",
		"strength":          0.6,
		"rationale":         "curated: partial coverage only",
		"note":              "reviewed against the source text",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("edit status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp admincrosswalkreview.EditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.From.RelationshipType != "equal" || resp.From.Strength != 1.0 {
		t.Fatalf("from block wrong: %+v", resp.From)
	}
	if resp.To.RelationshipType != "subset_of" || resp.To.Strength != 0.6 {
		t.Fatalf("to block wrong: %+v", resp.To)
	}
	if resp.EditorID != editor.String() {
		t.Fatalf("editor id = %q; want %q", resp.EditorID, editor.String())
	}
	if resp.MappingTier != "draft" {
		t.Fatalf("mapping tier = %q; want draft (an edit must not transition the tier)", resp.MappingTier)
	}

	relType, strength, rationale, tier := readEdgeContent(t, edgeID)
	if relType != "subset_of" || strength != 0.6 || rationale != "curated: partial coverage only" {
		t.Fatalf("edge content not updated: %s %v %q", relType, strength, rationale)
	}
	if tier != "draft" {
		t.Fatalf("tier changed by a content edit: %q", tier)
	}

	// Exactly one immutable audit row, carrying the full before/after diff and
	// the actor.
	store := crosswalkedit.NewStore(appPool)
	edits, err := store.ListEdits(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("list edits: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("audit rows = %d; want 1", len(edits))
	}
	e := edits[0]
	if string(e.From.RelationshipType) != "equal" || e.From.Strength != 1.0 || e.From.Rationale != "slice536b1 test edge" {
		t.Fatalf("audit before-diff wrong: %+v", e.From)
	}
	if string(e.To.RelationshipType) != "subset_of" || e.To.Strength != 0.6 {
		t.Fatalf("audit after-diff wrong: %+v", e.To)
	}
	if e.EditorID != editor {
		t.Fatalf("audit editor = %v; want %v", e.EditorID, editor)
	}
	if e.Note != "reviewed against the source text" {
		t.Fatalf("audit note = %q", e.Note)
	}
}

// TestEditNonAdminRejected: threat-model S/E — a non-admin caller cannot edit;
// neither the content nor an audit row is written.
func TestEditNonAdminRejected(t *testing.T) {
	fx := seedChain(t, "nonadmin", []string{"equal"}, []float64{1.0})
	edgeID := fx.edgeIDs[0]
	router := newRouter(false, "user:"+uuid.NewString())

	rec := patchEdge(t, router, edgeID, map[string]any{"strength": 0.2})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d; want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if _, strength, _, _ := readEdgeContent(t, edgeID); strength != 1.0 {
		t.Fatalf("content changed despite 403: strength=%v", strength)
	}
	if n := countAuditRows(t, edgeID); n != 0 {
		t.Fatalf("audit rows written despite 403: %d", n)
	}
}

// TestEditVerifiedTierGate is D-536b-1 end to end: a verified edge rejects
// content edits (409, nothing written), demotes verified -> under_review
// through slice 483's state machine (the edge this slice added), then edits
// cleanly — one lifecycle, audited on both dimensions.
func TestEditVerifiedTierGate(t *testing.T) {
	fx := seedChain(t, "vgate", []string{"equal"}, []float64{1.0})
	edgeID := fx.edgeIDs[0]
	reviewer := uuid.New()
	router := newRouter(true, "user:"+reviewer.String())

	// Drive the edge to verified through the 483 machine.
	tiers := crosswalktier.NewStore(appPool)
	for _, to := range []crosswalktier.Tier{crosswalktier.TierUnderReview, crosswalktier.TierVerified} {
		if _, err := tiers.Transition(context.Background(), crosswalktier.TransitionInput{
			EdgeID: edgeID, ToTier: to, ReviewerID: reviewer, Note: "test",
		}); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}

	rec := patchEdge(t, router, edgeID, map[string]any{"strength": 0.5})
	if rec.Code != http.StatusConflict {
		t.Fatalf("verified edit status = %d; want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	if _, strength, _, tier := readEdgeContent(t, edgeID); strength != 1.0 || tier != "verified" {
		t.Fatalf("verified edge mutated: strength=%v tier=%s", strength, tier)
	}
	if n := countAuditRows(t, edgeID); n != 0 {
		t.Fatalf("audit row written for a rejected edit: %d", n)
	}

	// Demote (the D-536b-1 verified -> under_review edge), then the edit lands.
	if _, err := tiers.Transition(context.Background(), crosswalktier.TransitionInput{
		EdgeID: edgeID, ToTier: crosswalktier.TierUnderReview, ReviewerID: reviewer, Note: "demote to correct strength",
	}); err != nil {
		t.Fatalf("demotion transition: %v", err)
	}
	rec = patchEdge(t, router, edgeID, map[string]any{"strength": 0.5})
	if rec.Code != http.StatusOK {
		t.Fatalf("post-demotion edit status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if _, strength, _, tier := readEdgeContent(t, edgeID); strength != 0.5 || tier != "under_review" {
		t.Fatalf("post-demotion state wrong: strength=%v tier=%s", strength, tier)
	}
	if n := countAuditRows(t, edgeID); n != 1 {
		t.Fatalf("audit rows after demoted edit = %d; want 1", n)
	}
}

// TestEditValidationArms: malformed inputs are rejected before any write —
// bad strength (400), bad relationship type (400), empty patch (400), no-op
// patch (422), unknown edge (404). None writes content or audit rows.
func TestEditValidationArms(t *testing.T) {
	fx := seedChain(t, "valid", []string{"equal"}, []float64{1.0})
	edgeID := fx.edgeIDs[0]
	router := newRouter(true, "user:"+uuid.NewString())

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"strength above 1", map[string]any{"strength": 1.5}, http.StatusBadRequest},
		{"unknown relationship type", map[string]any{"relationship_type": "related_to"}, http.StatusBadRequest},
		{"empty patch", map[string]any{"note": "no fields"}, http.StatusBadRequest},
		{"no-op patch", map[string]any{"strength": 1.0, "relationship_type": "equal"}, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		rec := patchEdge(t, router, edgeID, tc.body)
		if rec.Code != tc.want {
			t.Fatalf("%s: status = %d; want %d (body=%s)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	if rec := patchEdge(t, router, uuid.New(), map[string]any{"strength": 0.4}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown edge: status = %d; want 404", rec.Code)
	}

	if relType, strength, _, _ := readEdgeContent(t, edgeID); relType != "equal" || strength != 1.0 {
		t.Fatalf("content changed by a rejected arm: %s %v", relType, strength)
	}
	if n := countAuditRows(t, edgeID); n != 0 {
		t.Fatalf("audit rows written by rejected arms: %d", n)
	}
}

// TestReviewListSurfacesConflicts: the review list returns the framework
// version's edges (content + provenance + tier, no reviewer identity) plus
// the slice-536a findings — here a duplicate_equal_claim from two `equal`
// edges to distinct anchors.
func TestReviewListSurfacesConflicts(t *testing.T) {
	fx := seedChain(t, "review", []string{"equal", "equal"}, []float64{1.0, 1.0})
	router := newRouter(true, "user:"+uuid.NewString())

	rec := getReview(t, router, fx.versionID)
	if rec.Code != http.StatusOK {
		t.Fatalf("review status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp admincrosswalkreview.ReviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalEdges != 2 || len(resp.Edges) != 2 {
		t.Fatalf("edges = %d/%d; want 2/2", len(resp.Edges), resp.TotalEdges)
	}
	for _, e := range resp.Edges {
		if e.MappingTier != "draft" || e.SourceAttribution != "community_draft" {
			t.Fatalf("edge tier/provenance wrong: %+v", e)
		}
		if e.RequirementCode != "CC-536" {
			t.Fatalf("requirement code = %q", e.RequirementCode)
		}
	}

	var found bool
	for _, c := range resp.Conflicts {
		if c.Reason == "duplicate_equal_claim" {
			found = true
			if c.Severity != "high" || len(c.EdgeIDs) != 2 {
				t.Fatalf("duplicate_equal_claim shape wrong: %+v", c)
			}
			if c.RequirementCode != "CC-536" {
				t.Fatalf("conflict requirement = %q", c.RequirementCode)
			}
		}
	}
	if !found {
		t.Fatalf("duplicate_equal_claim not surfaced; conflicts = %+v", resp.Conflicts)
	}

	// Threat-model S/E: the review list is admin-gated too.
	nonAdmin := newRouter(false, "user:"+uuid.NewString())
	if rec := getReview(t, nonAdmin, fx.versionID); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin review status = %d; want 403", rec.Code)
	}
}

// TestReviewListBadVersionParam: a malformed framework_version_id is a 400.
func TestReviewListBadVersionParam(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	router := newRouter(true, "user:"+uuid.NewString())
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/crosswalk-review?framework_version_id=not-a-uuid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad param status = %d; want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}
