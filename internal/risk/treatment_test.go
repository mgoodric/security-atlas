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
				AcceptanceStatus:     dbx.RiskAcceptanceStatusApproved,
				AcceptedUntilPresent: true,
				Accepter:             "ciso@example.com",
			},
			wantErr: false,
		},
		{
			name: "accept missing accepted_until",
			in: risk.TreatmentInput{
				Treatment:        dbx.RiskTreatmentAccept,
				AcceptanceStatus: dbx.RiskAcceptanceStatusApproved,
				Accepter:         "ciso@example.com",
			},
			wantErr: true,
		},
		{
			name: "accept missing accepter",
			in: risk.TreatmentInput{
				Treatment:            dbx.RiskTreatmentAccept,
				AcceptanceStatus:     dbx.RiskAcceptanceStatusApproved,
				AcceptedUntilPresent: true,
			},
			wantErr: true,
		},
		// ---- accept acceptance status (#1746) ----
		{
			name: "proposed accept with no accepter or expiry ok",
			in: risk.TreatmentInput{
				Treatment:        dbx.RiskTreatmentAccept,
				AcceptanceStatus: dbx.RiskAcceptanceStatusProposed,
			},
			wantErr: false,
		},
		{
			name: "accept without acceptance status fails",
			in: risk.TreatmentInput{
				Treatment:            dbx.RiskTreatmentAccept,
				AcceptedUntilPresent: true,
				Accepter:             "ciso@example.com",
			},
			wantErr: true,
		},
		{
			name: "unknown acceptance status fails",
			in: risk.TreatmentInput{
				Treatment:        dbx.RiskTreatmentAccept,
				AcceptanceStatus: "maybe",
			},
			wantErr: true,
		},
		{
			name: "acceptance status on a non-accept treatment fails",
			in: risk.TreatmentInput{
				Treatment:        dbx.RiskTreatmentUntreated,
				AcceptanceStatus: dbx.RiskAcceptanceStatusProposed,
			},
			wantErr: true,
		},
		// ---- untreated (#1746) ----
		{
			name:    "untreated needs no fields",
			in:      risk.TreatmentInput{Treatment: dbx.RiskTreatmentUntreated},
			wantErr: false,
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
			name: "mitigate with a linked action plan and no controls ok",
			in: risk.TreatmentInput{
				Treatment:             dbx.RiskTreatmentMitigate,
				LinkedActionPlanCount: 1,
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

// TestIsOpenTreatment pins the open-exposure rule (#1746): untreated and
// proposed acceptances stay open; only an approved acceptance drops out.
func TestIsOpenTreatment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		treatment dbx.RiskTreatment
		status    dbx.RiskAcceptanceStatus
		want      bool
	}{
		{dbx.RiskTreatmentUntreated, "", true},
		{dbx.RiskTreatmentAccept, dbx.RiskAcceptanceStatusProposed, true},
		{dbx.RiskTreatmentAccept, dbx.RiskAcceptanceStatusApproved, false},
		{dbx.RiskTreatmentMitigate, "", true},
		{dbx.RiskTreatmentTransfer, "", true},
		{dbx.RiskTreatmentAvoid, "", true},
	}
	for _, c := range cases {
		if got := risk.IsOpenTreatment(c.treatment, c.status); got != c.want {
			t.Errorf("IsOpenTreatment(%q, %q) = %v, want %v", c.treatment, c.status, got, c.want)
		}
	}
}

func TestTreatmentLabel(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		risk.TreatmentLabel(dbx.RiskTreatmentAccept, dbx.RiskAcceptanceStatusProposed): "accept (proposed)",
		risk.TreatmentLabel(dbx.RiskTreatmentAccept, dbx.RiskAcceptanceStatusApproved): "accept (approved)",
		risk.TreatmentLabel(dbx.RiskTreatmentUntreated, ""):                            "untreated",
		risk.TreatmentLabel(dbx.RiskTreatmentAvoid, ""):                                "avoid",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("TreatmentLabel = %q, want %q", got, want)
		}
	}
}
