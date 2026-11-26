//go:build integration

// #2873: with a JetStream publisher wired, the push answers before the
// consumer runs Process, so the handler refuses an owner-only kind itself.
// The refusal is a 403 and still leaves a rejected_scope_violation audit
// row, and nothing reaches the publisher.

package evidence_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// countingPublisher stands in for the JetStream publisher and records
// whether the handler handed it anything.
type countingPublisher struct{ calls int }

func (p *countingPublisher) Publish(_ context.Context, _ *evidencev1.EvidenceRecord, _ credstore.Credential) (ingest.Receipt, ingest.Decision, error) {
	p.calls++
	return ingest.Receipt{RecordID: "00000000-0000-0000-0000-000000002873"}, ingest.DecisionAccepted, nil
}

func TestEvidencePush2873_PublisherPathRefusesAndAuditsOwnedKind(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin, "evidence_audit_log", "evidence_records")

	pub := &countingPublisher{}
	handler := apievidence.NewHTTPHandler(ingest.New(app, allowAllValidator{}), 0).WithPublisher(pub)
	cred := credstore.Credential{ID: "key_2873_push", TenantID: tenant}

	const idem = "forged-ack-publish-2873"
	body := strings.Replace(pushBody(idem), `"sast.scan_result.v1"`, `"policy.acknowledgment.v1"`, 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/evidence:push", strings.NewReader(body))
	req = req.WithContext(authctx.WithCredential(context.Background(), cred))
	rr := httptest.NewRecorder()
	handler.PushHTTP(rr, req)

	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "scope_violation") {
		t.Fatalf("publisher-path push of policy.acknowledgment.v1: status %d body %s, want 403 scope_violation", rr.Code, rr.Body.String())
	}
	if pub.calls != 0 {
		t.Fatalf("publisher got %d records, want 0", pub.calls)
	}

	ctx, cancel := context.WithTimeout(context.Background(), harnessTimeout)
	defer cancel()
	var n int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM evidence_audit_log
		WHERE tenant_id = $1 AND credential_id = $2 AND decision = 'rejected_scope_violation'
		  AND evidence_kind = 'policy.acknowledgment.v1' AND idempotency_key = $3`,
		tenant, cred.ID, idem).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("rejected_scope_violation audit rows for the forged push = %d, want 1", n)
	}

	// An ordinary kind still goes to the publisher.
	req = httptest.NewRequest(http.MethodPost, "/v1/evidence:push", strings.NewReader(pushBody("ordinary-publish-2873")))
	req = req.WithContext(authctx.WithCredential(context.Background(), cred))
	rr = httptest.NewRecorder()
	handler.PushHTTP(rr, req)
	if pub.calls != 1 {
		t.Fatalf("ordinary push: publisher got %d records (status %d body %s), want 1", pub.calls, rr.Code, rr.Body.String())
	}
}
