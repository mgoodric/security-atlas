//go:build integration

// #2821: stock payload checks run in SQL (ADR 0028), which needs a real
// Postgres session. Two layers prove them:
//
//   - every stock bundle's tests/ cases run through bundletest.Run inside a
//     tenant transaction, as the upload gate runs them; and
//   - cc6_1, the tracer, and each later check (cc6_6 and cc4_1, #2822; both cc8_1
//     bundles, #2823; the windowed cc6_3, cc1_4, cc6_5 and cc9_1, #2824) are
//     evaluated by the live engine against evidence
//     records in the ledger, so the query is shown to decide the stored
//     control state and not only the fixture runner's.

package control_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
// is omitted and leaves the check inconclusive.
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
	}
	payload := func(r rec) map[string]any {
		p := map[string]any{
			"repo_id":        r.repoID,
			"repo_full_name": fmt.Sprintf("acme/repo-%d", r.repoID),
			"default_branch": "main",
		}
		if r.reviews < 0 {
			p["reason"] = "branch protection: github: HTTP 502"
			return p
		}
		p["required_reviews"] = r.reviews
		p["require_code_owner_reviews"] = false
		p["require_signed_commits"] = false
		p["require_linear_history"] = false
		p["protection_source"] = "classic"
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
		{"soc2_cc8_1_change_management", "reviewed and admin-enforced", []rec{{"pass", r1, 1, "on"}, {"fail", r2, 2, "on"}}, eval.ResultPass},
		{"soc2_cc8_1_change_management", "admins not enforced", []rec{{"pass", r1, 1, "on"}, {"pass", r2, 1, "off"}}, eval.ResultFail},
		{"soc2_cc8_1_change_management", "no approving review", []rec{{"pass", r1, 0, "on"}}, eval.ResultFail},
		{"soc2_cc8_1_change_management", "admin enforcement unread", []rec{{"pass", r1, 1, ""}}, eval.ResultInconclusive},
		{"soc2_cc8_1_change_management", "unread repo", []rec{{"pass", r1, 1, "on"}, {"inconclusive", r2, -1, ""}}, eval.ResultInconclusive},
		{"soc2_cc8_1_change_management", "no record", nil, eval.ResultInconclusive},
		{"soc2_cc8_1_configuration_change_control", "reviewed, admins not enforced", []rec{{"fail", r1, 1, "off"}, {"pass", r2, 2, "on"}}, eval.ResultPass},
		{"soc2_cc8_1_configuration_change_control", "no approving review", []rec{{"pass", r1, 1, "on"}, {"pass", r2, 0, "on"}}, eval.ResultFail},
		{"soc2_cc8_1_configuration_change_control", "unread repo", []rec{{"inconclusive", r1, -1, ""}}, eval.ResultInconclusive},
		{"soc2_cc8_1_configuration_change_control", "observed failure outranks unread", []rec{{"inconclusive", r1, -1, ""}, {"fail", r2, 0, "off"}}, eval.ResultFail},
		{"soc2_cc8_1_configuration_change_control", "no record", nil, eval.ResultInconclusive},
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
