package risk_test

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// TestValidateTreatment covers AC-3..AC-5 + the implicit "avoid" branch.
// Each subtest maps to an ISC in the AC-3/4/5 group.
func TestValidateTreatment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      risk.TreatmentInput
		wantErr bool
	}{
		// ---- accept (AC-4) ----
		{
			name: "accept with both fields ok",
			in: risk.TreatmentInput{
				Treatment:            dbx.RiskTreatmentAccept,
				AcceptedUntilPresent: true,
				Accepter:             "ciso@example.com",
			},
			wantErr: false,
		},
		{
			name: "accept missing accepted_until",
			in: risk.TreatmentInput{
				Treatment: dbx.RiskTreatmentAccept,
				Accepter:  "ciso@example.com",
			},
			wantErr: true,
		},
		{
			name: "accept missing accepter",
			in: risk.TreatmentInput{
				Treatment:            dbx.RiskTreatmentAccept,
				AcceptedUntilPresent: true,
			},
			wantErr: true,
		},
		// ---- mitigate (AC-3) ----
		{
			name: "mitigate with one linked control ok",
			in: risk.TreatmentInput{
				Treatment:          dbx.RiskTreatmentMitigate,
				LinkedControlCount: 1,
			},
			wantErr: false,
		},
		{
			name: "mitigate with zero linked controls fails",
			in: risk.TreatmentInput{
				Treatment:          dbx.RiskTreatmentMitigate,
				LinkedControlCount: 0,
			},
			wantErr: true,
		},
		// ---- transfer (AC-5) ----
		{
			name: "transfer with instrument_reference ok",
			in: risk.TreatmentInput{
				Treatment:           dbx.RiskTreatmentTransfer,
				InstrumentReference: "Policy #ACME-2026-CYBER",
			},
			wantErr: false,
		},
		{
			name: "transfer missing instrument_reference fails",
			in: risk.TreatmentInput{
				Treatment: dbx.RiskTreatmentTransfer,
			},
			wantErr: true,
		},
		// ---- avoid (no extra fields) ----
		{
			name: "avoid has no extra requirements",
			in: risk.TreatmentInput{
				Treatment: dbx.RiskTreatmentAvoid,
			},
			wantErr: false,
		},
		// ---- unknown ----
		{
			name: "unknown treatment fails",
			in: risk.TreatmentInput{
				Treatment: dbx.RiskTreatment("rolls-the-dice"),
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := risk.ValidateTreatment(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, risk.ErrTreatmentValidation) {
					t.Fatalf("expected ErrTreatmentValidation, got %v", err)
				}
				if !risk.IsTreatmentValidation(err) {
					t.Fatalf("IsTreatmentValidation returned false for %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}
