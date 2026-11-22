//go:build integration

// #2844 — category and theme values map once per batch, not per row. These
// drive the mapping step over HTTP: the batch's distinct values, mapping a
// value to an existing category or theme, creating one for a value (and its
// refusal for a non-approver), and approval rejecting a row whose value is
// still unmapped.
package riskimports_test

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// valuesCSV spells one category four ways and gives themes in varying case
// and separators. "Operational" and "availability" name an existing
// category and theme; the rest name none.
const valuesCSV = "Risk ID,Title,Category,Themes\n" +
	"V-1,Vendor outage,Ops,\"Vendors; Availability\"\n" +
	"V-2,Payroll error,Operational,vendors\n" +
	"V-3,Backup gap,operations,availability\n" +
	"V-4,Stale access,OPS,\n"

func mapValue(t *testing.T, e env, bearer, batchID string, body map[string]any) (int, []byte) {
	t.Helper()
	return call(t, e, http.MethodPut, bearer, "/v1/risks/imports/"+batchID+"/values", body)
}

func stageCSV(t *testing.T, e env, bearer, csv string) riskimport.Batch {
	t.Helper()
	status, raw := upload(t, e, bearer, "values.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", status, raw)
	}
	return decode[riskimport.Batch](t, raw)
}

func valuesOf(ms []riskimport.ValueMapping) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Value + "=" + m.Target + "/" + string(rune('0'+m.Rows))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// riskCategoryThemes reads an imported risk's category slug and themes.
func riskCategoryThemes(t *testing.T, e env, ref string) (string, string) {
	t.Helper()
	var cat, themes string
	if err := e.admin.QueryRow(context.Background(), `
		SELECT c.slug, array_to_string(r.themes, ',')
		FROM risks r JOIN org_risk_categories c ON c.id = r.category_id
		WHERE r.tenant_id = $1 AND r.external_ref = $2`, e.tenant, ref).Scan(&cat, &themes); err != nil {
		t.Fatalf("read risk %s: %v", ref, err)
	}
	return cat, themes
}

func TestRiskImport_ValueMapping(t *testing.T) {
	e := newEnv(t)
	b := stageCSV(t, e, e.grcJWT, valuesCSV)

	// Every row stages; the distinct values are listed once each, values
	// differing only in case counted together, with what they already
	// name.
	if b.StagedCount != 4 || b.RejectedCount != 0 {
		t.Fatalf("staged=%d rejected=%d (%+v)", b.StagedCount, b.RejectedCount, b.Rejected)
	}
	if got := valuesOf(b.CategoryValues); got != "Operational=operational/1,Ops=/2,operations=/1" {
		t.Errorf("category values = %s", got)
	}
	if got := valuesOf(b.ThemeValues); got != "Availability=availability/2,Vendors=/2" {
		t.Errorf("theme values = %s", got)
	}

	// A GRC engineer maps each variant to the existing category, by slug
	// and by name, and the theme to an existing one. Nothing is created.
	for _, m := range []map[string]any{
		{"kind": "category", "value": "ops", "target": "operational"},
		{"kind": "category", "value": "operations", "target": "Operational"},
		{"kind": "theme", "value": "VENDORS", "target": "vendor-risk"},
	} {
		status, raw := mapValue(t, e, e.grcJWT, b.ID, m)
		if status != http.StatusOK {
			t.Fatalf("map %v = %d; body=%s", m, status, raw)
		}
	}
	// A target the organisation lacks is refused and maps nothing.
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "category", "value": "Ops", "target": "nonsense"}); status != http.StatusUnprocessableEntity {
		t.Errorf("map to an unknown category = %d; body=%s", status, raw)
	}
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "theme", "value": "Vendors", "target": "vendors"}); status != http.StatusUnprocessableEntity {
		t.Errorf("map to an unknown theme = %d; body=%s", status, raw)
	}
	// A value the batch lacks is a 404; a viewer cannot map.
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "category", "value": "Finance", "target": "financial"}); status != http.StatusNotFound {
		t.Errorf("map a value the batch lacks = %d; body=%s", status, raw)
	}
	if status, raw := mapValue(t, e, e.viewJWT, b.ID, map[string]any{"kind": "category", "value": "Ops", "target": "financial"}); status != http.StatusForbidden {
		t.Errorf("viewer map = %d; body=%s", status, raw)
	}

	status, raw := call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET batch = %d; body=%s", status, raw)
	}
	read := decode[riskimport.Batch](t, raw)
	if got := valuesOf(read.CategoryValues); got != "Operational=operational/1,Ops=operational/2,operations=operational/1" {
		t.Errorf("category values after mapping = %s", got)
	}
	if got := valuesOf(read.ThemeValues); got != "Availability=availability/2,Vendors=vendor-risk/2" {
		t.Errorf("theme values after mapping = %s", got)
	}
	if r := stagedByRef(t, read, "V-1"); r.Category != "operational" || strings.Join(r.Themes, ",") != "vendor-risk,availability" {
		t.Errorf("V-1 staged as category=%q themes=%v", r.Category, r.Themes)
	}

	// Approval: every row takes the mapped category and themes.
	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", status, raw)
	}
	if res := decode[riskimport.ApproveResult](t, raw); res.ApprovedCount != 4 || len(res.Rejected) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("approve result = %+v", res)
	}
	for ref, want := range map[string]string{
		"V-1": "operational|availability,vendor-risk",
		"V-2": "operational|vendor-risk",
		"V-3": "operational|availability",
		"V-4": "operational|",
	} {
		cat, themes := riskCategoryThemes(t, e, ref)
		if got := cat + "|" + themes; got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
	// The themes are on the risk's trail, written by the approver.
	var n int
	if err := e.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM risk_audit_log a JOIN risks r ON r.id = a.risk_id
		WHERE r.tenant_id = $1 AND a.action_type = 'themes_changed' AND a.actor = $2`,
		e.tenant, e.approver.String()).Scan(&n); err != nil {
		t.Fatalf("count themes_changed: %v", err)
	}
	if n != 3 {
		t.Errorf("themes_changed rows = %d, want 3 (the rows with themes)", n)
	}
	// Seven categories seeded, none added.
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM org_risk_categories WHERE tenant_id = $1`, e.tenant).Scan(&n); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if n != 7 {
		t.Errorf("categories = %d, want the seeded 7", n)
	}

	// Once completed, the mapping is final.
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "category", "value": "Ops", "target": "financial"}); status != http.StatusConflict {
		t.Errorf("map on a completed batch = %d; body=%s", status, raw)
	}
}

func TestRiskImport_ValueCreation(t *testing.T) {
	e := newEnv(t)
	b := stageCSV(t, e, e.grcJWT, valuesCSV)

	countCategory := func(slug string) int {
		t.Helper()
		var n int
		if err := e.admin.QueryRow(context.Background(),
			`SELECT count(*) FROM org_risk_categories WHERE tenant_id = $1 AND slug = $2`, e.tenant, slug).Scan(&n); err != nil {
			t.Fatalf("count category: %v", err)
		}
		return n
	}
	countTheme := func(name string) int {
		t.Helper()
		var n int
		if err := e.admin.QueryRow(context.Background(),
			`SELECT count(*) FROM org_themes WHERE tenant_id = $1 AND theme_name = $2`, e.tenant, name).Scan(&n); err != nil {
			t.Fatalf("count theme: %v", err)
		}
		return n
	}

	// Neither a viewer nor grc_engineer carried only as a JWT role claim,
	// with no live grant (#2381), is a tenant approver: creating a category
	// or theme for a value is refused and creates nothing. (A live
	// grc_engineer grant is an approver, #2428.)
	claimOnly := e.issue(t, testjwt.OwnerFor(uuid.MustParse(e.tenant), []string{"grc_engineer"}))
	create := map[string]any{"kind": "category", "value": "Ops", "create": map[string]any{"slug": "ops", "name": "Ops"}}
	createTheme := map[string]any{"kind": "theme", "value": "Vendors", "create": map[string]any{"name": "vendors"}}
	for name, bearer := range map[string]string{"non-approver": claimOnly, "viewer": e.viewJWT} {
		if status, raw := mapValue(t, e, bearer, b.ID, create); status != http.StatusForbidden {
			t.Errorf("%s create category = %d; body=%s", name, status, raw)
		}
		if status, raw := mapValue(t, e, bearer, b.ID, createTheme); status != http.StatusForbidden {
			t.Errorf("%s create theme = %d; body=%s", name, status, raw)
		}
	}
	if countCategory("ops") != 0 || countTheme("vendors") != 0 {
		t.Fatalf("a refused create created something")
	}
	// Creating for a value the batch lacks creates nothing.
	if status, raw := mapValue(t, e, e.apprJWT, b.ID, map[string]any{
		"kind": "category", "value": "Finance", "create": map[string]any{"slug": "finance", "name": "Finance"},
	}); status != http.StatusNotFound || countCategory("finance") != 0 {
		t.Errorf("create for a value the batch lacks = %d; body=%s", status, raw)
	}

	// The approver creates both through the audited creation; each value
	// maps to what was created.
	status, raw := mapValue(t, e, e.apprJWT, b.ID, create)
	if status != http.StatusOK {
		t.Fatalf("approver create category = %d; body=%s", status, raw)
	}
	if m := decode[riskimport.ValueMapping](t, raw); m.Target != "ops" || m.Rows != 2 {
		t.Errorf("created category mapping = %+v", m)
	}
	status, raw = mapValue(t, e, e.apprJWT, b.ID, createTheme)
	if status != http.StatusOK {
		t.Fatalf("approver create theme = %d; body=%s", status, raw)
	}
	if countCategory("ops") != 1 || countTheme("vendors") != 1 {
		t.Fatalf("approver create did not create the category and theme")
	}
	var audited int
	if err := e.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM taxonomy_audit_log
		WHERE tenant_id = $1 AND action = 'create' AND subject_slug IN ('ops', 'vendors')`, e.tenant).Scan(&audited); err != nil {
		t.Fatalf("read taxonomy audit: %v", err)
	}
	if audited != 2 {
		t.Errorf("taxonomy audit create rows = %d, want 2", audited)
	}
	// A second create of the same slug is the creation's own conflict.
	if status, raw := mapValue(t, e, e.apprJWT, b.ID, map[string]any{
		"kind": "category", "value": "operations", "create": map[string]any{"slug": "ops", "name": "Ops again"},
	}); status != http.StatusConflict {
		t.Errorf("create a taken slug = %d; body=%s", status, raw)
	}
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "category", "value": "operations", "target": "ops"}); status != http.StatusOK {
		t.Fatalf("map to the created category = %d; body=%s", status, raw)
	}

	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", status, raw)
	}
	if res := decode[riskimport.ApproveResult](t, raw); res.ApprovedCount != 4 {
		t.Fatalf("approve result = %+v", res)
	}
	for ref, want := range map[string]string{
		"V-1": "ops|availability,vendors",
		"V-2": "operational|vendors",
		"V-3": "ops|availability",
		"V-4": "ops|",
	} {
		cat, themes := riskCategoryThemes(t, e, ref)
		if got := cat + "|" + themes; got != want {
			t.Errorf("%s = %s, want %s", ref, got, want)
		}
	}
}

func TestRiskImport_UnmappedValueRejectsRow(t *testing.T) {
	e := newEnv(t)
	b := stageCSV(t, e, e.grcJWT, valuesCSV)
	// Only "Ops" is mapped: V-1 still has the theme Vendors unmapped, V-2
	// too, V-3 the category operations; V-4 is fully mapped.
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "category", "value": "Ops", "target": "operational"}); status != http.StatusOK {
		t.Fatalf("map = %d; body=%s", status, raw)
	}
	// Clearing a mapping returns the value to unmapped.
	if status, raw := mapValue(t, e, e.grcJWT, b.ID, map[string]any{"kind": "category", "value": "Operational"}); status != http.StatusOK {
		t.Fatalf("clear mapping = %d; body=%s", status, raw)
	}

	// One row through the per-row route: rejected with the reason.
	v3 := stagedByRef(t, b, "V-3")
	status, raw := call(t, e, http.MethodPost, e.apprJWT, "/v1/risks/imports/"+b.ID+"/rows/"+v3.ID+"/approve", nil)
	if status != http.StatusOK {
		t.Fatalf("approve V-3 = %d; body=%s", status, raw)
	}
	if d := decode[riskimport.RowDecision](t, raw); d.Status != riskimport.RowStatusRejected || d.RiskID != "" ||
		!strings.Contains(d.RejectionReason, `category "operations"`) {
		t.Errorf("V-3 decision = %+v", d)
	}

	// The rest through approve-all.
	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d; body=%s", status, raw)
	}
	res := decode[riskimport.ApproveResult](t, raw)
	if res.ApprovedCount != 1 || res.Approved[0].ExternalRef != "V-4" || len(res.Rejected) != 2 {
		t.Fatalf("approve result = %+v", res)
	}
	reasons := map[string]string{}
	for _, d := range res.Rejected {
		reasons[d.ExternalRef] = d.RejectionReason
	}
	if !strings.Contains(reasons["V-1"], `theme "Vendors"`) || strings.Contains(reasons["V-1"], "category") {
		t.Errorf("V-1 reason = %q", reasons["V-1"])
	}
	if !strings.Contains(reasons["V-2"], `category "Operational"`) || !strings.Contains(reasons["V-2"], `theme "vendors"`) {
		t.Errorf("V-2 reason = %q", reasons["V-2"])
	}

	// No risk for a rejected row; each rejection is on the batch and in
	// the import audit, naming the approver.
	var risks, audit int
	if err := e.admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND external_ref IN ('V-1', 'V-2', 'V-3')`, e.tenant).Scan(&risks); err != nil {
		t.Fatalf("count risks: %v", err)
	}
	if err := e.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM risk_import_audit_log
		WHERE tenant_id = $1 AND batch_id = $2 AND action = 'reject' AND actor = $3 AND reason LIKE 'not mapped%'`,
		e.tenant, b.ID, e.approver.String()).Scan(&audit); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if risks != 0 || audit != 3 {
		t.Errorf("risks for rejected rows = %d (want 0), reject audit rows = %d (want 3)", risks, audit)
	}
	status, raw = call(t, e, http.MethodGet, e.apprJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET batch = %d; body=%s", status, raw)
	}
	for _, r := range decode[riskimport.Batch](t, raw).Staged {
		if r.ExternalRef != "V-4" && (r.Status != riskimport.RowStatusRejected || r.RejectionReason == "") {
			t.Errorf("%s status=%s reason=%q", r.ExternalRef, r.Status, r.RejectionReason)
		}
	}
}

// TestRiskImport_BlankCategoryRejectedAtUpload: a blank or whitespace-only
// category cell is refused as a missing required field on that row; the
// upload still stages the rest and lists no blank value.
func TestRiskImport_BlankCategoryRejectedAtUpload(t *testing.T) {
	e := newEnv(t)
	b := stageCSV(t, e, e.grcJWT, "Risk ID,Title,Category,Themes\n"+
		"B-1,Blank category,,availability\n"+
		"B-2,Spaces only,   ,\n"+
		"B-3,Fine,operational,\" ; ,\"\n")
	if b.StagedCount != 1 || b.RejectedCount != 2 {
		t.Fatalf("staged=%d rejected=%d (%+v)", b.StagedCount, b.RejectedCount, b.Rejected)
	}
	for _, r := range b.Rejected {
		if r.Reason != riskimport.RejectMissingField || r.Detail != "missing category" {
			t.Errorf("%s rejected as %s %q", r.ExternalRef, r.Reason, r.Detail)
		}
	}
	if got := valuesOf(b.CategoryValues); got != "operational=operational/1" {
		t.Errorf("category values = %s", got)
	}
	if len(b.ThemeValues) != 0 {
		t.Errorf("theme values = %+v, want none", b.ThemeValues)
	}
}
