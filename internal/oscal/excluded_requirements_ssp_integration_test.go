//go:build integration

// Integration tests for #1792: the audit-period SSP marks requirements
// excluded in the framework-scope revision in force at the freeze as
// `not-applicable`.
//
// Timeline seeded through the real framework-scope workflow (create, decide,
// submit, approve, activate) on the app pool (RLS enforced):
//
//	rev A  effective frozen_at-1h   excludes A.8.25
//	--- audit period frozen_at ---
//	rev B  effective frozen_at+1h   excludes A.8.25 and A.5.1
//
// A.8.25 was excluded at the freeze and must reach the SSP; A.5.1 was
// excluded only after it and must not.
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...

package oscal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

const (
	excludedAtFreezeJustification = "No in-house software development at the freeze."
	excludedAfterFreezeJustif     = "Policies moved out of scope after the freeze."
)

// exclusionFixture is a frozen period whose framework version has one
// requirement excluded at the freeze and one excluded only after it.
type exclusionFixture struct {
	tenant    string
	periodID  uuid.UUID
	namespace string // framework slug + "-" + version
}

func seedExclusionTimeline(t *testing.T, admin, app *pgxpool.Pool) exclusionFixture {
	t.Helper()
	ctx := context.Background()
	tenant := freshTenant(t, admin)
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM framework_scope_applicability WHERE tenant_id = $1`,
			`DELETE FROM framework_scope_workflow_log WHERE tenant_id = $1`,
			`DELETE FROM framework_scopes WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})

	fvID := seedFrameworkVersion(t, admin)
	var slug, version string
	if err := admin.QueryRow(ctx, `
		SELECT f.slug, fv.version FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id WHERE fv.id = $1`, fvID,
	).Scan(&slug, &version); err != nil {
		t.Fatalf("read framework label: %v", err)
	}
	req := map[string]uuid.UUID{}
	for _, code := range []string{"A.5.1", "A.8.25", "A.8.28"} {
		id := uuid.New()
		if _, err := admin.Exec(ctx, `
			INSERT INTO framework_requirements (id, framework_version_id, code, title, body)
			VALUES ($1, $2, $3, $4, '')`, id, fvID, code, "requirement "+code); err != nil {
			t.Fatalf("seed requirement %s: %v", code, err)
		}
		req[code] = id
	}

	// The control keeps the SSP non-empty; the period is frozen 24h ago.
	_ = seedControl(t, admin, tenant, "IAC")
	periodID, frozenAt := seedPeriod(t, admin, tenant, fvID, true)

	store := frameworkscope.NewStore(app)
	tctx := dbtest.WithTenantCtx(t, tenant)
	revision := func(name string, effective time.Time, excluded map[string]string) {
		t.Helper()
		sc, err := store.Create(tctx, frameworkscope.CreateRequest{
			FrameworkVersionID: fvID,
			Name:               name,
			Predicate:          []byte(`{"op":"true"}`),
			Actor:              "tester",
		})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		for code, why := range excluded {
			if _, _, _, err := store.SetDecision(tctx, frameworkscope.SetDecisionRequest{
				ScopeID: sc.ID, RequirementID: req[code],
				Decision: frameworkscope.DecisionExcluded, Justification: why, Actor: "tester",
			}); err != nil {
				t.Fatalf("exclude %s on %s: %v", code, name, err)
			}
		}
		if _, _, _, err := store.SetDecision(tctx, frameworkscope.SetDecisionRequest{
			ScopeID: sc.ID, RequirementID: req["A.8.28"],
			Decision: frameworkscope.DecisionApplicable, Actor: "tester",
		}); err != nil {
			t.Fatalf("applicable A.8.28 on %s: %v", name, err)
		}
		if _, err := store.Submit(tctx, sc.ID, "tester"); err != nil {
			t.Fatalf("submit %s: %v", name, err)
		}
		if _, err := store.Approve(tctx, frameworkscope.ApproveRequest{
			ID: sc.ID, ApproverUserID: uuid.NewString(), Actor: "approver",
		}); err != nil {
			t.Fatalf("approve %s: %v", name, err)
		}
		if _, err := store.Activate(tctx, sc.ID, "tester", effective); err != nil {
			t.Fatalf("activate %s: %v", name, err)
		}
	}
	revision("ISMS rev A", frozenAt.Add(-time.Hour), map[string]string{
		"A.8.25": excludedAtFreezeJustification,
	})
	revision("ISMS rev B", frozenAt.Add(time.Hour), map[string]string{
		"A.8.25": excludedAtFreezeJustification,
		"A.5.1":  excludedAfterFreezeJustif,
	})

	return exclusionFixture{tenant: tenant, periodID: periodID, namespace: slug + "-" + version}
}

// The DB read path, no Python needed: the SSP input carries the requirement
// excluded at the freeze and not the one excluded after it.
func TestSSP_ExcludedRequirements_FrozenRevisionOnly(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	f := seedExclusionTimeline(t, admin, app)

	var in struct {
		ExcludedRequirements []struct {
			Framework        string `json:"framework"`
			RequirementCode  string `json:"requirementCode"`
			Justification    string `json:"justification"`
			FrameworkScopeID string `json:"frameworkScopeId"`
		} `json:"excludedRequirements"`
		ControlImplementations []json.RawMessage `json:"controlImplementations"`
	}
	if err := json.Unmarshal(exportSSPJSONFake(t, app, f.tenant, f.periodID), &in); err != nil {
		t.Fatalf("parse ssp input: %v", err)
	}
	if len(in.ExcludedRequirements) != 1 {
		t.Fatalf("excluded requirements = %+v, want exactly A.8.25", in.ExcludedRequirements)
	}
	got := in.ExcludedRequirements[0]
	if got.RequirementCode != "A.8.25" || got.Framework != f.namespace ||
		got.Justification != excludedAtFreezeJustification || got.FrameworkScopeID == "" {
		t.Fatalf("excluded requirement = %+v", got)
	}
	if len(in.ControlImplementations) != 1 {
		t.Fatalf("control implementations = %d, want 1 (unchanged)", len(in.ControlImplementations))
	}
}

// The real bridge: the SSP carries the namespaced not-applicable entry with
// the justification as remarks and the framework prop, and nothing for the
// requirement excluded after the freeze.
func TestSSP_ExcludedRequirements_RealBridgeNotApplicable(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	f := seedExclusionTimeline(t, admin, app)

	addr, stop := startBridge(t)
	defer stop()
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	defer func() { _ = bridge.Close() }()
	signer, _ := oscal.NewEphemeralSigner()
	bundle, err := oscal.NewExporter(app, bridge, signer).Export(dbtest.WithTenantCtx(t, f.tenant), oscal.ExportInput{
		AuditPeriodID: f.periodID, OrganizationName: "Acme", SystemName: "Acme ISMS", RequestedBy: "tester",
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	var sspJSON []byte
	for _, m := range bundle.Members {
		if m.Filename == "ssp.json" {
			sspJSON = m.JSON
		}
	}
	if sspJSON == nil {
		t.Fatal("bundle has no ssp.json member")
	}
	if strings.Contains(string(sspJSON), excludedAfterFreezeJustif) {
		t.Fatal("SSP carries a requirement excluded only after the freeze")
	}

	var doc struct {
		SSP struct {
			CI struct {
				IRs []struct {
					ControlID string `json:"control-id"`
					Remarks   string `json:"remarks"`
					Props     []struct {
						Name, Value, NS string
					} `json:"props"`
				} `json:"implemented-requirements"`
			} `json:"control-implementation"`
		} `json:"system-security-plan"`
	}
	if err := json.Unmarshal(sspJSON, &doc); err != nil {
		t.Fatalf("parse ssp.json: %v", err)
	}
	want := fmt.Sprintf("%s_A.8.25", f.namespace)
	var notApplicable int
	for _, ir := range doc.SSP.CI.IRs {
		props := map[string]string{}
		for _, p := range ir.Props {
			props[p.Name] = p.Value
		}
		if props["implementation-status"] != "not-applicable" {
			continue
		}
		notApplicable++
		if ir.ControlID != want || ir.Remarks != excludedAtFreezeJustification || props["framework"] != f.namespace {
			t.Fatalf("not-applicable entry = %+v, want control-id %s", ir, want)
		}
	}
	if notApplicable != 1 {
		t.Fatalf("not-applicable entries = %d, want 1", notApplicable)
	}
	if len(doc.SSP.CI.IRs) != 2 {
		t.Fatalf("implemented-requirements = %d, want 2 (one control, one exclusion)", len(doc.SSP.CI.IRs))
	}
}
