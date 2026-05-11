package risk_test

import (
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// TestValidateInherentScore exercises every methodology branch and the obvious
// invalid shapes. It runs without a DB — pure validation logic.
//
// Covers AC-2: methodology-specific inherent_score validation. Each row in the
// table maps to one or more ISC-22..ISC-25 criteria.
func TestValidateInherentScore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		methodology dbx.RiskMethodology
		body        string
		wantErr     error
	}{
		// ---- nist_800_30 ----
		{
			name:        "nist valid 5x5",
			methodology: dbx.RiskMethodologyNist80030,
			body:        `{"likelihood":3,"impact":4}`,
			wantErr:     nil,
		},
		{
			name:        "nist missing likelihood",
			methodology: dbx.RiskMethodologyNist80030,
			body:        `{"impact":3}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		{
			name:        "nist impact out of range high",
			methodology: dbx.RiskMethodologyNist80030,
			body:        `{"likelihood":3,"impact":7}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		{
			name:        "nist impact out of range zero",
			methodology: dbx.RiskMethodologyNist80030,
			body:        `{"likelihood":3,"impact":0}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		// ---- fair ----
		{
			name:        "fair valid",
			methodology: dbx.RiskMethodologyFair,
			body:        `{"lef":1.2,"lm":50000}`,
			wantErr:     nil,
		},
		{
			name:        "fair missing lef",
			methodology: dbx.RiskMethodologyFair,
			body:        `{"lm":50000}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		{
			name:        "fair missing lm",
			methodology: dbx.RiskMethodologyFair,
			body:        `{"lef":1.2}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		// ---- qualitative_5x5 ----
		{
			name:        "qualitative valid",
			methodology: dbx.RiskMethodologyQualitative5x5,
			body:        `{"likelihood":2,"impact":2}`,
			wantErr:     nil,
		},
		{
			name:        "qualitative likelihood out of range",
			methodology: dbx.RiskMethodologyQualitative5x5,
			body:        `{"likelihood":9,"impact":3}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		// ---- cis_ram + iso_27005 (permissive in v1) ----
		{
			name:        "cis_ram open object accepted",
			methodology: dbx.RiskMethodologyCisRam,
			body:        `{"placeholder":1}`,
			wantErr:     nil,
		},
		{
			name:        "iso_27005 empty object rejected",
			methodology: dbx.RiskMethodologyIso27005,
			body:        `{}`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		// ---- methodology not in set ----
		{
			name:        "unknown methodology",
			methodology: dbx.RiskMethodology("monte_carlo"),
			body:        `{"foo":1}`,
			wantErr:     risk.ErrInvalidMethodology,
		},
		// ---- empty body ----
		{
			name:        "empty body",
			methodology: dbx.RiskMethodologyNist80030,
			body:        ``,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
		// ---- malformed JSON ----
		{
			name:        "malformed json",
			methodology: dbx.RiskMethodologyNist80030,
			body:        `{`,
			wantErr:     risk.ErrInherentScoreInvalid,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := risk.ValidateInherentScore(tc.methodology, []byte(tc.body))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestDefaultMethodologyIsNist80030(t *testing.T) {
	t.Parallel()
	if risk.DefaultMethodology != dbx.RiskMethodologyNist80030 {
		t.Fatalf("expected default methodology nist_800_30, got %q", risk.DefaultMethodology)
	}
}

func TestAllowedMethodologiesCount(t *testing.T) {
	t.Parallel()
	got := risk.AllowedMethodologies()
	if len(got) != 5 {
		t.Fatalf("expected 5 allowed methodologies, got %d (%v)", len(got), got)
	}
}
