//go:build integration

// #2171 — every optional module can be switched off, and every route
// belongs to a module. Each request goes through the production router
// (HTTPHandlerForTests: JWT, tenancy, the module gate, OPA) on real
// Postgres under RLS, with switches flipped through
// PATCH /v1/admin/features as an admin would.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/oscal"
	"github.com/mgoodric/security-atlas/internal/scope"
)

type modulesEnv struct {
	*moduleGateFixture
	tenant       string
	migrate, app *pgxpool.Pool
}

// newModulesEnv builds the production router for a fresh tenant. A
// non-nil exporter is attached before the router is built, so the
// audit-period OSCAL export routes are mounted.
func newModulesEnv(t *testing.T, exporter func(app *pgxpool.Pool) *oscal.Exporter) *modulesEnv {
	t.Helper()
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate,
		"control_evaluations", "evidence_records", "audit_period_audit_log",
		"audit_periods", "policies", "scope_cells", "controls",
		"feature_flag_audit_log", "feature_flags", "decision_audit_log")
	tid := uuid.MustParse(tenant)

	adminClaims := testjwt.AdminFor(tid)
	// A non-admin. The harness token carries an empty role claim, which
	// derives nothing since #2401, so the role is granted explicitly here.
	memberClaims := testjwt.ViewerFor(tid)
	engine, err := authz.NewEngine(context.Background(), noteSubjectRoles{
		adminClaims.Subject:  {authz.RoleAdmin},
		memberClaims.Subject: {authz.RoleGRCEngineer},
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	if exporter != nil {
		srv.AttachOscalExporter(exporter(app))
	}
	f := &moduleGateFixture{
		admin:  srv.IssueTestJWT(t, adminClaims),
		member: srv.IssueTestJWT(t, memberClaims),
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	f.url = ts.URL
	return &modulesEnv{moduleGateFixture: f, tenant: tenant, migrate: migrate, app: app}
}

// isMarker reports whether a response is the switched-off marker for key.
func isMarker(r moduleGateResponse, key string) bool {
	return r.status == http.StatusNotFound && r.marker == key && r.body == "{\"error\":\"feature disabled\"}\n"
}

// For each optional module, a representative route and a nested route
// answer the switched-off marker when the module is off, and answer as
// they did before once it is back on.
func TestModules_EachOptionalModuleSwitchesOff(t *testing.T) {
	e := newModulesEnv(t, nil)
	id := uuid.NewString()
	cases := []struct {
		module string
		key    string
		routes []string
	}{
		{"risk", "risk.enabled", []string{"/v1/risks", "/v1/controls/" + id + "/action-plans", "/v1/risks/" + id + "/children"}},
		{"third_party_risk", "vendor.enabled", []string{"/v1/vendors", "/v1/vendors/" + id + "/reviews"}},
		{"policies", "policy.enabled", []string{"/v1/policies", "/v1/controls/" + id + "/policies", "/v1/me/acknowledgments"}},
		{"questionnaires", "questionnaires.enabled", []string{"/v1/questionnaires", "/v1/questionnaires/" + id}},
		{"personnel_security", "personnel.enabled", []string{"/v1/personnel-security/checklists", "/v1/personnel-security/checklists/" + id}},
		{"exceptions", "exceptions.enabled", []string{"/v1/exceptions", "/v1/exceptions/" + id}},
		{"board_reporting", "board.reporting", []string{"/v1/board-packs", "/v1/board-packs/" + id}},
	}
	// Board reporting is off by default; switch it on to take the "as
	// today" baseline every other module already has.
	e.setSwitch(t, "board.reporting", true)

	for _, c := range cases {
		t.Run(c.module, func(t *testing.T) {
			baseline := map[string]int{}
			for _, p := range c.routes {
				got := e.do(t, http.MethodGet, p, e.admin, nil)
				if got.marker != "" {
					t.Fatalf("on: GET %s answered the marker %q", p, got.marker)
				}
				baseline[p] = got.status
			}
			e.setSwitch(t, c.key, false)
			for _, bearer := range []string{e.admin, e.member} {
				for _, p := range c.routes {
					if got := e.do(t, http.MethodGet, p, bearer, nil); !isMarker(got, c.key) {
						t.Fatalf("off: GET %s: status %d marker %q body %q; want the %s marker", p, got.status, got.marker, got.body, c.key)
					}
				}
			}
			e.setSwitch(t, c.key, true)
			for _, p := range c.routes {
				got := e.do(t, http.MethodGet, p, e.admin, nil)
				if got.marker != "" || got.status != baseline[p] {
					t.Fatalf("on again: GET %s: status %d marker %q; want %d as before, no marker", p, got.status, got.marker, baseline[p])
				}
			}
		})
	}

	// Vendor claims are a feature of Third-party risk: off with its module
	// even while its own switch is on.
	e.setSwitch(t, "vendor.claims", true)
	if got := e.do(t, http.MethodGet, "/v1/oscal/component-definitions", e.admin, nil); got.status != http.StatusOK {
		t.Fatalf("vendor claims on: status %d body %s", got.status, got.body)
	}
	e.setSwitch(t, "vendor.enabled", false)
	if got := e.do(t, http.MethodGet, "/v1/oscal/component-definitions", e.admin, nil); !isMarker(got, "vendor.enabled") {
		t.Fatalf("third-party risk off: vendor claims status %d marker %q; want the vendor.enabled marker", got.status, got.marker)
	}
}

// flagItem returns one entry of GET /v1/admin/features.
func (e *modulesEnv) flagItem(t *testing.T, key string) map[string]any {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/admin/features", e.admin, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/admin/features: status %d body %s", got.status, got.body)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode features: %v", err)
	}
	for _, it := range body.Items {
		if it["key"] == key {
			return it
		}
	}
	t.Fatalf("flag %s not listed", key)
	return nil
}

// With Risk off and risk hierarchy on, the hierarchy routes answer the
// marker; switching Risk back on restores them without touching the
// hierarchy switch.
func TestModules_RiskOffHidesHierarchyAndKeepsItsSwitch(t *testing.T) {
	e := newModulesEnv(t, nil)
	e.setSwitch(t, "risk.hierarchy", true)
	before := e.flagItem(t, "risk.hierarchy")

	hierarchy := []string{"/v1/org_units", "/v1/risks/" + uuid.NewString() + "/parents", "/v1/aggregation-rules"}
	baseline := map[string]int{}
	for _, p := range hierarchy {
		baseline[p] = e.do(t, http.MethodGet, p, e.admin, nil).status
	}

	e.setSwitch(t, "risk.enabled", false)
	for _, p := range hierarchy {
		if got := e.do(t, http.MethodGet, p, e.admin, nil); !isMarker(got, "risk.enabled") {
			t.Fatalf("risk off: GET %s: status %d marker %q; want the risk.enabled marker", p, got.status, got.marker)
		}
	}
	if mid := e.flagItem(t, "risk.hierarchy"); mid["enabled"] != true {
		t.Fatalf("risk off: risk.hierarchy reads %v; its switch must keep its value", mid["enabled"])
	}

	e.setSwitch(t, "risk.enabled", true)
	for _, p := range hierarchy {
		if got := e.do(t, http.MethodGet, p, e.admin, nil); got.marker != "" || got.status != baseline[p] {
			t.Fatalf("risk on again: GET %s: status %d marker %q; want %d", p, got.status, got.marker, baseline[p])
		}
	}
	after := e.flagItem(t, "risk.hierarchy")
	for _, k := range []string{"enabled", "has_override", "last_changed_by", "last_changed_at"} {
		if before[k] != after[k] {
			t.Fatalf("risk.hierarchy %s changed across the Risk off/on cycle: %v -> %v", k, before[k], after[k])
		}
	}
}

func seedModuleControl(t *testing.T, migrate *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type,
		                      bundle_id, freshness_class, evidence_queries, applicability_expr)
		VALUES ($1, $2, '#2171 control', 'HRS', 'automated', $3, 'monthly', '[]'::jsonb, 'true')`,
		id, tenant, "bundle-2171-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// seedModuleEvidence writes one ledger record of the kind an optional
// module writes (policy acknowledgment, personnel-security workflow).
func seedModuleEvidence(t *testing.T, migrate *pgxpool.Pool, tenant string, ctrl uuid.UUID, kind string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(), `
		INSERT INTO evidence_records (id, tenant_id, control_id, observed_at, ingested_at,
		                              provenance, result, payload, hash, control_ref)
		VALUES ($1, $2, $3, $4, now(), jsonb_build_object('evidence_kind', $5::text), 'pass', '{}'::jsonb, $6, $7)`,
		id, tenant, ctrl, time.Now().UTC().Add(-time.Hour), kind, "hash-2171-"+id.String(), ctrl.String()); err != nil {
		t.Fatalf("seed evidence %s: %v", kind, err)
	}
	return id
}

// stateResults returns each state's result from GET /v1/controls/{id}/state.
func (e *modulesEnv) stateResults(t *testing.T, ctrl uuid.UUID) []string {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/controls/"+ctrl.String()+"/state", e.admin, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET state: status %d body %s", got.status, got.body)
	}
	var body struct {
		States []struct {
			Result string `json:"result"`
		} `json:"states"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	out := make([]string, 0, len(body.States))
	for _, s := range body.States {
		out = append(out, s.Result)
	}
	return out
}

// With Policies and Personnel security off, the evidence they wrote is
// still listed under Evidence and still counts toward control results.
func TestModules_EvidenceFromSwitchedOffModulesStillCounts(t *testing.T) {
	e := newModulesEnv(t, nil)
	ctrl := seedModuleControl(t, e.migrate, e.tenant)
	ackID := seedModuleEvidence(t, e.migrate, e.tenant, ctrl, "policy.acknowledgment.v1")
	psID := seedModuleEvidence(t, e.migrate, e.tenant, ctrl, "personnel_security.workflow.v1")

	engine := eval.NewEngine(eval.NewStore(e.app), scope.NewStore(e.app))
	evaluate := func() {
		t.Helper()
		if _, err := engine.EvaluateControl(dbtest.WithTenantCtx(t, e.tenant), ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
			t.Fatalf("EvaluateControl: %v", err)
		}
	}
	evaluate()
	listPath := "/v1/evidence?control_id=" + ctrl.String()
	listOn := e.do(t, http.MethodGet, listPath, e.admin, nil)
	if listOn.status != http.StatusOK || !strings.Contains(listOn.body, ackID.String()) || !strings.Contains(listOn.body, psID.String()) {
		t.Fatalf("modules on: evidence list status %d does not hold both records: %s", listOn.status, listOn.body)
	}
	stateOn := e.stateResults(t, ctrl)
	if len(stateOn) == 0 || stateOn[0] != "pass" {
		t.Fatalf("modules on: control results %v; want pass from the two records", stateOn)
	}

	e.setSwitch(t, "policy.enabled", false)
	e.setSwitch(t, "personnel.enabled", false)
	// A fresh evaluation while the modules are off reads the same ledger.
	evaluate()
	listOff := e.do(t, http.MethodGet, listPath, e.admin, nil)
	if listOff.status != http.StatusOK || listOff.body != listOn.body {
		t.Fatalf("modules off: evidence list changed (status %d):\non =%s\noff=%s", listOff.status, listOn.body, listOff.body)
	}
	if stateOff := e.stateResults(t, ctrl); len(stateOff) != len(stateOn) || stateOff[0] != stateOn[0] {
		t.Fatalf("modules off: control results %v; want %v", stateOff, stateOn)
	}
}

// capturingBridge is an oscal.BridgeClient that serializes each input as
// its protojson, so the bundle the API returns carries exactly what the
// exporter assembled from the database. last_modified (the export's own
// clock) is cleared so two exports of the same period compare equal.
type capturingBridge struct{ mu sync.Mutex }

func (b *capturingBridge) marshal(m proto.Message) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return protojson.Marshal(m)
}

func (b *capturingBridge) SerializeSSP(_ context.Context, in *oscalv1.SspInput) ([]byte, error) {
	c := proto.Clone(in).(*oscalv1.SspInput)
	if c.Metadata != nil {
		c.Metadata.LastModified = ""
	}
	return b.marshal(c)
}

func (b *capturingBridge) SerializeAssessment(_ context.Context, in *oscalv1.AssessmentInput) ([]byte, []byte, error) {
	c := proto.Clone(in).(*oscalv1.AssessmentInput)
	if c.Metadata != nil {
		c.Metadata.LastModified = ""
	}
	out, err := b.marshal(c)
	return out, out, err
}

func (b *capturingBridge) SerializePOAM(_ context.Context, in *oscalv1.PoamInput) ([]byte, error) {
	c := proto.Clone(in).(*oscalv1.PoamInput)
	if c.Metadata != nil {
		c.Metadata.LastModified = ""
	}
	return b.marshal(c)
}

func (b *capturingBridge) RoundTripValidate(context.Context, string, []byte) (bool, []string, error) {
	return true, nil, nil
}

func (b *capturingBridge) ImportCatalog(context.Context, []byte, string) (*oscalv1.ImportCatalogResponse, error) {
	return nil, nil
}

func (b *capturingBridge) ImportProfile(context.Context, []byte, [][]byte, [][]byte, string) (*oscalv1.ImportProfileResponse, error) {
	return nil, nil
}

func (b *capturingBridge) ImportComponentDefinition(context.Context, []byte, string) (*oscalv1.ImportComponentDefinitionResponse, error) {
	return nil, nil
}

func (b *capturingBridge) Close() error { return nil }

// oscalMembers posts an export and returns the SSP and POA&M members'
// OSCAL bodies by model type.
func (e *modulesEnv) oscalMembers(t *testing.T, period uuid.UUID) map[string]string {
	t.Helper()
	got := e.do(t, http.MethodPost, "/v1/audit-periods/"+period.String()+"/oscal-export", e.admin,
		map[string]any{"organization_name": "Acme", "system_name": "Acme GRC"})
	if got.status != http.StatusOK {
		t.Fatalf("POST oscal-export: status %d body %s", got.status, got.body)
	}
	var body struct {
		Members []struct {
			ModelType string          `json:"model_type"`
			OSCAL     json.RawMessage `json:"oscal"`
		} `json:"members"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	out := map[string]string{}
	for _, m := range body.Members {
		out[m.ModelType] = string(m.OSCAL)
	}
	return out
}

// With any optional module switched off, the OSCAL SSP and POA&M output
// for an audit period is unchanged.
func TestModules_OSCALOutputIgnoresSwitches(t *testing.T) {
	e := newModulesEnv(t, func(app *pgxpool.Pool) *oscal.Exporter {
		signer, err := oscal.NewEphemeralSigner()
		if err != nil {
			t.Fatalf("NewEphemeralSigner: %v", err)
		}
		return oscal.NewExporter(app, &capturingBridge{}, signer)
	})
	ctx := context.Background()
	fwID, versionID, period := uuid.New(), uuid.New(), uuid.New()
	t.Cleanup(func() {
		// Registered before the inserts, so a failed seed still cleans up.
		// Runs before SeedTenant's cleanup (LIFO), so the period goes
		// first, then the global framework rows, which outlive the tenant
		// otherwise (#3041).
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`, e.tenant},
			{`DELETE FROM audit_periods WHERE id = $1`, period},
			{`DELETE FROM framework_versions WHERE id = $1`, versionID},
			{`DELETE FROM frameworks WHERE id = $1`, fwID},
		} {
			if _, err := e.migrate.Exec(ctx, q.sql, q.arg); err != nil {
				t.Errorf("cleanup %q: %v", q.sql, err)
			}
		}
	})
	if _, err := e.migrate.Exec(ctx, `INSERT INTO frameworks (id, tenant_id, name, slug, issuer) VALUES ($1, NULL, '#2171 framework', $2, 'test')`,
		fwID, "fw-2171-"+fwID.String()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := e.migrate.Exec(ctx, `INSERT INTO framework_versions (id, tenant_id, framework_id, version) VALUES ($1, NULL, $2, '1.0')`,
		versionID, fwID); err != nil {
		t.Fatalf("seed framework version: %v", err)
	}
	frozenAt := time.Now().UTC().Add(-24 * time.Hour)
	if err := dbtest.FreezeRaw(ctx, e.migrate, e.tenant, period, `
		INSERT INTO audit_periods (id, tenant_id, name, framework_version_id, period_start, period_end, status, created_by,
		                           frozen_at, frozen_by, frozen_hash)
		VALUES ($1, $2, '#2171 period', $3, $4, $5, 'frozen', 'tester', $6, 'tester', decode('00','hex'))`,
		period, e.tenant, versionID, frozenAt.Add(-90*24*time.Hour), frozenAt, frozenAt); err != nil {
		t.Fatalf("seed frozen period: %v", err)
	}
	ctrl := seedModuleControl(t, e.migrate, e.tenant)
	if _, err := e.migrate.Exec(ctx, `
		INSERT INTO policies (id, tenant_id, title, version, body_md, status, owner_role, approver_role, created_by, effective_date)
		VALUES ($1, $2, 'Access Control Policy', '2.0', '# body', 'published', 'grc_engineer', 'ciso', 'tester', CURRENT_DATE)`,
		uuid.New(), e.tenant); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if _, err := e.migrate.Exec(ctx, `
		INSERT INTO control_evaluations (id, tenant_id, control_id, scope_cell_id, eval_run_id, evaluated_at, result,
		                                 freshness_status, evidence_count_in_window, trigger)
		VALUES ($1, $2, $3, NULL, $4, $5, 'fail', 'stale', 0, 'manual')`,
		uuid.New(), e.tenant, ctrl, uuid.New(), frozenAt.Add(-time.Hour)); err != nil {
		t.Fatalf("seed failing evaluation: %v", err)
	}

	// Board reporting on too, so "every module on" is the baseline.
	e.setSwitch(t, "board.reporting", true)
	baseline := e.oscalMembers(t, period)
	for _, model := range []string{"system-security-plan", "plan-of-action-and-milestones"} {
		if baseline[model] == "" {
			t.Fatalf("baseline export has no %s member (members: %v)", model, keys(baseline))
		}
	}
	if !strings.Contains(baseline["system-security-plan"], "Access Control Policy") {
		t.Fatalf("baseline SSP does not carry the seeded policy: %s", baseline["system-security-plan"])
	}

	for _, m := range featureflag.OptionalModules() {
		e.setSwitch(t, m.FlagKey, false)
		got := e.oscalMembers(t, period)
		for _, model := range []string{"system-security-plan", "plan-of-action-and-milestones"} {
			if got[model] != baseline[model] {
				t.Fatalf("%s off: %s changed:\nbefore=%s\nafter =%s", m.Key, model, baseline[model], got[model])
			}
		}
		e.setSwitch(t, m.FlagKey, true)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Core modules have no switch an admin can turn off: none is listed as a
// module switch, the switch keys their names suggest are refused, and
// their routes never answer the marker whatever is switched off.
func TestModules_CoreModulesHaveNoSwitch(t *testing.T) {
	e := newModulesEnv(t, nil)

	got := e.do(t, http.MethodGet, "/v1/admin/features", e.admin, nil)
	var body struct {
		Items []struct {
			Key          string `json:"key"`
			ModuleSwitch bool   `json:"module_switch"`
			ModuleCore   bool   `json:"module_core"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode features: %v", err)
	}
	for _, it := range body.Items {
		if it.ModuleSwitch && it.ModuleCore {
			t.Fatalf("%s is listed as a core module's switch", it.Key)
		}
	}

	for _, m := range featureflag.Modules {
		if !m.Core {
			continue
		}
		for _, key := range []string{m.Key + ".enabled", "controls.core", "evidence.ledger"} {
			r := e.do(t, http.MethodPatch, "/v1/admin/features/"+key, e.admin, map[string]any{"enabled": false})
			if r.status == http.StatusOK {
				t.Fatalf("PATCH %s enabled=false answered 200; a core module has no switch", key)
			}
		}
	}

	// Every optional module and feature off: core routes still answer.
	for _, m := range featureflag.OptionalModules() {
		e.setSwitch(t, m.FlagKey, false)
	}
	for _, p := range []string{"/v1/controls", "/v1/evidence", "/v1/frameworks/posture", "/v1/framework-scopes",
		"/v1/audit-periods", "/v1/dashboard/export", "/v1/me", "/v1/incidents"} {
		if r := e.do(t, http.MethodGet, p, e.admin, nil); r.marker != "" {
			t.Fatalf("core GET %s answered the marker %q", p, r.marker)
		}
	}
}
