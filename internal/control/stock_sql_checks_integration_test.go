//go:build integration

// #2821: stock payload checks run in SQL (ADR 0028), which needs a real
// Postgres session. Two layers prove them:
//
//   - every stock bundle's tests/ cases run through bundletest.Run inside a
//     tenant transaction, as the upload gate runs them; and
//   - cc6_1, the tracer, and each later check (cc6_6, #2822; both cc8_1
//     bundles, #2823) are evaluated by the live engine against evidence
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
