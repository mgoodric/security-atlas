//go:build integration

// #2958 — every parent link an import makes writes one child_linked row on
// the parent, by the person whose decision made it, naming the batch and
// the child's row. A link settled by the parent row's decision notes that
// row. A link the hierarchy rules refuse writes none. Same harness as
// integration_test.go.
package riskimports_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

type importLinkRow struct {
	Actor string
	After map[string]any
}

// importLinkRows reads the child_linked rows on parent.
func importLinkRows(t *testing.T, e env, parent string) []importLinkRow {
	t.Helper()
	rows, err := e.admin.Query(context.Background(), `
		SELECT actor, after_state FROM risk_audit_log
		WHERE tenant_id = $1 AND risk_id = $2::uuid AND action_type = 'child_linked'
		ORDER BY created_at, id`, e.tenant, parent)
	if err != nil {
		t.Fatalf("read child_linked rows: %v", err)
	}
	defer rows.Close()
	var out []importLinkRow
	for rows.Next() {
		var (
			r   importLinkRow
			raw []byte
		)
		if err := rows.Scan(&r.Actor, &raw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		_ = json.Unmarshal(raw, &r.After)
		out = append(out, r)
	}
	return out
}

func decidedBy(t *testing.T, e env, rowID string) string {
	t.Helper()
	var who string
	if err := e.admin.QueryRow(context.Background(),
		`SELECT decided_by FROM risk_import_rows WHERE id = $1::uuid`, rowID).Scan(&who); err != nil {
		t.Fatalf("read decided_by: %v", err)
	}
	return who
}

func TestRiskImport_ParentLinksAreAudited(t *testing.T) {
	e := newEnv(t)
	existing := seedRiskAt(t, e.app, e.tenant, "E-COMPANY", dbx.RiskLevelCompany)

	csv := "Risk ID,Title,Category,Level,Parent Risk ID\n" +
		// Waits for R-PARENT, then is linked by R-PARENT's decision.
		"R-CHILD,New child of a new parent,operational,team,R-PARENT\n" +
		"R-PARENT,New parent,operational,org,\n" +
		// Linked by its own decision.
		"R-UNDER-E,New child of an existing parent,operational,org,E-COMPANY\n" +
		// Refused (same level): no row.
		"R-LEVEL,Not below its parent,operational,org,R-PARENT\n"
	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)

	// R-CHILD first, on its own, so it waits on R-PARENT's row.
	child := stagedByRef(t, b, "R-CHILD")
	if status, raw := approveRow(t, e, e.apprJWT, b.ID, child.ID); status != http.StatusOK {
		t.Fatalf("approve R-CHILD = %d %s", status, raw)
	}
	if status, raw := approve(t, e, e.apprJWT, b.ID); status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}

	got := getBatch(t, e, b.ID)
	row := func(ref string) riskimport.BatchRow { return stagedByRef(t, got, ref) }
	parentRow, underE := row("R-PARENT"), row("R-UNDER-E")

	// R-PARENT: one row for R-CHILD (R-LEVEL was refused), by the person
	// who decided R-PARENT, noting it was settled through that row.
	onParent := importLinkRows(t, e, parentRow.RiskID)
	if len(onParent) != 1 {
		t.Fatalf("child_linked rows on R-PARENT = %+v, want exactly one (R-CHILD)", onParent)
	}
	r := onParent[0]
	if r.Actor == "" || r.Actor != decidedBy(t, e, parentRow.ID) {
		t.Errorf("R-CHILD link actor = %q, want R-PARENT's approver %q", r.Actor, decidedBy(t, e, parentRow.ID))
	}
	if r.After["child_risk_id"] != row("R-CHILD").RiskID || r.After["import_batch_id"] != b.ID ||
		r.After["import_row_id"] != child.ID || r.After["settled_by_import_row_id"] != parentRow.ID {
		t.Errorf("R-CHILD link after = %v", r.After)
	}
	if note, _ := r.After["note"].(string); note == "" {
		t.Errorf("R-CHILD link has no note: %v", r.After)
	}

	// E-COMPANY: one row for R-UNDER-E, by its own approver, no note.
	onExisting := importLinkRows(t, e, existing)
	if len(onExisting) != 1 {
		t.Fatalf("child_linked rows on E-COMPANY = %+v, want exactly one", onExisting)
	}
	r = onExisting[0]
	if r.Actor != decidedBy(t, e, underE.ID) || r.After["child_risk_id"] != underE.RiskID ||
		r.After["import_row_id"] != underE.ID || r.After["import_batch_id"] != b.ID {
		t.Errorf("R-UNDER-E link = %+v", r)
	}
	if _, ok := r.After["note"]; ok {
		t.Errorf("R-UNDER-E linked by its own decision carries a note: %v", r.After)
	}
}
