// #2873: an owner-only kind (policy.acknowledgment.v1) is refused on every
// Service except the one its handler builds with WithOwnedKind. Pool-free:
// the refusal comes before the DB, and the allowed case stops at the
// ledger-marshal seam (marshal_count_test.go).
package ingest

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

const ackKind = "policy.acknowledgment.v1"

func ownedKindFixture(t *testing.T) *Service {
	t.Helper()
	stop := func(proto.Message) ([]byte, error) { return nil, errStopAfterMarshal }
	// A redaction rule routes Process through the seam, which ends it
	// before the DB write.
	return serviceForCountTest(t, &countingValidator{rules: []string{"$.secret_value"}}, stop)
}

func TestOwnedKind_GenericServiceRefusesAck(t *testing.T) {
	t.Parallel()
	rec := fixedFixture(t)
	rec.EvidenceKind = ackKind
	cred := credstore.Credential{ID: "push-key", TenantID: "11111111-1111-1111-1111-111111111111"}

	_, decision, err := ownedKindFixture(t).Process(context.Background(), rec, cred)
	if !errors.Is(err, ErrOwnedKind) || !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("generic push of %s: err=%v, want ErrOwnedKind wrapping ErrScopeViolation", ackKind, err)
	}
	if decision != DecisionRejectedScopeViolation {
		t.Fatalf("decision=%s, want %s", decision, DecisionRejectedScopeViolation)
	}
}

func TestOwnedKind_OwnerServiceWritesAck(t *testing.T) {
	t.Parallel()
	rec := fixedFixture(t)
	rec.EvidenceKind = ackKind
	cred := credstore.Credential{ID: "ack-handler", TenantID: "11111111-1111-1111-1111-111111111111"}

	_, _, err := ownedKindFixture(t).WithOwnedKind(ackKind).Process(context.Background(), rec, cred)
	if !errors.Is(err, errStopAfterMarshal) {
		t.Fatalf("owner push of %s: err=%v, want it to pass the owner check and reach the ledger marshal", ackKind, err)
	}
}

func TestOwnedKind_OtherKindsUnaffected(t *testing.T) {
	t.Parallel()
	rec := fixedFixture(t) // secret.scan.v1
	cred := credstore.Credential{ID: "push-key", TenantID: "11111111-1111-1111-1111-111111111111"}

	if _, _, err := ownedKindFixture(t).Process(context.Background(), rec, cred); !errors.Is(err, errStopAfterMarshal) {
		t.Fatalf("generic push of %s: err=%v, want it to reach the ledger marshal", rec.EvidenceKind, err)
	}
}

// The publish-path guard agrees with Process, on a built Service and on a
// nil one.
func TestOwnedKind_RefuseOwnedKind(t *testing.T) {
	t.Parallel()
	cred := credstore.Credential{ID: "push-key", TenantID: "11111111-1111-1111-1111-111111111111"}
	ordinary := fixedFixture(t)
	ack := fixedFixture(t)
	ack.EvidenceKind = ackKind
	svc := ownedKindFixture(t)
	var nilSvc *Service

	for name, s := range map[string]*Service{"built": svc, "nil": nilSvc} {
		if err := s.RefuseOwnedKind(context.Background(), ordinary, cred); err != nil {
			t.Errorf("%s: RefuseOwnedKind(%s) = %v, want nil", name, ordinary.EvidenceKind, err)
		}
		if err := s.RefuseOwnedKind(context.Background(), ack, cred); !errors.Is(err, ErrOwnedKind) {
			t.Errorf("%s: RefuseOwnedKind(%s) = %v, want ErrOwnedKind", name, ackKind, err)
		}
	}
	if err := svc.WithOwnedKind(ackKind).RefuseOwnedKind(context.Background(), ack, cred); err != nil {
		t.Errorf("owner copy: RefuseOwnedKind(%s) = %v, want nil", ackKind, err)
	}
}

func TestOwnedKind_WithOwnedKindPanicsOnOrdinaryKind(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"secret.scan.v1", "", "policy.acknowledgment.v2"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WithOwnedKind(%q) did not panic", kind)
				}
			}()
			ownedKindFixture(t).WithOwnedKind(kind)
		}()
	}
}
