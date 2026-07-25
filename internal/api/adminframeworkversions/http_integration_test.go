//go:build integration

// Integration tests for the slice 484 framework-versioning admin surface +
// lifecycle store (ADR 0019). Requires DATABASE_URL_APP (atlas_app, the role
// holding the NARROW UPDATE(status)/UPDATE(latest_version_id) grants + the
// review-queue/audit grants) + DATABASE_URL (BYPASSRLS, for seeding catalog
// rows). Proves, against real Postgres:
//
//   - AC-1: an admin promotion moves the target version to current, demotes the
//     prior current version to legacy ("superseded"), repoints
//     frameworks.latest_version_id, and writes the append-only audit; reversible
//     via Revert.
//   - AC-3 + AC-7: the migration-suggest job, given the two adjacent versions,
//     writes exact-code 1:1 carryovers + flags the added/removed remainder into
//     the review queue, and a version-pinned read returns ONLY the pinned
//     version's requirements (no cross-version bleed — P0-484-5).
//   - AC-4: a suggestion is human-approved one at a time, audited; a
//     double-decide is rejected (P0-484-1 — the platform suggests, the human
//     approves; nothing auto-applies).
//   - AC-8 (threat-model T): an in-place edit of a frozen (current) version's
//     requirement is rejected by the immutability trigger (P0-484-2).
//   - AC-9 (threat-model E): a non-admin promotion is rejected 403 (P0-484-3).
//
// frameworks / framework_versions / framework_requirements are CATALOG tables
// (no tenant_id, no RLS), so the handler runs WITHOUT tenancymw — the gate is
// the admin-role authz check exercised through the authctx credential the
// production /v1 chain injects.
package adminframeworkversions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/adminframeworkversions"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/frameworkversion"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping adminframeworkversions integration tests")
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

// seedFramework seeds one framework plus a shared SCF anchor, then loads two
// adjacent SYNTHETIC SOC 2-style versions:
//
//	v1 ("2017")              status=current  — requirements CC6.1, CC6.2, CC6.3
//	v2 ("2017-synthetic-rev") status=legacy   — requirements CC6.1, CC6.2, CC6.4
//
// The overlap (CC6.1, CC6.2) is the exact-code carryover set; CC6.3 is removed,
// CC6.4 is added. Each requirement gets an edge to the shared anchor so the
// reverse-traversal read returns it. v2 is seeded as legacy so the suggest job
// (which only reads requirements, not status) works and the pinned-read /
// promotion paths have a clean prior version to operate on. Returns the ids the
// tests need.
type seeded struct {
	frameworkID uuid.UUID
	v1ID        uuid.UUID // current (2017)
	v2ID        uuid.UUID // legacy  (2017-synthetic-rev)
	anchorID    uuid.UUID
	v1CC61Req   uuid.UUID // a frozen-version requirement (for AC-8)
}

func seedFramework(t *testing.T, tag string) seeded {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	uniq := tag + "-" + uuid.NewString()[:8]

	fwID := uuid.New()
	mustExec(t, `INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, 'slice484 synthetic SOC2', 'AICPA', '')`, fwID, uniq)

	// The shared SCF anchor lives under a SEPARATE framework (scfFwID) so the
	// test framework (fwID) has EXACTLY ONE current version — the
	// at-most-one-current-per-framework invariant the lifecycle store relies on
	// (GetCurrentFrameworkVersion is :one). scf_anchors carries a
	// framework_version_id FK, so the anchor needs its own framework+version.
	scfFwID := uuid.New()
	mustExec(t, `INSERT INTO frameworks (id, tenant_id, slug, name, issuer, description)
		VALUES ($1, NULL, $2, 'slice484 scf catalog', 'SCF', '')`, scfFwID, "scf-"+uniq)
	scfVerID := uuid.New()
	mustExec(t, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, 'scf-2025', 'current')`, scfVerID, scfFwID)
	anchorID := uuid.New()
	mustExec(t, `INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
		VALUES ($1, $2, $3, 'IAC', 'slice484 anchor', '')`, anchorID, scfVerID, "IAC-"+uniq)

	v1ID := uuid.New()
	mustExec(t, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2017', 'current')`, v1ID, fwID)
	mustExec(t, `UPDATE frameworks SET latest_version_id = $1 WHERE id = $2`, v1ID, fwID)

	v2ID := uuid.New()
	mustExec(t, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2017-synthetic-rev', 'legacy')`, v2ID, fwID)

	v1CC61 := seedReqEdge(t, v1ID, anchorID, "CC6.1")
	seedReqEdge(t, v1ID, anchorID, "CC6.2")
	seedReqEdge(t, v1ID, anchorID, "CC6.3")
	seedReqEdge(t, v2ID, anchorID, "CC6.1")
	seedReqEdge(t, v2ID, anchorID, "CC6.2")
	seedReqEdge(t, v2ID, anchorID, "CC6.4")

	t.Cleanup(func() {
		c := context.Background()
		_, _ = adminPool.Exec(c, `DELETE FROM framework_version_audit WHERE framework_id = $1`, fwID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_version_migrations WHERE framework_id = $1`, fwID)
		// The immutability trigger fires only on UPDATE (slice 484 D6), so a
		// DELETE of a frozen version's requirements is allowed — cleanup just
		// deletes from the leaves up. Edges cascade off requirements; deleting
		// the framework cascades its versions.
		_, _ = adminPool.Exec(c, `DELETE FROM fw_to_scf_edges WHERE framework_requirement_id IN (
			SELECT id FROM framework_requirements WHERE framework_version_id IN ($1, $2))`, v1ID, v2ID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_requirements WHERE framework_version_id IN ($1, $2)`, v1ID, v2ID)
		_, _ = adminPool.Exec(c, `DELETE FROM scf_anchors WHERE id = $1`, anchorID)
		_, _ = adminPool.Exec(c, `UPDATE frameworks SET latest_version_id = NULL WHERE id = $1`, fwID)
		_, _ = adminPool.Exec(c, `DELETE FROM framework_versions WHERE framework_id = $1`, fwID)
		_, _ = adminPool.Exec(c, `DELETE FROM frameworks WHERE id IN ($1, $2)`, fwID, scfFwID)
	})

	return seeded{frameworkID: fwID, v1ID: v1ID, v2ID: v2ID, anchorID: anchorID, v1CC61Req: v1CC61}
}

func seedReqEdge(t *testing.T, versionID, anchorID uuid.UUID, code string) uuid.UUID {
	t.Helper()
	reqID := uuid.New()
	mustExec(t, `INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
		VALUES ($1, $2, $3, $4, '')`, reqID, versionID, code, "requirement "+code)
	edgeID := uuid.New()
	mustExec(t, `INSERT INTO fw_to_scf_edges
		(id, framework_requirement_id, scf_anchor_id, relationship_type, strength, source_attribution, rationale)
		VALUES ($1, $2, $3, 'equal', 1.0, 'community_draft', '')`, edgeID, reqID, anchorID)
	return reqID
}

func mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed exec %q: %v", sql, err)
	}
}

func newRouter(isAdmin bool, actorID string) http.Handler {
	h := adminframeworkversions.New(frameworkversion.NewStore(appPool))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:      "key_test",
				IsAdmin: isAdmin,
				UserID:  actorID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/v1/admin/framework-versions/{id}/promote", h.Promote)
	r.Post("/v1/admin/framework-versions/{id}/revert", h.Revert)
	r.Post("/v1/admin/framework-versions/migrations:suggest", h.Suggest)
	r.Get("/v1/admin/framework-versions/migrations", h.ListMigrations)
	r.Post("/v1/admin/framework-versions/migrations/{id}/decision", h.Decide)
	return r
}

func doJSON(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// AC-9 / threat-model E / P0-484-3 — a non-admin promotion is rejected 403.
func TestNonAdminPromotion_Rejected403(t *testing.T) {
	s := seedFramework(t, "eop")
	router := newRouter(false, "user:"+uuid.NewString())
	rec := doJSON(t, router, http.MethodPost,
		"/v1/admin/framework-versions/"+s.v2ID.String()+"/promote",
		map[string]string{"note": "should be blocked"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin promote = %d; want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	// The version must NOT have been promoted.
	var status string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT status FROM framework_versions WHERE id = $1`, s.v2ID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "legacy" {
		t.Errorf("blocked promotion still changed status to %q; want legacy", status)
	}
}

// AC-1 — admin promotion: v2 -> current, v1 -> legacy, latest_version_id
// repointed, audit written; then Revert restores v1.
func TestPromoteAndRevert(t *testing.T) {
	s := seedFramework(t, "promote")
	actor := uuid.New()
	router := newRouter(true, "user:"+actor.String())

	rec := doJSON(t, router, http.MethodPost,
		"/v1/admin/framework-versions/"+s.v2ID.String()+"/promote",
		map[string]string{"note": "promote synthetic rev"})
	if rec.Code != http.StatusOK {
		t.Fatalf("promote = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}

	assertStatus(t, s.v2ID, "current")
	assertStatus(t, s.v1ID, "legacy")
	assertLatest(t, s.frameworkID, s.v2ID)

	var auditN int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM framework_version_audit WHERE framework_id = $1 AND action = 'promote'`,
		s.frameworkID).Scan(&auditN); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditN < 2 { // one promote + one demote
		t.Errorf("promote audit rows = %d; want >= 2 (promote + demote)", auditN)
	}

	// Revert: v2 -> legacy, v1 -> current again.
	rec = doJSON(t, router, http.MethodPost,
		"/v1/admin/framework-versions/"+s.v2ID.String()+"/revert",
		map[string]string{"prior_version_id": s.v1ID.String(), "note": "revert"})
	if rec.Code != http.StatusOK {
		t.Fatalf("revert = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	assertStatus(t, s.v1ID, "current")
	assertStatus(t, s.v2ID, "legacy")
	assertLatest(t, s.frameworkID, s.v1ID)
}

// AC-3 + AC-7 — the suggest job writes exact-code carryovers + flags the rest,
// and a pinned read returns only the pinned version's requirements (no bleed).
func TestSuggestAndPinnedReadNoBleed(t *testing.T) {
	s := seedFramework(t, "suggest")
	actor := uuid.New()
	router := newRouter(true, "user:"+actor.String())

	// Suggest v1 (2017) -> v2 (synthetic rev).
	rec := doJSON(t, router, http.MethodPost,
		"/v1/admin/framework-versions/migrations:suggest",
		map[string]string{"from_version_id": s.v1ID.String(), "to_version_id": s.v2ID.String()})
	if rec.Code != http.StatusOK {
		t.Fatalf("suggest = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var summary struct {
		ExactCode int `json:"exact_code_carryovers"`
		Added     int `json:"added_flagged"`
		Removed   int `json:"removed_flagged"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	// CC6.1, CC6.2 carry over; CC6.4 added; CC6.3 removed.
	if summary.ExactCode != 2 || summary.Added != 1 || summary.Removed != 1 {
		t.Fatalf("suggest summary = %+v; want exact=2 added=1 removed=1", summary)
	}

	// The queue must hold exactly those rows, all pending, none auto-applied
	// (P0-484-1). Verify directly.
	var pending int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM framework_version_migrations
		 WHERE from_version_id = $1 AND to_version_id = $2 AND status = 'pending'`,
		s.v1ID, s.v2ID).Scan(&pending); err != nil {
		t.Fatalf("count queue: %v", err)
	}
	if pending != 4 {
		t.Errorf("review queue pending rows = %d; want 4 (2 exact + 1 added + 1 removed)", pending)
	}

	// P0-484-5 — the suggest job must NOT have created any fw_to_scf edge or
	// requirement (it only suggests). Edge count for v2 stays 3.
	var v2Edges int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM fw_to_scf_edges e
		 JOIN framework_requirements r ON r.id = e.framework_requirement_id
		 WHERE r.framework_version_id = $1`, s.v2ID).Scan(&v2Edges); err != nil {
		t.Fatalf("count v2 edges: %v", err)
	}
	if v2Edges != 3 {
		t.Errorf("suggest mutated the catalog: v2 edge count = %d; want 3 (unchanged)", v2Edges)
	}

	// AC-4 — approve ONE exact-code carryover, audited; double-decide rejected.
	var migID uuid.UUID
	if err := adminPool.QueryRow(context.Background(),
		`SELECT id FROM framework_version_migrations
		 WHERE from_version_id = $1 AND to_version_id = $2 AND match_kind = 'exact_code'
		 ORDER BY requirement_code LIMIT 1`, s.v1ID, s.v2ID).Scan(&migID); err != nil {
		t.Fatalf("pick a migration: %v", err)
	}
	rec = doJSON(t, router, http.MethodPost,
		"/v1/admin/framework-versions/migrations/"+migID.String()+"/decision",
		map[string]any{"approve": true, "note": "carryover ok"})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve = %d; want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	// Double-decide is a conflict.
	rec = doJSON(t, router, http.MethodPost,
		"/v1/admin/framework-versions/migrations/"+migID.String()+"/decision",
		map[string]any{"approve": false, "note": "too late"})
	if rec.Code != http.StatusConflict {
		t.Errorf("double-decide = %d; want 409", rec.Code)
	}
}

// assertStatus / assertLatest helpers.
func assertStatus(t *testing.T, versionID uuid.UUID, want string) {
	t.Helper()
	var got string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT status FROM framework_versions WHERE id = $1`, versionID).Scan(&got); err != nil {
		t.Fatalf("read status %s: %v", versionID, err)
	}
	if got != want {
		t.Errorf("version %s status = %q; want %q", versionID, got, want)
	}
}

func assertLatest(t *testing.T, frameworkID, want uuid.UUID) {
	t.Helper()
	var got uuid.UUID
	if err := adminPool.QueryRow(context.Background(),
		`SELECT latest_version_id FROM frameworks WHERE id = $1`, frameworkID).Scan(&got); err != nil {
		t.Fatalf("read latest: %v", err)
	}
	if got != want {
		t.Errorf("latest_version_id = %s; want %s", got, want)
	}
}

// AC-8 / threat-model T / P0-484-2 — an in-place edit of a frozen (current)
// version's requirement is rejected. Defense in depth (slice 484 D6):
//
//  1. The privileged loader role (atlas_migrate, adminPool) CAN normally write
//     framework_requirements, but the immutability TRIGGER rejects an in-place
//     UPDATE of a frozen version's requirement — this is the §3.3 guard the
//     loader must obey (ship a new version, not an in-place edit).
//  2. The application role (atlas_app, appPool) is additionally GRANT-blocked
//     — it holds only SELECT, so it cannot UPDATE at all (permission denied,
//     a deeper backstop).
//
// The row must be unchanged after both attempts.
func TestFrozenVersionRequirementImmutable(t *testing.T) {
	s := seedFramework(t, "frozen")
	ctx := context.Background()

	// (1) Loader role: the trigger rejects the in-place edit of a 'current'
	// (frozen) version's requirement.
	_, err := adminPool.Exec(ctx,
		`UPDATE framework_requirements SET title = 'tampered' WHERE id = $1`, s.v1CC61Req)
	if err == nil {
		t.Fatalf("loader in-place edit of a frozen version's requirement SUCCEEDED; want trigger rejection")
	}
	if !strings.Contains(err.Error(), "frozen framework_version") {
		t.Errorf("unexpected error (want immutability-trigger rejection): %v", err)
	}

	// (2) App role: no UPDATE grant — permission denied (defense in depth).
	_, appErr := appPool.Exec(ctx,
		`UPDATE framework_requirements SET title = 'tampered' WHERE id = $1`, s.v1CC61Req)
	if appErr == nil {
		t.Errorf("atlas_app UPDATE of a requirement SUCCEEDED; want permission denied (no UPDATE grant)")
	}

	// The row must be unchanged.
	var title string
	if err := adminPool.QueryRow(ctx,
		`SELECT title FROM framework_requirements WHERE id = $1`, s.v1CC61Req).Scan(&title); err != nil {
		t.Fatalf("read title: %v", err)
	}
	if title != "requirement CC6.1" {
		t.Errorf("frozen requirement title changed to %q; want unchanged", title)
	}
}

// sanity: the store's same-framework guard rejects a cross-framework suggest.
func TestSuggest_RejectsCrossFramework(t *testing.T) {
	s1 := seedFramework(t, "xfw-a")
	s2 := seedFramework(t, "xfw-b")
	store := frameworkversion.NewStore(appPool)
	_, err := store.SuggestMigrations(context.Background(), s1.v1ID, s2.v1ID)
	if err == nil {
		t.Fatal("cross-framework suggest should fail")
	}
	if !errors.Is(err, frameworkversion.ErrNotSameFramework) {
		t.Errorf("want ErrNotSameFramework, got %v", err)
	}
}
