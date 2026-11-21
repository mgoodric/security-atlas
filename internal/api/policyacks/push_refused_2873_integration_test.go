//go:build integration

// #2873: an unbound policy.acknowledgment.v1 record counts for cc1_5, cc5_3
// and cc7_4 tenant-wide (#2817), so only the acknowledgement handler may
// write one. The generic evidence push, over HTTP and over gRPC, refuses
// the kind; the handler's own record still lands.

package policyacks_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/api/policyacks"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

// forgedAckPayload is a payload that passes the kind's schema, so the only
// reason to refuse it is who sent it.
func forgedAckPayload() map[string]any {
	return map[string]any{
		"policy_id":         "00000000-0000-0000-0000-000000002873",
		"policy_version_id": "00000000-0000-0000-0000-000000002874",
		"user_id":           "00000000-0000-0000-0000-000000002875",
		"acknowledged_at":   time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
	}
}

func TestPush2873_GenericPushRefusesAckKind(t *testing.T) {
	s := setup(t)

	t.Run("http", func(t *testing.T) {
		payload := forgedAckPayload()
		body := fmt.Sprintf(`{"record": {
			"idempotency_key": "forged-ack-http-2873",
			"evidence_kind": %q,
			"schema_version": %q,
			"control_id": "policy:%s:v%s",
			"scope": [{"key": "policy_id", "values": [%q]}],
			"observed_at": %q,
			"result": "pass",
			"payload": {"policy_id": %q, "policy_version_id": %q, "user_id": %q, "acknowledged_at": %q},
			"source_attribution": {"actor_type": "service_account", "actor_id": "forger-2873"}
		}}`,
			policyacks.EvidenceKind, policyacks.EvidenceVersion,
			payload["policy_id"], payload["policy_version_id"], payload["policy_id"],
			time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
			payload["policy_id"], payload["policy_version_id"], payload["user_id"], payload["acknowledged_at"])
		s.do(t, http.MethodPost, "/v1/evidence:push", []byte(body), s.adminBear, http.StatusForbidden)
	})

	t.Run("grpc", func(t *testing.T) {
		pl, err := structpb.NewStruct(forgedAckPayload())
		if err != nil {
			t.Fatalf("payload: %v", err)
		}
		rec := &evidencev1.EvidenceRecord{
			IdempotencyKey: "forged-ack-grpc-2873",
			EvidenceKind:   policyacks.EvidenceKind,
			SchemaVersion:  policyacks.EvidenceVersion,
			ControlId:      "policy:forged:v1",
			Scope:          []*evidencev1.ScopeDimension{{Key: "policy_id", Values: []string{"forged"}}},
			ObservedAt:     timestamppb.New(time.Now().Add(-time.Minute)),
			Result:         evidencev1.Result_RESULT_PASS,
			Payload:        pl,
			SourceAttribution: &evidencev1.SourceAttribution{
				ActorType: "service_account",
				ActorId:   "forger-2873",
			},
		}
		// An unrestricted push key: no kind list, no scope predicate.
		cred := credstore.Credential{ID: "push-key-2873", TenantID: s.tenant}
		ctx := authctx.WithCredential(context.Background(), cred)
		_, perr := apievidence.New(s.ingester, nil, nil).Push(ctx, &evidencev1.PushRequest{Record: rec})
		if st, _ := status.FromError(perr); st.Code() != codes.PermissionDenied || !strings.Contains(st.Message(), policyacks.EvidenceKind) {
			t.Fatalf("gRPC push of %s: got %v, want PermissionDenied naming the kind", policyacks.EvidenceKind, perr)
		}
		// The ingest service the gRPC path wraps refuses it as an owned kind.
		if _, _, ierr := s.ingester.Process(context.Background(), rec, cred); !errors.Is(ierr, ingest.ErrOwnedKind) {
			t.Fatalf("ingest.Process of %s: got %v, want ErrOwnedKind", policyacks.EvidenceKind, ierr)
		}
	})

	if n := countEvidenceRecords(t, s.admin, s.tenant, policyacks.EvidenceKind); n != 0 {
		t.Fatalf("after refused pushes: %d %s records, want 0", n, policyacks.EvidenceKind)
	}

	// The handler is still the kind's writer.
	policyID := createAndPublishPolicy(t, s, "Acceptable Use Policy", "1", []string{s.requiredRole}, s.controlID)
	_, bearer := s.makeUserAndBearer(t, "ack-2873@test", []string{s.requiredRole}, false)
	s.postJSON(t, "/v1/policies/"+policyID+"/acknowledge", []byte("{}"), bearer, http.StatusCreated)
	if n := countEvidenceRecords(t, s.admin, s.tenant, policyacks.EvidenceKind); n != 1 {
		t.Fatalf("after handler ack: %d %s records, want 1", n, policyacks.EvidenceKind)
	}
}
