//go:build integration

// #2961: the remaining hierarchy link paths are audited.
//   - POST /v1/risks/aggregate links through LinkChildrenTx: a child_linked
//     row per child by the caller, and its level rule and cap apply (400).
//   - Deleting a risk drops its links in the delete's transaction and
//     writes a child_unlinked row per link on the parent's trail.
//   - A child's GET /v1/risks/{id}/audit-log lists its parents' link and
//     unlink rows, marked with parent_risk_id, with no second row written.
//
// Run with:
//
//	go test -tags=integration ./internal/api/risks/ -run HierarchyAudit2961

package risks_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHierarchyAudit2961_AggregateLinksAreAudited(t *testing.T) {
	env := newTransitionEnv(t)
	a := seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding A", 3, 4)
	b := seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding B", 2, 2)

	code, body := env.do(t, http.MethodPost, "/v1/risks/aggregate", env.ownerJWT, map[string]any{
		"parent":         map[string]any{"title": "Aggregated org risk", "level": "org", "severity_function": "max"},
		"child_risk_ids": []string{a.String(), b.String()},
	})
	if code != http.StatusCreated {
		t.Fatalf("aggregate: status %d: %v", code, body)
	}
	parent := uuid.MustParse(riskField(t, body, "id").(string))

	rows := childAuditRows(t, env.admin, env.tenant, parent)
	var linked []string
	for _, r := range rows {
		if r.Action != "child_linked" || r.Actor != env.owner.String() {
			t.Errorf("row %+v, want child_linked by the caller %s", r, env.owner)
		}
		linked = append(linked, r.After["child_risk_id"].(string))
	}
	slices.Sort(linked)
	want := []string{a.String(), b.String()}
	slices.Sort(want)
	if !slices.Equal(linked, want) {
		t.Fatalf("linked = %v, want %v", linked, want)
	}

	// The same aggregate again returns the existing parent and writes none.
	if code, body := env.do(t, http.MethodPost, "/v1/risks/aggregate", env.ownerJWT, map[string]any{
		"parent":         map[string]any{"title": "Aggregated org risk", "level": "org", "severity_function": "max"},
		"child_risk_ids": []string{b.String(), a.String()},
	}); code != http.StatusCreated {
		t.Fatalf("repeat aggregate: status %d: %v", code, body)
	}
	if n := len(childAuditRows(t, env.admin, env.tenant, parent)); n != 2 {
		t.Fatalf("rows after repeat = %d, want 2", n)
	}
}

func TestHierarchyAudit2961_AggregateRefusesLevelAndCap(t *testing.T) {
	env := newTransitionEnv(t)
	org := seedLeveledRisk(t, env.admin, env.tenant, "org", "org risk", 3, 3)

	// A child at the parent's level: 400, and no parent is left behind.
	code, body := env.do(t, http.MethodPost, "/v1/risks/aggregate", env.ownerJWT, map[string]any{
		"parent":         map[string]any{"title": "Same-level parent", "level": "org", "severity_function": "max"},
		"child_risk_ids": []string{org.String()},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("same-level aggregate: status %d, want 400: %v", code, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "level") {
		t.Errorf("same-level error = %v, want it to name the level", body)
	}
	var parents int
	if err := env.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND title = 'Same-level parent'`, env.tenant).Scan(&parents); err != nil {
		t.Fatalf("count parents: %v", err)
	}
	if parents != 0 {
		t.Fatalf("parents left by a refused aggregate = %d, want 0", parents)
	}

	// More than 100 children: 400.
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding", 2, 2).String()
	}
	code, body = env.do(t, http.MethodPost, "/v1/risks/aggregate", env.ownerJWT, map[string]any{
		"parent":         map[string]any{"title": "Too many", "level": "org", "severity_function": "max"},
		"child_risk_ids": ids,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("101-child aggregate: status %d, want 400: %v", code, body)
	}
}

func TestHierarchyAudit2961_DeleteWritesUnlinkRows(t *testing.T) {
	env := newTransitionEnv(t)
	company := seedLeveledRisk(t, env.admin, env.tenant, "company", "enterprise risk", 2, 2)
	org := seedLeveledRisk(t, env.admin, env.tenant, "org", "org risk", 3, 3)
	team := seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding", 4, 4)
	link := func(parent, child uuid.UUID) {
		t.Helper()
		if code, body := env.do(t, http.MethodPost, "/v1/risks/"+parent.String()+"/children", env.ownerJWT,
			map[string]any{"child_risk_ids": []string{child.String()}}); code != http.StatusOK {
			t.Fatalf("link: status %d: %v", code, body)
		}
	}
	link(company, org)
	link(org, team)

	// Deleting the middle risk drops both its links: one row on the
	// surviving parent (company), one on its own trail for its child.
	if code, body := env.do(t, http.MethodDelete, "/v1/risks/"+org.String(), env.ownerJWT, nil); code != http.StatusNoContent {
		t.Fatalf("delete: status %d: %v", code, body)
	}
	for _, c := range []struct {
		parent, child uuid.UUID
	}{{company, org}, {org, team}} {
		rows := childAuditRows(t, env.admin, env.tenant, c.parent)
		if len(rows) != 2 || rows[1].Action != "child_unlinked" {
			t.Fatalf("rows on %s = %+v, want child_linked then child_unlinked", c.parent, rows)
		}
		un := rows[1]
		if un.Actor != env.owner.String() || un.Before["child_risk_id"] != c.child.String() {
			t.Errorf("unlink row = %+v, want %s unlinked by %s", un, c.child, env.owner)
		}
		if note, _ := un.After["note"].(string); !strings.Contains(note, "deleted") {
			t.Errorf("unlink note = %v, want one naming the delete", un.After)
		}
	}
	var links int
	if err := env.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_aggregations WHERE tenant_id = $1`, env.tenant).Scan(&links); err != nil {
		t.Fatalf("count links: %v", err)
	}
	if links != 0 {
		t.Fatalf("links after delete = %d, want 0", links)
	}
}

func TestHierarchyAudit2961_ChildHistoryShowsParentRows(t *testing.T) {
	env := newTransitionEnv(t)
	org := seedLeveledRisk(t, env.admin, env.tenant, "org", "org risk", 3, 3)
	team := seedLeveledRisk(t, env.admin, env.tenant, "team", "team finding", 4, 4)
	path := "/v1/risks/" + org.String() + "/children"
	if code, body := env.do(t, http.MethodPost, path, env.ownerJWT,
		map[string]any{"child_risk_ids": []string{team.String()}}); code != http.StatusOK {
		t.Fatalf("link: status %d: %v", code, body)
	}
	if code, body := env.do(t, http.MethodDelete, path+"/"+team.String(), env.ownerJWT, nil); code != http.StatusNoContent {
		t.Fatalf("unlink: status %d: %v", code, body)
	}

	// The child's History reads the parent's two rows, marked.
	var actions []string
	for _, e := range env.rawAuditRows(t, team.String()) {
		if e["parent_risk_id"] != org.String() {
			t.Errorf("entry %v, want parent_risk_id %s", e, org)
		}
		actions = append(actions, e["action"].(string))
	}
	if !slices.Equal(actions, []string{"child_linked", "child_unlinked"}) {
		t.Fatalf("child history = %v, want [child_linked child_unlinked]", actions)
	}
	// The parent's own History carries them unmarked.
	for _, e := range env.rawAuditRows(t, org.String()) {
		if _, ok := e["parent_risk_id"]; ok {
			t.Errorf("parent's own entry %v carries parent_risk_id", e)
		}
	}
	// No second row was written: both rows are on the parent's trail.
	var onChild int
	if err := env.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_audit_log WHERE tenant_id = $1 AND risk_id = $2`, env.tenant, team).Scan(&onChild); err != nil {
		t.Fatalf("count child rows: %v", err)
	}
	if onChild != 0 {
		t.Fatalf("rows on the child's own trail = %d, want 0", onChild)
	}
}
