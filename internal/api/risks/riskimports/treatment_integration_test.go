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
