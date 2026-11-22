//go:build integration

// #2821: stock payload checks run in SQL (ADR 0028), which needs a real
// Postgres session. Two layers prove them:
//
//   - every stock bundle's tests/ cases run through bundletest.Run inside a
//     tenant transaction, as the upload gate runs them; and
//   - cc6_1, the tracer, and each later check (cc6_6 and cc4_1, #2822; both cc8_1
//     bundles, #2823; the windowed cc6_3, cc1_4, cc6_5 and cc9_1, #2824;
//     cc6_6 MFA on admin apps, #2828; cc7_1 remediation and cc1_4 training
//     tickets, #2829) are
//     evaluated by the live engine against evidence
//     records in the ledger, so the query is shown to decide the stored
//     control state and not only the fixture runner's.

package control_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/control/bundletest"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestStockSQLBundleTests_PassAgainstPostgres(t *testing.T) {
	app := dbtest.NewAppPool(t)
	soc2 := filepath.Join(repoRoot(t), "controls", "soc2")

	ran := 0
	forEachSOC2Bundle(t, func(name string, b *control.Bundle) {
		if !hasSQLQuery(b) {
			return
		}
		ran++
		ctx, err := tenancy.WithTenant(context.Background(), uuid.NewString())
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			t.Fatalf("ApplyTenant: %v", err)
		}
		rep, err := bundletest.Run(ctx, filepath.Join(soc2, name), bundletest.Options{Tx: tx})
		if err != nil {
			t.Fatalf("%s: run bundle tests: %v", name, err)
		}
		if len(rep.Cases) == 0 {
			t.Errorf("%s: no test cases ran", name)
		}
		for _, c := range rep.Cases {
			if !c.Passed {
				t.Errorf("%s / %s: expected %s, got %s (err %q)", name, c.Name, c.ExpectedState, c.ActualState, c.Err)
			}
		}
	})
	if ran == 0 {
		t.Fatal("no stock bundle declares a SQL query")
	}
}

const cc61Bundle = "soc2_cc6_1_logical_access_baseline"

// seedStockBundleControl inserts the bundle as its control.yaml states it;
// the engine reads freshness_class and evidence_queries from the row.
func seedStockBundleControl(t *testing.T, admin *pgxpool.Pool, tenant, bundle string) uuid.UUID {
	t.Helper()
	b, err := control.ParseDirectory(filepath.Join(repoRoot(t), "controls", "soc2", bundle))
	if err != nil {
		t.Fatalf("parse %s: %v", bundle, err)
	}
	m := b.Manifest
	queries, err := m.EvidenceQueriesJSON()
	if err != nil {
		t.Fatalf("%s evidence queries: %v", bundle, err)
	}
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, 'true')
	`, id, tenant, m.Title, m.ControlFamily, m.ImplementationType, m.BundleID, m.FreshnessClass, queries); err != nil {
		t.Fatalf("seed %s: %v", bundle, err)
	}
	return id
}

func seedMFAPolicy(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl uuid.UUID, observedAt time.Time, connectorResult string, payload map[string]any) {
	t.Helper()
	seedStockEvidence(t, admin, tenant, ctrl, "okta.mfa_policy.v2", observedAt, connectorResult, payload)
}

// seedStockEvidence inserts one ledger record of the given kind, carrying the
// connector's own verdict and payload.
func seedStockEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl uuid.UUID, kind string, observedAt time.Time, connectorResult string, payload map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, evidence_kind, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, $5, now(), '{}'::jsonb, $6, $7::jsonb, $8, $9)
	`, id, tenant, ctrl, kind, observedAt, connectorResult, string(body), "hash-stock-"+id.String()[:8], ctrl.String()); err != nil {
		t.Fatalf("seed %s record: %v", kind, err)
	}
}

func TestCC61_EngineEvaluatesMFAPolicyPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	observed := time.Now().UTC().Add(-2 * time.Hour)

	// An okta.mfa_policy.v2 payload as the connector builds it (#2901).
	policy := func(status string, required bool, factors []string, everyone bool) map[string]any {
		groups := []string{"00g1engineering"}
		if everyone {
			groups = []string{"00g1everyone"}
		}
		return map[string]any{
			"policy_id":           "00p1",
			"policy_name":         "Default Policy",
			"status":              status,
			"mfa_required":        required,
			"factors_allowed":     factors,
			"applies_to_groups":   groups,
			"applies_to_everyone": everyone,
		}
	}
	// connectorResult is what the Okta connector emits for each payload:
	// pass whenever an active policy requires MFA with a factor, na when it
	// is inactive. The query must ignore it.
	cases := []struct {
		name            string
		connectorResult string
		payload         map[string]any
		want            string
	}{
		{"webauthn on everyone", "pass", policy("ACTIVE", true, []string{"okta_verify", "webauthn"}, true), eval.ResultPass},
		{"only sms and push", "pass", policy("ACTIVE", true, []string{"okta_push", "okta_sms"}, true), eval.ResultFail},
		{"mfa not required", "fail", policy("ACTIVE", false, []string{"webauthn"}, true), eval.ResultFail},
		{"not applied to everyone", "pass", policy("ACTIVE", true, []string{"webauthn"}, false), eval.ResultFail},
		{"inactive", "na", policy("INACTIVE", true, []string{"webauthn"}, true), eval.ResultFail},
		{"no snapshot", "", nil, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, cc61Bundle)
			if tc.payload != nil {
				seedMFAPolicy(t, admin, tenant, ctrl, observed, tc.connectorResult, tc.payload)
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}

const cc66Bundle = "soc2_cc6_6_boundary_protection"

// #2822: cc6_6 decides on each host's latest firewall_enabled, not on the
// osquery connector's verdict (disk encryption plus screen lock). #2920: an
// absent firewall_enabled (not read) is inconclusive, and an observed off
// still fails.
func TestCC66_EngineEvaluatesHostFirewallPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	observed := time.Now().UTC().Add(-2 * time.Hour)

	type rec struct {
		connectorResult string
		host            string
		firewall        string // "on", "off", or "" for not read
	}
	payload := func(r rec) map[string]any {
		p := map[string]any{
			"host_uuid":               r.host,
			"hostname":                "host-" + r.host[:4],
			"platform":                "darwin",
			"disk_encryption_enabled": true,
			"mdm_enrolled":            true,
		}
		if r.firewall != "" {
			p["firewall_enabled"] = r.firewall == "on"
			p["firewall_source"] = "fleet_policy:macOS firewall on"
		}
		return p
	}
	const h1, h2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	// connectorResult is the osquery verdict (disk encryption plus screen
	// lock). The query must ignore it.
	cases := []struct {
		name    string
		records []rec
		want    string
	}{
		{"every host firewall on", []rec{{"fail", h1, "on"}, {"pass", h2, "on"}}, eval.ResultPass},
		{"one host firewall off", []rec{{"pass", h1, "on"}, {"pass", h2, "off"}}, eval.ResultFail},
		{"unread host is inconclusive", []rec{{"inconclusive", h1, ""}}, eval.ResultInconclusive},
		{"unread host plus one off fails", []rec{{"pass", h1, ""}, {"pass", h2, "off"}}, eval.ResultFail},
		{"no record", nil, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, cc66Bundle)
			for _, r := range tc.records {
				seedStockEvidence(t, admin, tenant, ctrl, "osquery.host_posture.v1", observed, r.connectorResult, payload(r))
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}

const cc66MFABundle = "soc2_cc6_6_mfa_external_threats"

// #2828: cc6_6 MFA on admin apps decides on each app's latest
// okta.app_assignment.v1 payload (classification, sign_on_mfa_required),
// not on the Okta connector's verdict, which is always inconclusive.
func TestCC66_EngineEvaluatesAdminAppMFAPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	observed := time.Now().UTC().Add(-2 * time.Hour)

	type rec struct {
		id    string
		class string // "" when unclassified
		mfa   string // "on", "off", or "" for undecided
	}
	payload := func(r rec) map[string]any {
		p := map[string]any{
			"app_id":               r.id,
			"app_name":             "app-" + r.id,
			"status":               "ACTIVE",
			"assigned_group_ids":   []string{"00g1"},
			"assigned_group_count": 1,
			"sign_on_policy_id":    "rst1",
		}
		if r.class != "" {
			p["classification"] = r.class
		}
		if r.mfa == "" {
			p["reason"] = "rule rul1 verification method is not ASSURANCE"
		} else {
			p["sign_on_mfa_required"] = r.mfa == "on"
		}
		return p
	}
	cases := []struct {
		name    string
		records []rec
		want    string
	}{
		{"admin and infrastructure require mfa", []rec{{"0oa1", "admin", "on"}, {"0oa2", "infrastructure", "on"}, {"0oa3", "standard", "off"}}, eval.ResultPass},
		{"admin app allows 1fa", []rec{{"0oa1", "admin", "off"}, {"0oa2", "infrastructure", "on"}}, eval.ResultFail},
		{"admin app undecided", []rec{{"0oa1", "admin", ""}}, eval.ResultInconclusive},
		{"only standard apps", []rec{{"0oa3", "standard", "off"}}, eval.ResultInconclusive},
		{"unclassified app allows 1fa", []rec{{"0oa1", "", "off"}}, eval.ResultInconclusive},
		{"no record", nil, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, cc66MFABundle)
			for _, r := range tc.records {
				seedStockEvidence(t, admin, tenant, ctrl, "okta.app_assignment.v1", observed, "inconclusive", payload(r))
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}

const cc41Bundle = "soc2_cc4_1_continuous_monitoring"

// #2822: cc4_1 decides on each host's latest last_seen_at (Fleet's
// seen_time) against the evaluation instant, not on the pull time
// (observed_at) and not on the osquery connector's verdict. The replay case
// shows the window is measured from the as-of instant, not the wall clock.
func TestCC41_EngineEvaluatesHostCheckIn(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	now := time.Now().UTC()

	type rec struct {
		connectorResult string
		host            string
		observedAgo     time.Duration
		seenAgo         time.Duration // 0 for no last_seen_at
	}
	payload := func(r rec) map[string]any {
		p := map[string]any{
			"host_uuid":               r.host,
			"hostname":                "host-" + r.host[:4],
			"platform":                "darwin",
			"disk_encryption_enabled": true,
		}
		if r.seenAgo != 0 {
			p["last_seen_at"] = now.Add(-r.seenAgo).Format(time.RFC3339)
		}
		return p
	}
	const h1, h2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	// connectorResult is the osquery verdict (disk encryption plus screen
	// lock). The query must ignore it.
	cases := []struct {
		name    string
		records []rec
		asOf    time.Time
		want    string
	}{
		{"every host seen in 24h", []rec{{"fail", h1, time.Hour, 2 * time.Hour}, {"pass", h2, time.Hour, 20 * time.Hour}}, eval.FarFuture, eval.ResultPass},
		{"one host last seen 30h ago", []rec{{"pass", h1, time.Hour, 2 * time.Hour}, {"pass", h2, time.Hour, 30 * time.Hour}}, eval.FarFuture, eval.ResultFail},
		{"latest record three days old", []rec{{"pass", h1, 72 * time.Hour, 73 * time.Hour}}, eval.FarFuture, eval.ResultFail},
		{"no last_seen_at is inconclusive", []rec{{"pass", h1, time.Hour, 0}}, eval.FarFuture, eval.ResultInconclusive},
		{"replay measures from the as-of instant", []rec{{"pass", h1, 50 * time.Hour, 51 * time.Hour}}, now.Add(-48 * time.Hour), eval.ResultPass},
		{"no record", nil, eval.FarFuture, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, cc41Bundle)
			for _, r := range tc.records {
				seedStockEvidence(t, admin, tenant, ctrl, "osquery.host_posture.v1", now.Add(-r.observedAgo), r.connectorResult, payload(r))
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, tc.asOf); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}

// #2823, #2925: both cc8_1 checks decide on each repo's latest
// github.repo_protection.v2 payload, keyed by repo_id, not on the GitHub
// connector's verdict (required_reviews >= 1). An unread protection field
// is omitted and leaves the check inconclusive. #2827: change management
// also requires a status check and blocked force pushes on production
// repos, and configuration change control checks only iac repos.
func TestCC81_EngineEvaluatesRepoProtectionPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	observed := time.Now().UTC().Add(-2 * time.Hour)

	type rec struct {
		connectorResult string
		repoID          int64
		reviews         int // -1: not read
		admins          string
		topics          []string // nil: iac + production
		noChecks        bool     // required_status_checks empty
	}
	payload := func(r rec) map[string]any {
		p := map[string]any{
			"repo_id":        r.repoID,
			"repo_full_name": fmt.Sprintf("acme/repo-%d", r.repoID),
			"default_branch": "main",
		}
		topics := r.topics
		if topics == nil {
			topics = []string{"iac", "production"}
		}
		p["topics"] = topics
		p["deploys_to_production"] = slices.Contains(topics, "production")
		if r.reviews < 0 {
			p["reason"] = "branch protection: github: HTTP 502"
			return p
		}
		p["required_reviews"] = r.reviews
		p["require_code_owner_reviews"] = false
		p["require_signed_commits"] = false
		p["require_linear_history"] = false
		p["protection_source"] = "classic"
		p["required_status_checks"] = []string{"ci/build"}
		if r.noChecks {
			p["required_status_checks"] = []string{}
		}
		p["allow_force_pushes"] = false
		if r.admins != "" {
			p["enforce_admins"] = r.admins == "on"
		}
		return p
	}
	const r1, r2 = 1296269, 1296270
	// connectorResult is the GitHub verdict (required_reviews >= 1). The
	// queries must ignore it.
	cases := []struct {
		bundle  string
		name    string
		records []rec
		want    string
	}{
		{"soc2_cc8_1_change_management", "reviewed and admin-enforced", []rec{{"pass", r1, 1, "on", nil, false}, {"fail", r2, 2, "on", nil, false}}, eval.ResultPass},
		{"soc2_cc8_1_change_management", "admins not enforced", []rec{{"pass", r1, 1, "on", nil, false}, {"pass", r2, 1, "off", nil, false}}, eval.ResultFail},
		{"soc2_cc8_1_change_management", "no approving review", []rec{{"pass", r1, 0, "on", nil, false}}, eval.ResultFail},
		{"soc2_cc8_1_change_management", "admin enforcement unread", []rec{{"pass", r1, 1, "", nil, false}}, eval.ResultInconclusive},
		{"soc2_cc8_1_change_management", "unread repo", []rec{{"pass", r1, 1, "on", nil, false}, {"inconclusive", r2, -1, "", nil, false}}, eval.ResultInconclusive},
		{"soc2_cc8_1_change_management", "no record", nil, eval.ResultInconclusive},
		{"soc2_cc8_1_change_management", "production repo without status check", []rec{{"pass", r1, 1, "on", nil, false}, {"pass", r2, 1, "on", nil, true}}, eval.ResultFail},
		{"soc2_cc8_1_change_management", "non-production repo without status check", []rec{{"pass", r1, 1, "on", []string{"iac"}, true}}, eval.ResultPass},
		{"soc2_cc8_1_configuration_change_control", "reviewed, admins not enforced", []rec{{"fail", r1, 1, "off", nil, false}, {"pass", r2, 2, "on", nil, false}}, eval.ResultPass},
		{"soc2_cc8_1_configuration_change_control", "no approving review", []rec{{"pass", r1, 1, "on", nil, false}, {"pass", r2, 0, "on", nil, false}}, eval.ResultFail},
		{"soc2_cc8_1_configuration_change_control", "unread repo", []rec{{"inconclusive", r1, -1, "", nil, false}}, eval.ResultInconclusive},
		{"soc2_cc8_1_configuration_change_control", "observed failure outranks unread", []rec{{"inconclusive", r1, -1, "", nil, false}, {"fail", r2, 0, "off", nil, false}}, eval.ResultFail},
		{"soc2_cc8_1_configuration_change_control", "no record", nil, eval.ResultInconclusive},
		{"soc2_cc8_1_configuration_change_control", "non-iac repo is out of scope", []rec{{"pass", r1, 1, "on", nil, false}, {"fail", r2, 0, "off", []string{"web"}, false}}, eval.ResultPass},
		{"soc2_cc8_1_configuration_change_control", "only non-iac repos", []rec{{"fail", r1, 0, "off", []string{"web"}, false}}, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.bundle+"/"+tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, tc.bundle)
			for _, r := range tc.records {
				seedStockEvidence(t, admin, tenant, ctrl, "github.repo_protection.v2", observed, r.connectorResult, payload(r))
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}

// #2824: the four windowed checks decide on the age of each record against
// the engine's evaluation instant, not on the record's own verdict. cc6_3,
// cc6_5 and cc9_1 need any keyed record inside 90 or 365 days of
// observed_at; cc1_4 needs each person's latest completed_at inside 365 days
// from an allowed completion_source. Each window is shown just inside and
// just outside, and a replay is measured from its as-of instant.
func TestWindowedStockChecks_EngineEvaluatesAgainstEvaluationInstant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	now := time.Now().UTC()
	day := 24 * time.Hour

	const (
		cc63 = "soc2_cc6_3_periodic_access_review"
		cc14 = "soc2_cc1_4_competence_training"
		cc65 = "soc2_cc6_5_asset_disposal"
		cc91 = "soc2_cc9_1_business_continuity"
	)
	type rec struct {
		connectorResult string
		ago             time.Duration
		source          string // cc1_4 only
	}
	kindOf := map[string]string{
		cc63: "access_review.completion.v1",
		cc14: "security_awareness.training_completion.v1",
		cc65: "manual.upload.v1",
		cc91: "manual.upload.v1",
	}
	payload := func(bundle string, i int, r rec) map[string]any {
		switch bundle {
		case cc63:
			return map[string]any{
				"review_id":      fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
				"completed_by":   "alice@example.com",
				"reviewer_role":  "campaign_reviewer",
				"users_reviewed": 42,
			}
		case cc14:
			return map[string]any{
				"assignment_id":     fmt.Sprintf("00000000-0000-4000-8000-%012d", i),
				"campaign_id":       "7f000000-0000-4000-8000-000000000001",
				"campaign_name":     "annual security awareness",
				"course_code":       "SAT-101",
				"course_title":      "Security awareness fundamentals",
				"person_id":         "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
				"completed_at":      now.Add(-r.ago).Format(time.RFC3339Nano),
				"completion_source": r.source,
			}
		default:
			return map[string]any{
				"uploaded_by":  "owner@example.com",
				"filename":     fmt.Sprintf("evidence-%d.pdf", i),
				"content_type": "application/pdf",
			}
		}
	}
	// connectorResult is the record's own verdict (always pass from the
	// platform's writers, inconclusive from the manual connector). The
	// queries must ignore it.
	cases := []struct {
		bundle  string
		name    string
		records []rec
		asOf    time.Time
		want    string
	}{
		{cc63, "review 89 days old", []rec{{"pass", 89 * day, ""}}, eval.FarFuture, eval.ResultPass},
		{cc63, "review 91 days old", []rec{{"pass", 91 * day, ""}}, eval.FarFuture, eval.ResultFail},
		{cc63, "replay measures from the as-of instant", []rec{{"pass", 100 * day, ""}}, now.Add(-20 * day), eval.ResultPass},
		{cc63, "no record", nil, eval.FarFuture, eval.ResultInconclusive},
		{cc65, "upload 89 days old", []rec{{"inconclusive", 89 * day, ""}}, eval.FarFuture, eval.ResultPass},
		{cc65, "upload 91 days old", []rec{{"inconclusive", 91 * day, ""}}, eval.FarFuture, eval.ResultFail},
		{cc65, "no record", nil, eval.FarFuture, eval.ResultInconclusive},
		{cc91, "upload 364 days old", []rec{{"inconclusive", 364 * day, ""}}, eval.FarFuture, eval.ResultPass},
		{cc91, "upload 366 days old", []rec{{"inconclusive", 366 * day, ""}}, eval.FarFuture, eval.ResultFail},
		{cc91, "no record", nil, eval.FarFuture, eval.ResultInconclusive},
		{cc14, "completion 364 days old", []rec{{"pass", 364 * day, "lms_connector"}}, eval.FarFuture, eval.ResultPass},
		{cc14, "completion 366 days old", []rec{{"pass", 366 * day, "csv"}}, eval.FarFuture, eval.ResultFail},
		{cc14, "disallowed completion source", []rec{{"pass", 10 * day, "self_attested"}}, eval.FarFuture, eval.ResultFail},
		{cc14, "replay measures from the as-of instant", []rec{{"pass", 380 * day, "manual"}}, now.Add(-30 * day), eval.ResultPass},
		{cc14, "no record", nil, eval.FarFuture, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.bundle+"/"+tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, tc.bundle)
			for i, r := range tc.records {
				seedStockEvidence(t, admin, tenant, ctrl, kindOf[tc.bundle], now.Add(-r.ago), r.connectorResult, payload(tc.bundle, i, r))
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, tc.asOf); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}

type hostPostureRec struct {
	connectorResult string
	payload         map[string]any
}

// evaluateStockHostPosture seeds one tenant with the bundle and the given
// osquery.host_posture.v1 payloads (each with its connector verdict), runs
// the engine and returns the stored control state.
func evaluateStockHostPosture(t *testing.T, admin *pgxpool.Pool, engine *eval.Engine, bundle string, records []hostPostureRec) string {
	t.Helper()
	observed := time.Now().UTC().Add(-2 * time.Hour)
	tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	ctrl := seedStockBundleControl(t, admin, tenant, bundle)
	for _, r := range records {
		seedStockEvidence(t, admin, tenant, ctrl, "osquery.host_posture.v1", observed, r.connectorResult, r.payload)
	}
	if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl: %v", err)
	}
	states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
	if err != nil {
		t.Fatalf("ControlState: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("want 1 state, got %d", len(states))
	}
	return states[0].Result
}

// #2826: cc6_8 and cc7_2 decide on each host's latest antimalware_protected
// / edr_healthy (read from a named Fleet policy), not on the osquery
// connector's verdict. Absent is inconclusive, an observed false fails.
func TestCC68CC72_EngineEvaluatesPolicyBooleanPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	const h1, h2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	for _, b := range []struct{ bundle, field, source string }{
		{"soc2_cc6_8_malware_protection", "antimalware_protected", "antimalware_source"},
		{"soc2_cc7_2_anomaly_detection", "edr_healthy", "edr_source"},
	} {
		rec := func(connectorResult, host, v string) hostPostureRec {
			p := map[string]any{"host_uuid": host, "hostname": "host-" + host[:4], "platform": "darwin", "disk_encryption_enabled": true}
			if v != "" {
				p[b.field] = v == "on"
				p[b.source] = "fleet_policy:named"
			}
			return hostPostureRec{connectorResult, p}
		}
		cases := []struct {
			name    string
			records []hostPostureRec
			want    string
		}{
			{"every host on", []hostPostureRec{rec("fail", h1, "on"), rec("pass", h2, "on")}, eval.ResultPass},
			{"one host off", []hostPostureRec{rec("pass", h1, "on"), rec("pass", h2, "off")}, eval.ResultFail},
			{"unread host", []hostPostureRec{rec("pass", h1, "on"), rec("pass", h2, "")}, eval.ResultInconclusive},
			{"unread plus off fails", []hostPostureRec{rec("pass", h1, ""), rec("pass", h2, "off")}, eval.ResultFail},
			{"no record", nil, eval.ResultInconclusive},
		}
		for _, tc := range cases {
			t.Run(b.bundle+"/"+tc.name, func(t *testing.T) {
				if got := evaluateStockHostPosture(t, admin, engine, b.bundle, tc.records); got != tc.want {
					t.Errorf("result = %q, want %q", got, tc.want)
				}
			})
		}
	}
}

// #2826: a1_1 decides on each production host's latest disk, memory and
// CPU p95 readings, not on the osquery connector's verdict.
func TestA11_EngineEvaluatesCapacityPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	const h1, h2 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	// Readings are disk, memory, CPU p95; a negative value is not read.
	rec := func(connectorResult, host string, prod bool, disk, mem, cpu float64) hostPostureRec {
		p := map[string]any{"host_uuid": host, "hostname": "host-" + host[:4], "platform": "linux"}
		if prod {
			p["host_role"] = "production"
			p["host_role_source"] = "fleet_label:Production"
		}
		for k, v := range map[string]float64{"disk_used_percent": disk, "memory_used_percent": mem, "cpu_p95_5m_percent": cpu} {
			if v >= 0 {
				p[k] = v
			}
		}
		return hostPostureRec{connectorResult, p}
	}
	cases := []struct {
		name    string
		records []hostPostureRec
		want    string
	}{
		{"every production host under", []hostPostureRec{rec("fail", h1, true, 40, 50, 20), rec("pass", h2, false, 99, 99, 99)}, eval.ResultPass},
		{"disk at bound", []hostPostureRec{rec("pass", h1, true, 85, 10, 10)}, eval.ResultFail},
		{"fleet shape, disk only", []hostPostureRec{rec("pass", h1, true, 29, -1, -1)}, eval.ResultInconclusive},
		{"fleet shape, disk over", []hostPostureRec{rec("pass", h1, true, 91, -1, -1)}, eval.ResultFail},
		{"no production host", []hostPostureRec{rec("pass", h1, false, 99, 99, 99)}, eval.ResultInconclusive},
		{"no record", nil, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluateStockHostPosture(t, admin, engine, "soc2_a1_1_capacity_monitoring", tc.records); got != tc.want {
				t.Errorf("result = %q, want %q", got, tc.want)
			}
		})
	}
}

// #2829: cc7_1 vulnerability remediation and cc1_4's training-ticket query
// decide on each ticket's latest jira.ticket_evidence.v1 payload (labels,
// severity, created_at, resolved_at, resolution, assignee_account_id),
// not on the Jira connector's status verdict. An open ticket's age is
// measured from the evaluation instant, so a replay as of an earlier
// instant sees it younger.
func TestJiraTicketChecks_EngineEvaluatesTicketPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	now := time.Now().UTC()
	observed := now.Add(-2 * time.Hour)
	hour := time.Hour
	ts := func(ago time.Duration) string { return now.Add(-ago).Format(time.RFC3339Nano) }

	const (
		cc71 = "soc2_cc7_1_vulnerability_remediation"
		cc14 = "soc2_cc1_4_competence_training"
	)
	vuln := func(key, severity string, createdAgo, resolvedAgo time.Duration) map[string]any {
		p := map[string]any{
			"ticket_key": key, "status": "In Progress", "labels": []string{"vulnerability"},
			"created_at": ts(createdAgo),
		}
		if severity != "" {
			p["severity"] = severity
		}
		if resolvedAgo > 0 {
			p["status"], p["resolution"], p["resolved_at"] = "Done", "Done", ts(resolvedAgo)
		}
		return p
	}
	training := func(key, account, resolution string, resolvedAgo time.Duration) map[string]any {
		p := map[string]any{
			"ticket_key": key, "status": "To Do", "labels": []string{"security-training"},
			"assignee_account_id": account,
		}
		if resolution != "" {
			p["status"], p["resolution"], p["resolved_at"] = "Done", resolution, ts(resolvedAgo)
		}
		return p
	}
	cases := []struct {
		bundle   string
		name     string
		payloads []map[string]any
		asOf     time.Time
		want     string
	}{
		{cc71, "every severity within sla", []map[string]any{
			vuln("V-1", "critical", 100*hour, 0),
			vuln("V-2", "high", 600*hour, 0),
			vuln("V-3", "medium", 3000*hour, 1000*hour),
		}, eval.FarFuture, eval.ResultPass},
		{cc71, "open critical past 168 hours", []map[string]any{vuln("V-1", "critical", 170*hour, 0)}, eval.FarFuture, eval.ResultFail},
		{cc71, "high resolved after 721 hours", []map[string]any{vuln("V-2", "high", 800*hour, 79*hour)}, eval.FarFuture, eval.ResultFail},
		{cc71, "replay measures from the as-of instant", []map[string]any{vuln("V-1", "critical", 170*hour, 0)}, now.Add(-10 * hour), eval.ResultPass},
		{cc71, "no severity past 168 hours", []map[string]any{vuln("V-5", "", 200*hour, 0)}, eval.FarFuture, eval.ResultInconclusive},
		{cc71, "only training tickets", []map[string]any{training("S-1", "acct-1", "", 0)}, eval.FarFuture, eval.ResultInconclusive},
		{cc71, "no record", nil, eval.FarFuture, eval.ResultInconclusive},
		{cc14, "every assignee trained within 365 days", []map[string]any{
			training("S-1", "acct-1", "Done", 100*24*hour),
			training("S-2", "acct-2", "Done", 364*24*hour),
		}, eval.FarFuture, eval.ResultPass},
		{cc14, "training resolved 366 days ago", []map[string]any{training("S-1", "acct-1", "Done", 366*24*hour)}, eval.FarFuture, eval.ResultFail},
		{cc14, "open training ticket", []map[string]any{training("S-1", "acct-1", "", 0)}, eval.FarFuture, eval.ResultFail},
		{cc14, "replay measures from the as-of instant", []map[string]any{training("S-1", "acct-1", "Done", 380*24*hour)}, now.Add(-30 * 24 * hour), eval.ResultPass},
		{cc14, "only vulnerability tickets", []map[string]any{vuln("V-1", "critical", 900*hour, 0)}, eval.FarFuture, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.bundle+"/"+tc.name, func(t *testing.T) {
			tenant := dbtest.SeedTenant(t, admin, "control_evaluations", "evidence_records", "controls")
			ctx, err := tenancy.WithTenant(context.Background(), tenant)
			if err != nil {
				t.Fatalf("WithTenant: %v", err)
			}
			ctrl := seedStockBundleControl(t, admin, tenant, tc.bundle)
			// A replay sees only records observed by its as-of instant.
			obs := observed
			if tc.asOf.Before(obs) {
				obs = tc.asOf.Add(-hour)
			}
			for _, p := range tc.payloads {
				// The connector's verdict is pass for a terminal status; the
				// queries must ignore it.
				seedStockEvidence(t, admin, tenant, ctrl, "jira.ticket_evidence.v1", obs, "pass", p)
			}
			if _, err := engine.EvaluateControl(ctx, ctrl, eval.TriggerManual, tc.asOf); err != nil {
				t.Fatalf("EvaluateControl: %v", err)
			}
			states, err := engine.ControlState(ctx, ctrl, "", eval.FarFuture)
			if err != nil {
				t.Fatalf("ControlState: %v", err)
			}
			if len(states) != 1 {
				t.Fatalf("want 1 state, got %d", len(states))
			}
			if states[0].Result != tc.want {
				t.Errorf("result = %q, want %q", states[0].Result, tc.want)
			}
		})
	}
}
