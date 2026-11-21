//go:build integration

// #2842 — treatment and acceptance arrive under the rules a single create
// follows. Same harness as integration_test.go.
package riskimports_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// seedImportControl inserts an active control carrying bundleID.
func seedImportControl(t *testing.T, admin *pgxpool.Pool, tenant, bundleID string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, $3, 'AC', 'manual_attested', $3)
	`, id, tenant, bundleID); err != nil {
		t.Fatalf("seed control %s: %v", bundleID, err)
	}
	return id
}

// importedRisk is what the tests read back from a created risk.
type importedRisk struct {
	Treatment, AcceptanceStatus, Accepter, ProposedBy, Justification, ProposedUntil, Instrument string
	Controls                                                                                    []string
}

func readImportedRisk(t *testing.T, e env, id string) importedRisk {
	t.Helper()
	var r importedRisk
	if err := e.admin.QueryRow(context.Background(), `
		SELECT r.treatment::text, COALESCE(r.acceptance_status::text, ''), r.accepter,
		       COALESCE(r.acceptance_proposed_by, ''), COALESCE(r.acceptance_justification, ''),
		       COALESCE(to_char(r.acceptance_proposed_until, 'YYYY-MM-DD'), ''), r.instrument_reference,
		       COALESCE((SELECT array_agg(l.control_id::text ORDER BY l.control_id)
		                 FROM risk_control_links l WHERE l.risk_id = r.id), '{}')
		FROM risks r WHERE r.id = $1`, id).Scan(&r.Treatment, &r.AcceptanceStatus, &r.Accepter, &r.ProposedBy,
		&r.Justification, &r.ProposedUntil, &r.Instrument, &r.Controls); err != nil {
		t.Fatalf("read risk %s: %v", id, err)
	}
	return r
}

func TestRiskImport_TreatmentAndAcceptance(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tables := append(cleanupTables(), "controls")
	e := newEnvFor(t, admin, app, dbtest.SeedTenant(t, admin, tables...))
	ac1 := seedImportControl(t, admin, e.tenant, "AC-1-"+e.tenant[:8])
	ac2 := seedImportControl(t, admin, e.tenant, "AC-2-"+e.tenant[:8])
	// Another tenant's control never resolves.
	other := dbtest.SeedTenant(t, admin, tables...)
	foreign := "FOREIGN-" + other[:8]
	seedImportControl(t, admin, other, foreign)

	until := time.Now().UTC().AddDate(0, 6, 0).Format(time.DateOnly)
	csv := fmt.Sprintf("Risk ID,Title,Category,Treatment,Justification,Accepted Until,Accepted By,Instrument,Linked Controls\n"+
		"R-ACC,Accepted in the register,operational,accepted,Fix costs more than the exposure,%[1]s,Jo the former CISO,,\n"+
		"R-MIT,Mitigated by two controls,operational,mitigate,,,,,\"%[2]s; %[3]s\"\n"+
		"R-MIT-NONE,Mitigated with no links,operational,mitigate,,,,,\n"+
		"R-MIT-BAD,Mitigated by unknown controls,operational,mitigate,,,,,\"NO-SUCH; %[4]s\"\n"+
		"R-TRA,Transferred to insurance,operational,transfer,,,,POL-2027-14,\n"+
		"R-TRA-NONE,Transferred with no instrument,operational,transfer,,,,,\n"+
		"R-ACC-NOWHY,Accepted with no justification,operational,accept,,%[1]s,,,\n",
		until, "AC-1-"+e.tenant[:8], ac2.String(), foreign)

	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	if b.StagedCount != 6 || b.RejectedCount != 1 || b.Rejected[0].ExternalRef != "R-ACC-NOWHY" ||
		b.Rejected[0].Reason != riskimport.RejectInvalidAcceptance {
		t.Fatalf("batch staged=%d rejected=%+v", b.StagedCount, b.Rejected)
	}

	// Staging: the accepted row keeps its proposal and its accepter as
	// history; mitigate and transfer rows the register cannot back are
	// downgraded and flagged.
	wantLinks := []string{ac1.String(), ac2.String()}
	slices.Sort(wantLinks)
	for _, tc := range []struct {
		ref, treatment, requested string
		flagged                   bool
		links, unresolved         []string
	}{
		{"R-ACC", "accept", "", false, []string{}, []string{}},
		{"R-MIT", "mitigate", "", false, wantLinks, []string{}},
		{"R-MIT-NONE", "untreated", "mitigate", true, []string{}, []string{}},
		{"R-MIT-BAD", "untreated", "mitigate", true, []string{}, []string{"NO-SUCH", foreign}},
		{"R-TRA", "transfer", "", false, []string{}, []string{}},
		{"R-TRA-NONE", "untreated", "transfer", true, []string{}, []string{}},
	} {
		r := stagedByRef(t, b, tc.ref)
		flagged := slices.Contains(r.Flags, riskimport.FlagTreatmentDowngraded)
		if r.Treatment != tc.treatment || r.RequestedTreatment != tc.requested || flagged != tc.flagged ||
			!slices.Equal(r.LinkedControlIDs, tc.links) || !slices.Equal(r.UnresolvedControls, tc.unresolved) {
			t.Errorf("%s staged as %+v", tc.ref, r)
		}
	}
	acc := stagedByRef(t, b, "R-ACC")
	if acc.Justification != "Fix costs more than the exposure" || acc.ProposedUntil != until ||
		acc.HistoricalAccepter != "Jo the former CISO" {
		t.Errorf("R-ACC staged as %+v", acc)
	}

	// The batch read shows the same staged values.
	status, raw = call(t, e, http.MethodGet, e.grcJWT, "/v1/risks/imports/"+b.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("get batch = %d %s", status, raw)
	}
	if got := stagedByRef(t, decode[riskimport.Batch](t, raw), "R-ACC"); got.HistoricalAccepter != "Jo the former CISO" ||
		got.Treatment != "accept" || got.ProposedUntil != until {
		t.Errorf("R-ACC read back as %+v", got)
	}

	status, raw = approve(t, e, e.apprJWT, b.ID)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, raw)
	}
	res := decode[riskimport.ApproveResult](t, raw)
	if res.ApprovedCount != 6 || len(res.Skipped) != 0 {
		t.Fatalf("approve result = %+v", res)
	}
	riskOf := map[string]string{}
	for _, d := range res.Approved {
		riskOf[d.ExternalRef] = d.RiskID
	}

	// No imported risk arrives with an approved acceptance; the accepted
	// row is proposed by the person who approved the import row, and the
	// register's accepter is not the risk's accepter.
	var approved int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND acceptance_status = 'approved'`, e.tenant).Scan(&approved); err != nil {
		t.Fatalf("count approved acceptances: %v", err)
	}
	if approved != 0 {
		t.Errorf("approved acceptances = %d, want 0", approved)
	}
	for ref, want := range map[string]importedRisk{
		"R-ACC": {Treatment: "accept", AcceptanceStatus: "proposed", ProposedBy: e.approver.String(),
			Justification: "Fix costs more than the exposure", ProposedUntil: until, Controls: []string{}},
		"R-MIT":      {Treatment: "mitigate", Controls: wantLinks},
		"R-MIT-NONE": {Treatment: "untreated", Controls: []string{}},
		"R-MIT-BAD":  {Treatment: "untreated", Controls: []string{}},
		"R-TRA":      {Treatment: "transfer", Instrument: "POL-2027-14", Controls: []string{}},
		"R-TRA-NONE": {Treatment: "untreated", Controls: []string{}},
	} {
		got := readImportedRisk(t, e, riskOf[ref])
		if got.Treatment != want.Treatment || got.AcceptanceStatus != want.AcceptanceStatus || got.Accepter != "" ||
			got.ProposedBy != want.ProposedBy || got.Justification != want.Justification ||
			got.ProposedUntil != want.ProposedUntil || got.Instrument != want.Instrument ||
			!slices.Equal(got.Controls, want.Controls) {
			t.Errorf("%s created as %+v, want %+v", ref, got, want)
		}
	}

	// The acceptance is approved in the tool, by a person other than its
	// proposer: the import's approver is refused, a second approver is not.
	path := "/v1/risks/" + riskOf["R-ACC"] + "/acceptance/approve"
	body := map[string]string{"accepted_until": until}
	if status, raw := call(t, e, http.MethodPatch, e.apprJWT, path, body); status != http.StatusForbidden {
		t.Fatalf("proposer approves own acceptance = %d %s, want 403", status, raw)
	}
	second := seedUser(t, admin, e.tenant, "second-approver")
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'admin', 'test-2842')`,
		e.tenant, second.String()); err != nil {
		t.Fatalf("grant second approver: %v", err)
	}
	claims := testjwt.OwnerFor(uuid.MustParse(e.tenant), []string{"admin"})
	claims.Subject = "user:" + second.String()
	status, raw = call(t, e, http.MethodPatch, e.issue(t, claims), path, body)
	if status != http.StatusOK {
		t.Fatalf("second approver approves acceptance = %d %s", status, raw)
	}
	var out struct {
		Risk struct {
			AcceptanceStatus string `json:"acceptance_status"`
			Accepter         string `json:"accepter"`
		} `json:"risk"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode approval: %v", err)
	}
	if out.Risk.AcceptanceStatus != "approved" || out.Risk.Accepter != second.String() {
		t.Errorf("approved acceptance = %+v", out.Risk)
	}
}

// supersedeControl replaces prior with a new active version of the same
// bundle, in the order controls_one_active_version_per_bundle allows: the
// prior row is flipped first, under the deferred self-FK.
func supersedeControl(t *testing.T, admin *pgxpool.Pool, tenant string, prior uuid.UUID, bundleID string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	next := uuid.New()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE controls SET superseded_by = $2 WHERE id = $1`, prior, next); err != nil {
		t.Fatalf("supersede %s: %v", bundleID, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id, version)
		VALUES ($1, $2, $3, 'AC', 'manual_attested', $3, 2)`, next, tenant, bundleID); err != nil {
		t.Fatalf("insert successor %s: %v", bundleID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit supersede: %v", err)
	}
	return next
}

// TestRiskImport_LinkedControlsActiveOnly: only an active control version
// is linked, at upload and again at approval, and a bundle id names at most
// one active control, so a bundle ref is never ambiguous.
func TestRiskImport_LinkedControlsActiveOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	e := newEnvFor(t, admin, app, dbtest.SeedTenant(t, admin, append(cleanupTables(), "controls")...))
	bundleA, bundleB := "SUP-A-"+e.tenant[:8], "SUP-B-"+e.tenant[:8]
	ctlA := seedImportControl(t, admin, e.tenant, bundleA)
	ctlB := seedImportControl(t, admin, e.tenant, bundleB)

	// The active-version index the bundle resolver relies on: a second
	// active control with a bundle id already in use is refused.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'dup', 'AC', 'manual_attested', $3)`, uuid.New(), e.tenant, bundleB); err == nil {
		t.Fatal("a second active control with the same bundle id was accepted; bundle refs could be ambiguous")
	}

	csv := fmt.Sprintf("Risk ID,Title,Category,Treatment,Linked Controls\n"+
		"R-ONLY-A,Mitigated by A only,operational,mitigate,%[1]s\n"+
		"R-A-AND-B,Mitigated by A and B,operational,mitigate,\"%[1]s; %[2]s\"\n", ctlA, bundleB)
	status, raw := upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("upload = %d %s", status, raw)
	}
	b := decode[riskimport.Batch](t, raw)
	onlyA, aAndB := stagedByRef(t, b, "R-ONLY-A"), stagedByRef(t, b, "R-A-AND-B")
	if onlyA.Treatment != "mitigate" || !slices.Equal(onlyA.LinkedControlIDs, []string{ctlA.String()}) {
		t.Fatalf("R-ONLY-A staged as %+v", onlyA)
	}

	// A is superseded after upload.
	successor := supersedeControl(t, admin, e.tenant, ctlA, bundleA)

	// The row whose only link is now superseded is refused by the create
	// path and stays pending; nothing is linked to the old version.
	status, raw = approveRow(t, e, e.apprJWT, b.ID, onlyA.ID)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("approve row linked only to a superseded control = %d %s, want 422", status, raw)
	}
	var rowStatus string
	if err := admin.QueryRow(context.Background(),
		`SELECT status FROM risk_import_rows WHERE id = $1`, onlyA.ID).Scan(&rowStatus); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if rowStatus != riskimport.RowStatusPending {
		t.Errorf("refused row status = %s, want pending", rowStatus)
	}

	// The row with an active link as well keeps mitigate, linked to B only.
	status, raw = approveRow(t, e, e.apprJWT, b.ID, aAndB.ID)
	if status != http.StatusOK {
		t.Fatalf("approve row = %d %s", status, raw)
	}
	got := readImportedRisk(t, e, decode[riskimport.RowDecision](t, raw).RiskID)
	if got.Treatment != "mitigate" || !slices.Equal(got.Controls, []string{ctlB.String()}) {
		t.Errorf("R-A-AND-B created as %+v, want mitigate linked to %s only", got, ctlB)
	}

	// At upload, the superseded version's id no longer resolves, and the
	// bundle id resolves to the successor.
	csv = fmt.Sprintf("Risk ID,Title,Category,Treatment,Linked Controls\n"+
		"R-OLD,Old version,operational,mitigate,%s\n"+
		"R-NEW,By bundle,operational,mitigate,%s\n", ctlA, bundleA)
	status, raw = upload(t, e, e.grcJWT, "register.csv", []byte(csv))
	if status != http.StatusCreated {
		t.Fatalf("second upload = %d %s", status, raw)
	}
	b = decode[riskimport.Batch](t, raw)
	if r := stagedByRef(t, b, "R-OLD"); r.Treatment != "untreated" ||
		!slices.Contains(r.Flags, riskimport.FlagTreatmentDowngraded) ||
		!slices.Equal(r.UnresolvedControls, []string{ctlA.String()}) {
		t.Errorf("R-OLD staged as %+v", r)
	}
	if r := stagedByRef(t, b, "R-NEW"); r.Treatment != "mitigate" ||
		!slices.Equal(r.LinkedControlIDs, []string{successor.String()}) {
		t.Errorf("R-NEW staged as %+v", r)
	}
}
