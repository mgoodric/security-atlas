//go:build integration

// #2832: the stock ack checks key on the policy title and version the
// acknowledgement handler snapshots into the payload. This drives the
// records through POST /v1/policies/{id}/acknowledge, so the verdicts are
// over what the handler really writes, not over hand-shaped fixtures.

package policyacks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// stockPolicyTitles are the five titles cc1_5 checks, as policies/stock/*.md
// declares them.
var stockPolicyTitles = []string{
	"Information Security Policy",
	"Access Control Policy",
	"Vendor Management Policy",
	"Incident Response Plan",
	"Change Management Policy",
}

func TestAcknowledge2832_StockAckChecksReadTitleAndVersion(t *testing.T) {
	s := setup(t)
	t.Cleanup(func() {
		if _, err := s.admin.Exec(context.Background(),
			`DELETE FROM control_evaluations WHERE tenant_id = $1`, s.tenant); err != nil {
			t.Logf("cleanup control_evaluations: %v", err)
		}
	})
	controls := make(map[string]uuid.UUID, len(ackStockBundles))
	for _, b := range ackStockBundles {
		controls[b] = seedStockControl(t, s.admin, s.tenant, b)
	}
	ctx, err := tenancy.WithTenant(context.Background(), s.tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	engine := eval.NewEngine(eval.NewStore(s.app), scope.NewStore(s.app))
	check := func(stage string, want map[string]string) {
		t.Helper()
		for _, b := range ackStockBundles {
			if _, err := engine.EvaluateControl(ctx, controls[b], eval.TriggerManual, time.Now().Add(time.Hour)); err != nil {
				t.Fatalf("evaluate %s: %v", b, err)
			}
			if res, _ := latestEvaluation(t, s.admin, s.tenant, controls[b]); res != want[b] {
				t.Errorf("%s: %s = %s, want %s", stage, b, res, want[b])
			}
		}
	}

	policies := make(map[string]string, len(stockPolicyTitles))
	for _, title := range stockPolicyTitles {
		policies[title] = createAndPublishPolicy(t, s, title, "1", []string{s.requiredRole}, s.controlID)
	}
	ack := func(bearer, title string) {
		t.Helper()
		s.postJSON(t, "/v1/policies/"+policies[title]+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)
	}
	_, alice := s.makeUserAndBearer(t, "alice-2832@test", []string{s.requiredRole}, false)
	_, bob := s.makeUserAndBearer(t, "bob-2832@test", []string{s.requiredRole}, false)

	for _, title := range stockPolicyTitles {
		ack(alice, title)
	}
	ack(bob, "Incident Response Plan")

	// The handler snapshots the title and version into every record.
	var payload []byte
	if err := s.admin.QueryRow(context.Background(), `
		SELECT payload FROM evidence_records
		WHERE tenant_id = $1 AND evidence_kind = 'policy.acknowledgment.v1'
		  AND payload->>'policy_id' = $2
		LIMIT 1
	`, s.tenant, policies["Vendor Management Policy"]).Scan(&payload); err != nil {
		t.Fatalf("read ack payload: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p["policy_title"] != "Vendor Management Policy" || p["policy_version"] != "1" {
		t.Errorf("payload policy_title=%v policy_version=%v, want Vendor Management Policy / 1", p["policy_title"], p["policy_version"])
	}

	// Bob has acknowledged only the IR plan, so cc1_5 fails on him.
	check("bob acked one of five", map[string]string{
		"soc2_cc1_5_accountability_acknowledgment": eval.ResultFail,
		"soc2_cc5_3_policies_procedures":           eval.ResultPass,
		"soc2_cc7_4_incident_response":             eval.ResultPass,
	})

	for _, title := range stockPolicyTitles {
		if title != "Incident Response Plan" {
			ack(bob, title)
		}
	}
	check("both users acked all five", map[string]string{
		"soc2_cc1_5_accountability_acknowledgment": eval.ResultPass,
		"soc2_cc5_3_policies_procedures":           eval.ResultPass,
		"soc2_cc7_4_incident_response":             eval.ResultPass,
	})
}
