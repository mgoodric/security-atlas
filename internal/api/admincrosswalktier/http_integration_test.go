//go:build integration

// Integration tests for the slice 483 admin crosswalk-tier surface
// (POST /v1/admin/crosswalk-edges/{id}/tier). Requires DATABASE_URL_APP
// (atlas_app, the RLS-enforcing app role that holds the NARROW
// UPDATE(mapping_tier) grant) + DATABASE_URL (BYPASSRLS, for seeding catalog
// rows). Proves:
//
//   - AC-6: a full draft -> under_review -> verified transition writes the
//     append-only audit trail (one row per move) AND the new tier surfaces on
//     the public /requirements/{id}/anchors read path, against real Postgres —
//     including that the read path exposes the tier LABEL but NO reviewer
//     identity (P0-483-6).
//   - AC-7 (threat-model E): a NON-admin caller's transition is rejected 403;
//     an ILLEGAL skip (draft -> verified) is rejected 422 with neither the tier
//     nor an audit row written (P0-483-1 / P0-483-2 / P0-483-4).
//
// fw_to_scf_edges + fw_to_scf_edge_tier_transitions are CATALOG tables (no
// tenant_id, no RLS), so the handler runs WITHOUT tenancymw — the gate is the
// admin-role authz check, exercised here through the real authctx credential
// the production /v1 chain injects.
package admincrosswalktier_test

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

	"github.com/mgoodric/security-atlas/internal/api/admincrosswalktier"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/crosswalktier"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping admincrosswalktier integration tests")
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

// seedEdge seeds a complete catalog chain (framework -> version -> requirement
// -> anchor -> edge) at mapping_tier='draft' with source_attribution
// community_draft (the agent-authored draft case the slice governs). Returns the
// edge id and the requirement id (the requirement is the /anchors read key).
// Catalog rows are global; cleanup is scoped to the unique slugs this run
// creates so parallel suites don't collide.
func seedEdge(t *testing.T, tag string) (edgeID, requirementID uuid.UUID) {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	ctx := context.Background()
	uniq := tag + "-" + uuid.NewString()[:8]

	fwID := uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, 'slice483 test fw', 'test', '')
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

	requirementID = uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, 'CC-483', 'slice483 requirement', '')
	`, requirementID, verID); err != nil {
		t.Fatalf("seed requirement: %v", err)
	}

	// Reuse an existing SCF anchor if the catalog has one; otherwise mint a
	// throwaway anchor under this run's framework_version (scf_anchors carries a
	// framework_version_id FK).
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
		VALUES ($1, $2, $3, 'IAC', 'slice483 anchor', '')
	`, anchorID, scfVerID, "IAC-"+uniq); err != nil {
		t.Fatalf("seed anchor: %v", err)
	}

	edgeID = uuid.New()
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO fw_to_scf_edges
			(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', 'slice483 test edge')
	`, edgeID, requirementID, anchorID); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		// fw_to_scf_edge_tier_transitions cascades on edge delete; the edge
		// cascades on requirement/anchor delete. Delete from the leaves up.
		_, _ = adminPool.Exec(c, `DELETE FROM fw_to_scf_edges WHERE id = $1`, edgeID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_requirements WHERE id = $1`, requirementID)
		_, _ = adminPool.Exec(c, `DELETE FROM scf_anchors WHERE id = $1`, anchorID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_versions WHERE id IN ($1, $2)`, verID, scfVerID)
		_, _ = adminPool.Exec(c, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return edgeID, requirementID
}

// newRouter wires the handler behind a credential-injecting middleware exactly
// like the production /v1 chain (minus tenancymw — catalog tables, no RLS).
// reviewerID is the acting admin's user id (the JWT subject form "user:<uuid>").
func newRouter(isAdmin bool, reviewerID string) http.Handler {
	handler := admincrosswalktier.New(crosswalktier.NewStore(appPool))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:      "key_test",
				IsAdmin: isAdmin,
				UserID:  reviewerID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/v1/admin/crosswalk-edges/{id}/tier", handler.Transition)
	return r
}

func postTier(t *testing.T, router http.Handler, edgeID uuid.UUID, tier, note string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(admincrosswalktier.TransitionRequest{Tier: tier, Note: note})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/crosswalk-edges/"+edgeID.String()+"/tier", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestFullTransitionLifecycle is AC-6: draft -> under_review -> verified writes
// the audit trail and the tier surfaces on the read path.
func TestFullTransitionLifecycle(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	edgeID, requirementID := seedEdge(t, "lifecycle")
	reviewer := uuid.New()
	router := newRouter(true, "user:"+reviewer.String())

	// draft -> under_review.
	rec := postTier(t, router, edgeID, "under_review", "claimed for review")
	if rec.Code != http.StatusOK {
		t.Fatalf("under_review status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp admincrosswalktier.TransitionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FromTier != "draft" || resp.ToTier != "under_review" {
		t.Fatalf("transition response = %+v; want draft->under_review", resp)
	}
	if resp.ReviewerID != reviewer.String() {
		t.Fatalf("reviewer id = %q; want %q", resp.ReviewerID, reviewer.String())
	}

	// under_review -> verified.
	rec = postTier(t, router, edgeID, "verified", "vetted")
	if rec.Code != http.StatusOK {
		t.Fatalf("verified status = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	// Audit trail: two rows (newest first), the SAME transaction as each tier
	// change (threat-model R / P0-483-4).
	store := crosswalktier.NewStore(appPool)
	transitions, err := store.ListTransitions(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("list transitions: %v", err)
	}
	if len(transitions) != 2 {
		t.Fatalf("audit rows = %d; want 2", len(transitions))
	}
	if transitions[0].ToTier != crosswalktier.TierVerified || transitions[1].ToTier != crosswalktier.TierUnderReview {
		t.Fatalf("audit order wrong: %+v", transitions)
	}
	if transitions[0].ReviewerID != reviewer {
		t.Fatalf("audit reviewer = %v; want %v", transitions[0].ReviewerID, reviewer)
	}

	// The store reports the new current tier.
	cur, err := store.CurrentTier(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("current tier: %v", err)
	}
	if cur != crosswalktier.TierVerified {
		t.Fatalf("current tier = %q; want verified", cur)
	}

	// Read path: /requirements/{id}/anchors surfaces the tier LABEL and NO
	// reviewer identity (P0-483-6). Query the read SQL directly via the app
	// pool (the same query the anchors handler runs).
	var tier string
	if err := appPool.QueryRow(context.Background(), `
		SELECT e.mapping_tier
		FROM fw_to_scf_edges e
		WHERE e.framework_requirement_id = $1
	`, requirementID).Scan(&tier); err != nil {
		t.Fatalf("read mapping_tier: %v", err)
	}
	if tier != "verified" {
		t.Fatalf("read path tier = %q; want verified", tier)
	}
}

// TestNonAdminRejected is AC-7 / threat-model E: a non-admin caller cannot
// transition a tier (403), and neither the tier nor an audit row is written.
func TestNonAdminRejected(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	edgeID, _ := seedEdge(t, "nonadmin")
	router := newRouter(false, "user:"+uuid.NewString())

	rec := postTier(t, router, edgeID, "under_review", "should be blocked")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d; want 403 (body=%s)", rec.Code, rec.Body.String())
	}

	store := crosswalktier.NewStore(appPool)
	cur, err := store.CurrentTier(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("current tier: %v", err)
	}
	if cur != crosswalktier.TierDraft {
		t.Fatalf("tier changed despite 403: %q", cur)
	}
	transitions, err := store.ListTransitions(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("list transitions: %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("audit rows written despite 403: %d", len(transitions))
	}
}

// TestIllegalSkipRejected is AC-7 (tampering arm): an admin requesting the
// illegal draft -> verified skip is rejected 422 with no tier change and no
// audit row (P0-483-1 / threat-model T).
func TestIllegalSkipRejected(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	edgeID, _ := seedEdge(t, "skip")
	router := newRouter(true, "user:"+uuid.NewString())

	rec := postTier(t, router, edgeID, "verified", "illegal skip")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("skip status = %d; want 422 (body=%s)", rec.Code, rec.Body.String())
	}

	store := crosswalktier.NewStore(appPool)
	cur, err := store.CurrentTier(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("current tier: %v", err)
	}
	if cur != crosswalktier.TierDraft {
		t.Fatalf("tier changed on illegal skip: %q", cur)
	}
	transitions, err := store.ListTransitions(context.Background(), edgeID)
	if err != nil {
		t.Fatalf("list transitions: %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("audit row written on illegal skip: %d", len(transitions))
	}
}

// TestUnknownEdge404 confirms an unknown edge id is a 404.
func TestUnknownEdge404(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	router := newRouter(true, "user:"+uuid.NewString())
	rec := postTier(t, router, uuid.New(), "under_review", "no such edge")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown edge status = %d; want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestBadTier400 confirms a malformed tier is a 400.
func TestBadTier400(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	edgeID, _ := seedEdge(t, "badtier")
	router := newRouter(true, "user:"+uuid.NewString())
	rec := postTier(t, router, edgeID, "approved", "not a tier")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad tier status = %d; want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}
