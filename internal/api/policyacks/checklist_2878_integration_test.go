//go:build integration

// #2878: the checklist's has_evidence applies the same control-evidence rule
// as every other ledger reader. Before it matched control_id only, so the
// control-free acknowledgement record never reached cc1_5, cc5_3 or cc7_4 and
// the checklist marked them "no evidence" while they evaluated.

package policyacks_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/checklist"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestAcknowledge2878_ChecklistSeesAckEvidence(t *testing.T) {
	s := setup(t)

	controls := make(map[string]uuid.UUID, len(ackStockBundles))
	for _, b := range ackStockBundles {
		controls[b] = seedStockControl(t, s.admin, s.tenant, b)
	}

	ctx, err := tenancy.WithTenant(context.Background(), s.tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	store := checklist.NewStore(s.app)
	hasEvidence := func() map[string]bool {
		t.Helper()
		in, err := store.InScopeControls(ctx)
		if err != nil {
			t.Fatalf("InScopeControls: %v", err)
		}
		out := make(map[string]bool, len(in))
		for _, c := range in {
			out[c.ID] = c.HasEvidence
		}
		return out
	}

	before := hasEvidence()
	for b, id := range controls {
		if before[id.String()] {
			t.Fatalf("%s before ack: has_evidence = true, want false", b)
		}
	}

	policyID := createAndPublishPolicy(t, s, "Information Security Policy", "1", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "ack-2878@test", []string{s.requiredRole}, false)
	s.postJSON(t, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)

	after := hasEvidence()
	for b, id := range controls {
		if !after[id.String()] {
			t.Errorf("%s after ack: has_evidence = false, want true", b)
		}
	}
	// The unrelated control declares no evidence query for the kind.
	if got, ok := after[s.controlID.String()]; !ok || got {
		t.Errorf("unrelated control after ack: has_evidence = %v (present %v), want false", got, ok)
	}

	// The control_ref leg: a record bound by control_ref alone reaches the
	// control it names.
	if _, err := s.admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, ingested_at,
			provenance, result, payload, hash, evidence_kind
		)
		VALUES ($1, $2, NULL, $3, now(), now(),
			'{"connector_id":"test-2878"}'::jsonb, 'pass', '{}'::jsonb, $4, 'access_review.completion')
	`, uuid.New(), s.tenant, s.controlID.String(), "hash-2878-"+uuid.NewString()[:8]); err != nil {
		t.Fatalf("seed control_ref record: %v", err)
	}
	if !hasEvidence()[s.controlID.String()] {
		t.Errorf("control_ref-bound record: has_evidence = false, want true")
	}
}
