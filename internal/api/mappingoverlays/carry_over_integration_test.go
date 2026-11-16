//go:build integration

// #1803 — overlay edges carry over to a new framework version when the ADR
// 0019 migration queue's exact_code row is approved. Runs against a real
// Postgres with RLS: the approval runs through frameworkversion.Store on the
// non-owner app role, and every read goes through the tenant API servers.
//
// Fixture: newCoverageFixture (#1798) on version N ("2026"): tenant A has
// an active overlay edge C2 -> IAC-06 and a retired one C3 -> CRY-03;
// tenant B has its own active C2 -> IAC-06. Version N+1 ("2027") has C1-C3
// plus C4, which is new in N+1.

package mappingoverlays_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkversion"
)

// storedEdge is an overlay edge row as the migrate pool reads it, past RLS.
type storedEdge struct {
	id, tenant, anchor              uuid.UUID
	relationship, rationale, source string
	createdBy, status               string
	strength                        float64
	updatedAt                       time.Time
}

func edgesOn(t *testing.T, admin *pgxpool.Pool, requirement uuid.UUID) []storedEdge {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT id, tenant_id, scf_anchor_id, relationship_type::text, rationale, source_ref,
		       created_by, status, strength, updated_at
		FROM fw_to_scf_overlay_edges WHERE framework_requirement_id = $1
		ORDER BY tenant_id, scf_anchor_id, status`, requirement)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []storedEdge
	for rows.Next() {
		var e storedEdge
		if err := rows.Scan(&e.id, &e.tenant, &e.anchor, &e.relationship, &e.rationale, &e.source,
			&e.createdBy, &e.status, &e.strength, &e.updatedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type carryOverFixture struct {
	coverageFixture
	store     *frameworkversion.Store
	reviewer  uuid.UUID
	versionN  uuid.UUID
	versionN1 uuid.UUID
	next      map[string]uuid.UUID // N+1 requirement code -> id
	edgeA     string               // tenant A's active C2 -> IAC-06
	edgeB     string               // tenant B's active C2 -> IAC-06
	retiredA  string               // tenant A's retired C3 -> CRY-03
}

func newCarryOverFixture(t *testing.T) carryOverFixture {
	t.Helper()
	ctx := context.Background()
	f := carryOverFixture{
		coverageFixture: newCoverageFixture(t),
		store:           frameworkversion.NewStore(dbtest.NewAppPool(t)),
		reviewer:        uuid.New(),
		versionN1:       uuid.New(),
		next:            map[string]uuid.UUID{},
	}
	if err := f.admin.QueryRow(ctx, `SELECT id FROM framework_versions WHERE framework_id = $1 AND version = $2`,
		f.frameworkID, f.version).Scan(&f.versionN); err != nil {
		t.Fatalf("read version N: %v", err)
	}

	create := func(e env, code, anchor string) string {
		t.Helper()
		body := f.body(code, anchor)
		if e.tenant == f.tenantB.tenant {
			body["rationale"] = "tenant B's own attestation for " + code
			body["source_ref"] = "Tenant B FY25 report, " + code
		}
		status, resp := call(t, e, http.MethodPost, "/v1/mapping-overlays", e.admin, body)
		if status != http.StatusCreated {
			t.Fatalf("create overlay %s -> %s: status %d (%v)", code, anchor, status, resp)
		}
		return edgeOf(t, resp)["id"].(string)
	}
	f.edgeA = create(f.tenantA, "C2", "IAC-06")
	f.edgeB = create(f.tenantB, "C2", "IAC-06")
	f.retiredA = create(f.tenantA, "C3", "CRY-03")
	if status, resp := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+f.retiredA+"/retire",
		f.tenantA.admin, map[string]any{"reason": "auditor withdrew the finding"}); status != http.StatusOK {
		t.Fatalf("retire overlay: status %d (%v)", status, resp)
	}

	if _, err := f.admin.Exec(ctx, `INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '2027', 'legacy')`, f.versionN1, f.frameworkID); err != nil {
		t.Fatalf("seed version N+1: %v", err)
	}
	for _, code := range []string{"C1", "C2", "C3", "C4"} {
		id := uuid.New()
		if _, err := f.admin.Exec(ctx, `INSERT INTO framework_requirements (id, framework_version_id, code, title)
			VALUES ($1, $2, $3, $3)`, id, f.versionN1, code); err != nil {
			t.Fatalf("seed N+1 requirement %s: %v", code, err)
		}
		f.next[code] = id
	}
	// The catalog carries C1 -> IAC-06 into N+1 itself (the loader's job).
	if _, err := f.admin.Exec(ctx, `
		INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, source_attribution, mapping_tier)
		VALUES ($1, $2, $3, 'equal', 1.0, 'scf_official', 'verified')`,
		uuid.New(), f.next["C1"], f.anchorID); err != nil {
		t.Fatalf("seed N+1 catalog edge: %v", err)
	}

	if _, err := f.store.SuggestMigrations(ctx, f.versionN, f.versionN1); err != nil {
		t.Fatalf("suggest migrations: %v", err)
	}
	return f
}

// migration returns the queue row id for a requirement code and match kind.
func (f carryOverFixture) migration(t *testing.T, code, kind string) uuid.UUID {
	t.Helper()
	rows, err := f.store.ListMigrations(context.Background(), f.versionN, f.versionN1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.RequirementCode == code && string(r.MatchKind) == kind {
			return uuid.UUID(r.ID.Bytes)
		}
	}
	t.Fatalf("no %s migration row for %s", kind, code)
	return uuid.Nil
}

func (f carryOverFixture) approve(t *testing.T, code, kind string) frameworkversion.Decision {
	t.Helper()
	d, err := f.store.DecideMigration(context.Background(), f.migration(t, code, kind), f.reviewer, true, "carries over")
	if err != nil {
		t.Fatalf("approve %s %s: %v", kind, code, err)
	}
	return d
}

func TestOverlayCarryOver_CopiesActiveEdgesWithIdenticalFieldsAndLogs(t *testing.T) {
	f := newCarryOverFixture(t)
	ctx := context.Background()
	beforeN := edgesOn(t, f.admin, f.requirement["C2"])

	d := f.approve(t, "C2", "exact_code")
	if d.OverlayEdgesCopied != 2 {
		t.Errorf("OverlayEdgesCopied = %d; want 2 (one per tenant)", d.OverlayEdgesCopied)
	}

	// Each tenant's copy is in that tenant, with the source's fields.
	copies := edgesOn(t, f.admin, f.next["C2"])
	if len(copies) != 2 {
		t.Fatalf("N+1 C2 overlay edges = %d; want 2: %+v", len(copies), copies)
	}
	byTenant := map[uuid.UUID]storedEdge{}
	for _, c := range copies {
		byTenant[c.tenant] = c
	}
	for _, src := range beforeN {
		c, ok := byTenant[src.tenant]
		if !ok {
			t.Errorf("no N+1 copy for tenant %s", src.tenant)
			continue
		}
		if c.id == src.id || c.anchor != src.anchor || c.relationship != src.relationship ||
			c.strength != src.strength || c.rationale != src.rationale || c.source != src.source ||
			c.createdBy != src.createdBy || c.status != "active" {
			t.Errorf("tenant %s copy = %+v; want the fields of %+v, active, a new id", src.tenant, c, src)
		}
	}

	// The copy is logged in tenant A's audit trail, as carry_over by the reviewer.
	copyA := byTenant[uuid.MustParse(f.tenantA.tenant)]
	var action, actor, carriedFrom, version string
	if err := f.admin.QueryRow(ctx, `
		SELECT action, actor, after->>'carried_from_edge_id', after->>'framework_version'
		FROM fw_to_scf_overlay_edge_audit_log WHERE tenant_id = $1 AND overlay_edge_id = $2`,
		f.tenantA.tenant, copyA.id).Scan(&action, &actor, &carriedFrom, &version); err != nil {
		t.Fatalf("read carry-over audit row: %v", err)
	}
	if action != "carry_over" || actor != f.reviewer.String() || carriedFrom != f.edgeA || version != "2027" {
		t.Errorf("audit row = %s by %s from %s in %s; want carry_over by %s from %s in 2027",
			action, actor, carriedFrom, version, f.reviewer, f.edgeA)
	}
	// Tenant A's list now holds its N edge and the N+1 copy.
	if got := list(t, f.tenantA, "?requirement=C2&status=active"); len(got) != 2 {
		t.Errorf("tenant A active C2 edges across versions = %d; want 2 (N and N+1)", len(got))
	}

	// Edges on N are untouched.
	afterN := edgesOn(t, f.admin, f.requirement["C2"])
	if len(afterN) != len(beforeN) {
		t.Fatalf("N C2 edges = %d; want %d", len(afterN), len(beforeN))
	}
	for i := range beforeN {
		if afterN[i] != beforeN[i] {
			t.Errorf("N edge changed: %+v -> %+v", beforeN[i], afterN[i])
		}
	}

	// Idempotent: a second decide is refused and a re-run of the copy adds nothing.
	if _, err := f.store.DecideMigration(ctx, f.migration(t, "C2", "exact_code"), f.reviewer, true, "again"); !errors.Is(err, frameworkversion.ErrAlreadyDecided) {
		t.Errorf("second approve: err = %v; want ErrAlreadyDecided", err)
	}
	var again int
	if err := f.admin.QueryRow(ctx, `SELECT carry_over_overlay_edges($1, $2)`,
		f.migration(t, "C2", "exact_code"), f.reviewer.String()).Scan(&again); err != nil {
		t.Fatalf("re-run carry-over: %v", err)
	}
	if again != 0 || len(edgesOn(t, f.admin, f.next["C2"])) != 2 {
		t.Errorf("re-run copied %d; want 0 and still 2 edges on N+1", again)
	}
}

func TestOverlayCarryOver_SkipsRetiredNewAndRejectedAndKeepsTenantsApart(t *testing.T) {
	f := newCarryOverFixture(t)
	ctx := context.Background()

	// C3's only overlay edge is retired: nothing to copy.
	if d := f.approve(t, "C3", "exact_code"); d.OverlayEdgesCopied != 0 {
		t.Errorf("C3 copied %d; want 0 (its only edge is retired)", d.OverlayEdgesCopied)
	}
	if got := edgesOn(t, f.admin, f.next["C3"]); len(got) != 0 {
		t.Errorf("N+1 C3 overlay edges = %+v; want none", got)
	}
	// C4 is new in N+1: approving its 'added' row copies nothing.
	if d := f.approve(t, "C4", "added"); d.OverlayEdgesCopied != 0 {
		t.Errorf("C4 copied %d; want 0", d.OverlayEdgesCopied)
	}
	if got := edgesOn(t, f.admin, f.next["C4"]); len(got) != 0 {
		t.Errorf("N+1 C4 overlay edges = %+v; want none", got)
	}
	// A rejected carry-over copies nothing, and the function refuses it.
	c2 := f.migration(t, "C2", "exact_code")
	if d, err := f.store.DecideMigration(ctx, c2, f.reviewer, false, "not the same criterion"); err != nil || d.OverlayEdgesCopied != 0 {
		t.Fatalf("reject C2: %+v, %v", d, err)
	}
	if got := edgesOn(t, f.admin, f.next["C2"]); len(got) != 0 {
		t.Errorf("rejected C2 carry-over left edges %+v", got)
	}
	if _, err := f.admin.Exec(ctx, `SELECT carry_over_overlay_edges($1, $2)`, c2, f.reviewer.String()); err == nil {
		t.Error("carry_over_overlay_edges on a rejected migration succeeded; want refused")
	}

	// Tenant B's edges are not touched, and each tenant sees only its own.
	f2 := newCarryOverFixture(t)
	bBefore := edgesOn(t, f2.admin, f2.requirement["C2"])
	f2.approve(t, "C2", "exact_code")
	if after := edgesOn(t, f2.admin, f2.requirement["C2"]); len(after) != len(bBefore) || after[0] != bBefore[0] || after[1] != bBefore[1] {
		t.Errorf("N edges changed by the carry-over: %+v -> %+v", bBefore, after)
	}
	for _, tc := range []struct {
		e         env
		rationale string
	}{
		{f2.tenantA, "auditor tested the confidentiality-agreement control under C2"},
		{f2.tenantB, "tenant B's own attestation for C2"},
	} {
		for _, edge := range list(t, tc.e, "?requirement=C2") {
			if edge["rationale"] != tc.rationale {
				t.Errorf("tenant %s sees edge %v; want only its own (%q)", tc.e.tenant, edge, tc.rationale)
			}
		}
	}
}

func TestOverlayCarryOver_CoverageAndAuditedUnderOnNewVersion(t *testing.T) {
	f := newCarryOverFixture(t)
	ctx := context.Background()
	if _, err := f.store.Promote(ctx, f.versionN1, f.reviewer, "2027 is current"); err != nil {
		t.Fatalf("promote N+1: %v", err)
	}
	// Before the approval, N+1's C2 has no overlay edge: the report lists it.
	if !reportHasCode(t, f.tenantA, f.framework, "C2") {
		t.Fatal("audited-under does not list C2 on N+1 before the carry-over; the check below would prove nothing")
	}
	f.approve(t, "C2", "exact_code")

	var copyA string
	if err := f.admin.QueryRow(ctx, `SELECT id FROM fw_to_scf_overlay_edges WHERE tenant_id = $1 AND framework_requirement_id = $2`,
		f.tenantA.tenant, f.next["C2"]).Scan(&copyA); err != nil {
		t.Fatalf("read tenant A copy: %v", err)
	}

	// Per-requirement coverage on N+1 C2 reaches IAC-06 through A's copy.
	req := decode(t, rawGet(t, f.tenantA, "/v1/requirements/"+f.next["C2"].String()+"/coverage"))
	anchor := pathTo(req["anchors"], "scf_id", "IAC-06")
	if anchor == nil || anchor["origin"] != "overlay" || anchor["edge_id"] != copyA {
		t.Errorf("N+1 C2 -> IAC-06 = %v; want the overlay copy %s", anchor, copyA)
	}
	if pathTo(req["controls"], "id", f.controlA.String()) == nil {
		t.Errorf("N+1 C2 coverage does not list tenant A's control: %v", req)
	}
	// Per-control coverage includes N+1 C2 through the copy.
	row := pathTo(decode(t, rawGet(t, f.tenantA, "/v1/controls/"+f.controlA.String()+"/coverage"))["requirements"],
		"requirement_id", f.next["C2"].String())
	if row == nil || row["origin"] != "overlay" || row["edge_id"] != copyA {
		t.Errorf("control coverage N+1 C2 row = %v; want the overlay copy %s", row, copyA)
	}
	// N+1 C3's tenant A edge was retired on N, so N+1 C3 is not reached.
	if a := pathTo(decode(t, rawGet(t, f.tenantA, "/v1/requirements/"+f.next["C3"].String()+"/coverage"))["anchors"], "scf_id", "CRY-03"); a != nil {
		t.Errorf("N+1 C3 reaches CRY-03 through a retired edge: %v", a)
	}

	// The audited-under report resolves C2 in the current version (now N+1):
	// tenant A's control reaches it through the copy, so C2 is not a gap.
	if reportHasCode(t, f.tenantA, f.framework, "C2") {
		t.Error("audited-under still lists C2 after the carry-over")
	}
}

func reportHasCode(t *testing.T, e env, framework, code string) bool {
	t.Helper()
	_, rows := reportRows(t, e, e.admin, "?framework="+framework)
	for _, r := range rows {
		if r["requirement_code"] == code {
			return true
		}
	}
	return false
}
