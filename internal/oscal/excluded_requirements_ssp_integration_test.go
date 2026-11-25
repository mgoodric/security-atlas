//go:build integration

// Integration tests for #1792: the audit-period SSP marks requirements
// excluded in the framework-scope revision in force at the freeze as
// `not-applicable`.
//
// Timeline seeded through the real framework-scope workflow (create, decide,
// submit, approve, activate) on the app pool (RLS enforced):
//
//	rev A  activated, effective frozen_at-1h   excludes A.8.25
//	--- audit period frozen_at (rev A's activated_at) ---
//	rev B  activated, effective frozen_at+1h   excludes A.8.25 and A.5.1
//
// A.8.25 was excluded at the freeze and must reach the SSP; A.5.1 was
// excluded only after it and must not. Rev A is backdated but activated
// before the freeze, so it is in force at the freeze (#2078).
//
// Run with: go test -tags=integration -p 1 ./internal/oscal/...

package oscal_test

import (
	"context"
	"encoding/json"
	"errors"
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
	frozenAt  time.Time
	store     *frameworkscope.Store
	ctx       context.Context
	// approved takes a new revision with these exclusions to `approved`.
	approved func(name string, excluded map[string]string) uuid.UUID
}

// seedExclusionTimeline builds the timeline above. beforeFreeze, if given,
// runs after rev A is activated and before the period is frozen at rev A's
// activated_at; the fixture it receives already carries that frozenAt.
func seedExclusionTimeline(t *testing.T, admin, app *pgxpool.Pool, beforeFreeze ...func(exclusionFixture)) exclusionFixture {
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

	// The control keeps the SSP non-empty. The period is frozen below, once
	// rev A exists.
	_ = seedControl(t, admin, tenant, "IAC")
	periodID, _ := seedPeriod(t, admin, tenant, fvID, false)

	store := frameworkscope.NewStore(app)
	tctx := dbtest.WithTenantCtx(t, tenant)
	approved := func(name string, excluded map[string]string) uuid.UUID {
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
		if _, ok := excluded["A.8.28"]; !ok {
			if _, _, _, err := store.SetDecision(tctx, frameworkscope.SetDecisionRequest{
				ScopeID: sc.ID, RequirementID: req["A.8.28"],
				Decision: frameworkscope.DecisionApplicable, Actor: "tester",
			}); err != nil {
				t.Fatalf("applicable A.8.28 on %s: %v", name, err)
			}
		}
		if _, err := store.Submit(tctx, sc.ID, "tester", "tester"); err != nil {
			t.Fatalf("submit %s: %v", name, err)
		}
		if _, err := store.Approve(tctx, frameworkscope.ApproveRequest{
			ID: sc.ID, ApproverUserID: uuid.NewString(), Actor: "approver",
		}); err != nil {
			t.Fatalf("approve %s: %v", name, err)
		}
		return sc.ID
	}
	activate := func(id uuid.UUID, effective time.Time) {
		t.Helper()
		if _, err := store.Activate(tctx, id, "tester", effective); err != nil {
			t.Fatalf("activate %s: %v", id, err)
		}
	}

	// Rev A: effective a day back, activated now while the period is open.
	revA := approved("ISMS rev A", map[string]string{
		"A.8.25": excludedAtFreezeJustification,
	})
	activate(revA, time.Now().UTC().Add(-25*time.Hour))

	// Freeze the period at rev A's activation, so rev A existed at the freeze.
	var frozenAt time.Time
	if err := admin.QueryRow(ctx,
		`SELECT activated_at FROM framework_scopes WHERE id = $1`, revA,
	).Scan(&frozenAt); err != nil {
		t.Fatalf("read rev A activated_at: %v", err)
	}
	f := exclusionFixture{
		tenant: tenant, periodID: periodID, namespace: slug + "-" + version,
		frozenAt: frozenAt, store: store, ctx: tctx, approved: approved,
	}
	for _, hook := range beforeFreeze {
		hook(f)
	}
	if err := dbtest.FreezeRaw(ctx, admin, tenant, periodID,
		`UPDATE audit_periods SET status='frozen', frozen_at=$2, frozen_by='tester',
		        frozen_hash=decode('00','hex') WHERE id=$1`,
		periodID, frozenAt); err != nil {
		t.Fatalf("freeze audit period: %v", err)
	}

	activate(approved("ISMS rev B", map[string]string{
		"A.8.25": excludedAtFreezeJustification,
		"A.5.1":  excludedAfterFreezeJustif,
	}), frozenAt.Add(time.Hour))

	return f
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

// sspExcluded maps each requirement the frozen-period SSP input carries as
// excluded to its justification.
func sspExcluded(t *testing.T, app *pgxpool.Pool, f exclusionFixture) map[string]string {
	t.Helper()
	var in struct {
		ExcludedRequirements []struct {
			RequirementCode string `json:"requirementCode"`
			Justification   string `json:"justification"`
		} `json:"excludedRequirements"`
	}
	if err := json.Unmarshal(exportSSPJSONFake(t, app, f.tenant, f.periodID), &in); err != nil {
		t.Fatalf("parse ssp input: %v", err)
	}
	out := map[string]string{}
	for _, r := range in.ExcludedRequirements {
		out[r.RequirementCode] = r.Justification
	}
	return out
}

// #2078: once the period is frozen, an activation whose effective_from is
// earlier than the freeze is refused, and the frozen export is unchanged.
func TestSSP_ExcludedRequirements_BackdatedActivationAfterFreezeRefused(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	f := seedExclusionTimeline(t, admin, app)
	before := sspExcluded(t, app, f)

	revC := f.approved("ISMS rev C", map[string]string{"A.8.28": "Backdated across the freeze."})
	_, err := f.store.Activate(f.ctx, revC, "tester", f.frozenAt.Add(-time.Hour))
	if !errors.Is(err, frameworkscope.ErrEffectiveBeforeFreeze) {
		t.Fatalf("Activate with effective_from = freeze-1h: err = %v, want ErrEffectiveBeforeFreeze", err)
	}

	if after := sspExcluded(t, app, f); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("frozen export changed: before %v, after %v", before, after)
	}
}

// #2078: a later activation (effective after the freeze, as Activate now
// requires) leaves a re-export of the frozen period unchanged.
func TestSSP_ExcludedRequirements_ReExportUnchangedAfterLaterActivation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	f := seedExclusionTimeline(t, admin, app)
	before := sspExcluded(t, app, f)
	if len(before) != 1 || before["A.8.25"] != excludedAtFreezeJustification {
		t.Fatalf("export before = %v, want only A.8.25", before)
	}

	revD := f.approved("ISMS rev D", map[string]string{"A.8.28": "Activated after the freeze."})
	if _, err := f.store.Activate(f.ctx, revD, "tester", f.frozenAt.Add(time.Second)); err != nil {
		t.Fatalf("Activate with effective_from = freeze+1s: %v", err)
	}

	if after := sspExcluded(t, app, f); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("frozen export changed after a later activation: before %v, after %v", before, after)
	}
}

// #2078: frozen_at is the instant the freeze names, which can precede an
// activation that committed before the freeze did. Rev D is activated while
// the period is still open, effective at the coming frozen_at and activated
// after it. The period is then frozen at rev A's activated_at. Rev D did not
// exist at that instant, so the SSP reports rev A's exclusions; selecting by
// effective_from alone, rev D would win.
func TestSSP_ExcludedRequirements_RevisionActivatedAfterFrozenAtNotSeen(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	var revD uuid.UUID
	f := seedExclusionTimeline(t, admin, app, func(f exclusionFixture) {
		revD = f.approved("ISMS rev D", map[string]string{"A.8.28": "Activated after frozen_at."})
		if _, err := f.store.Activate(f.ctx, revD, "tester", f.frozenAt); err != nil {
			t.Fatalf("Activate rev D while open: %v", err)
		}
	})
	var activatedAt time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT activated_at FROM framework_scopes WHERE id = $1`, revD).Scan(&activatedAt); err != nil {
		t.Fatalf("read rev D activated_at: %v", err)
	}
	if !activatedAt.After(f.frozenAt) {
		t.Fatalf("precondition: rev D activated_at %v not after frozen_at %v", activatedAt, f.frozenAt)
	}

	if got := sspExcluded(t, app, f); len(got) != 1 || got["A.8.25"] != excludedAtFreezeJustification {
		t.Fatalf("frozen export = %v, want only rev A's A.8.25", got)
	}
}
