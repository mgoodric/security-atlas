package oscal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// fakeBridge is a BridgeClient stub for the unit tests. It returns
// canned OSCAL-shaped JSON and lets each test pin round-trip validity
// and inject errors.
type fakeBridge struct {
	sspErr        error
	assessErr     error
	poamErr       error
	roundTripErr  error
	roundTripOK   bool
	roundTripErrs []string
	calls         []string
}

func (f *fakeBridge) SerializeSSP(_ context.Context, _ *oscalv1.SspInput) ([]byte, error) {
	f.calls = append(f.calls, "ssp")
	if f.sspErr != nil {
		return nil, f.sspErr
	}
	return []byte(`{"system-security-plan":{"uuid":"x"}}`), nil
}

func (f *fakeBridge) SerializeAssessment(_ context.Context, _ *oscalv1.AssessmentInput) ([]byte, []byte, error) {
	f.calls = append(f.calls, "assessment")
	if f.assessErr != nil {
		return nil, nil, f.assessErr
	}
	return []byte(`{"assessment-plan":{"uuid":"x"}}`), []byte(`{"assessment-results":{"uuid":"x"}}`), nil
}

func (f *fakeBridge) SerializePOAM(_ context.Context, _ *oscalv1.PoamInput) ([]byte, error) {
	f.calls = append(f.calls, "poam")
	if f.poamErr != nil {
		return nil, f.poamErr
	}
	return []byte(`{"plan-of-action-and-milestones":{"uuid":"x"}}`), nil
}

func (f *fakeBridge) RoundTripValidate(_ context.Context, _ string, _ []byte) (bool, []string, error) {
	f.calls = append(f.calls, "roundtrip")
	if f.roundTripErr != nil {
		return false, nil, f.roundTripErr
	}
	return f.roundTripOK, f.roundTripErrs, nil
}

func (f *fakeBridge) Close() error { return nil }

// minimalAggregate builds an aggregate sufficient for the proto-conversion
// methods — they only dereference period plus the (possibly empty) slices.
func minimalAggregate() *aggregate {
	now := time.Now().UTC()
	return &aggregate{
		period: dbx.AuditPeriod{
			ID:       pgUUID(uuid.New()),
			TenantID: pgUUID(uuid.New()),
			Name:     "SOC 2 2026 Q2",
			Status:   "frozen",
			FrozenAt: pgtype.Timestamptz{Time: now, Valid: true},
		},
		frozenAt:     now,
		in:           ExportInput{OrganizationName: "Acme", SystemName: "Platform"},
		controlOwner: map[uuid.UUID]string{},
		controlTitle: map[uuid.UUID]string{},
	}
}

func TestExportFromAggregateHappyPathProducesSignedBundle(t *testing.T) {
	signer, _ := NewEphemeralSigner()
	bridge := &fakeBridge{roundTripOK: true}
	e := NewExporter(nil, bridge, signer)

	bundle, err := e.exportFromAggregate(context.Background(), minimalAggregate(), "tester")
	if err != nil {
		t.Fatalf("exportFromAggregate: %v", err)
	}
	if len(bundle.Members) != 4 {
		t.Fatalf("bundle has %d members, want 4 (SSP, AP, AR, POA&M)", len(bundle.Members))
	}
	// The bundle MUST be signed — AC-5, P0 anti-criterion.
	if bundle.Signature.Algorithm != "ed25519" || bundle.Signature.Signature == "" {
		t.Fatalf("bundle is not signed: %+v", bundle.Signature)
	}
	if err := VerifyBundle(bundle); err != nil {
		t.Errorf("VerifyBundle on the exported bundle: %v", err)
	}
	// Round-trip validation MUST have run for every member — AC-6/AC-7.
	roundTrips := 0
	for _, c := range bridge.calls {
		if c == "roundtrip" {
			roundTrips++
		}
	}
	if roundTrips != 4 {
		t.Errorf("round-trip validation ran %d times, want 4 (one per member)", roundTrips)
	}
}

func TestExportFromAggregateAbortsOnRoundTripFailure(t *testing.T) {
	signer, _ := NewEphemeralSigner()
	// roundTripOK=false -> a member fails compliance-trestle validation.
	bridge := &fakeBridge{roundTripOK: false, roundTripErrs: []string{"missing required field"}}
	e := NewExporter(nil, bridge, signer)

	_, err := e.exportFromAggregate(context.Background(), minimalAggregate(), "tester")
	if !errors.Is(err, ErrRoundTripFailed) {
		t.Fatalf("expected ErrRoundTripFailed, got %v", err)
	}
}

func TestExportFromAggregateAbortsOnBridgeError(t *testing.T) {
	signer, _ := NewEphemeralSigner()
	bridge := &fakeBridge{sspErr: errors.New("connection refused")}
	e := NewExporter(nil, bridge, signer)

	_, err := e.exportFromAggregate(context.Background(), minimalAggregate(), "tester")
	if !errors.Is(err, ErrBridgeUnavailable) {
		t.Fatalf("expected ErrBridgeUnavailable, got %v", err)
	}
}

func TestExportFromAggregateAbortsOnRoundTripRPCError(t *testing.T) {
	signer, _ := NewEphemeralSigner()
	bridge := &fakeBridge{roundTripErr: errors.New("bridge died mid-validate")}
	e := NewExporter(nil, bridge, signer)

	_, err := e.exportFromAggregate(context.Background(), minimalAggregate(), "tester")
	if !errors.Is(err, ErrBridgeUnavailable) {
		t.Fatalf("expected ErrBridgeUnavailable on round-trip RPC failure, got %v", err)
	}
}

func TestExportFromAggregateAbortsOnSigningFailure(t *testing.T) {
	// A zero-value Signer (no key) makes SignBundle fail; the export must
	// abort with ErrSigningFailed and never return a bundle.
	bridge := &fakeBridge{roundTripOK: true}
	e := NewExporter(nil, bridge, &Signer{})

	bundle, err := e.exportFromAggregate(context.Background(), minimalAggregate(), "tester")
	if !errors.Is(err, ErrSigningFailed) {
		t.Fatalf("expected ErrSigningFailed, got %v", err)
	}
	if bundle != nil {
		t.Fatal("no bundle must be returned when signing fails")
	}
}

func TestExportRejectsNilAuditPeriodID(t *testing.T) {
	e := NewExporter(nil, &fakeBridge{roundTripOK: true}, &Signer{})
	if _, err := e.Export(context.Background(), ExportInput{}); err == nil {
		t.Fatal("Export must reject a nil audit_period_id")
	}
}
