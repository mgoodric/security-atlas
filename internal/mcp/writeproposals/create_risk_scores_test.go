package writeproposals

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// #2502: create_risk scores take the rule POST /v1/risks applies
// (risk.PrepareCreateScores), with the same error, when the proposal is
// filed and when it is confirmed. FAIR keeps its own validation.
func TestCreateRiskScores_SameRuleAsREST(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		methodology string
		score       string
		want        error
	}{
		{"nist impact string", "nist_800_30", `{"likelihood":4,"impact":"5"}`, risk.ErrInherentScoreInvalid},
		{"nist impact out of range", "nist_800_30", `{"likelihood":4,"impact":6}`, risk.ErrInherentScoreInvalid},
		{"nist severity string", "nist_800_30", `{"likelihood":4,"impact":5,"severity":"20"}`, risk.ErrScoreNotNumeric},
		{"nist severity out of range", "", `{"likelihood":4,"impact":5,"severity":99}`, risk.ErrScoreNotNumeric},
		{"5x5 malformed", "qualitative_5x5", `{"note":"high"}`, risk.ErrInherentScoreInvalid},
		{"cis_ram likelihood string", "cis_ram", `{"likelihood":"4","impact":5}`, risk.ErrScoreNotNumeric},
		{"score not an object", "nist_800_30", `"high"`, risk.ErrInherentScoreInvalid},
		{"unknown methodology", "octave", `{"likelihood":4,"impact":5}`, risk.ErrInvalidMethodology},
		{"fair malformed", "fair", `{"lef":"x","lm":1}`, risk.ErrInherentScoreInvalid},
		{"nist valid", "nist_800_30", `{"likelihood":4,"impact":5}`, nil},
		{"fair valid", "fair", `{"loss_event_frequency":2,"loss_magnitude":1000}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := CreateRiskInput{Title: "t", Category: "c", Methodology: tc.methodology, InherentScore: json.RawMessage(tc.score)}
			_, _, err := createRiskScores(in)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("createRiskScores = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("createRiskScores = %v, want %v", err, tc.want)
			}
			_, _, restErr := risk.PrepareCreateScores(createRiskMethodology(in), in.InherentScore, nil)
			if restErr == nil || restErr.Error() != err.Error() {
				t.Fatalf("MCP error %q differs from REST's %q", err, restErr)
			}
		})
	}
}

// FAIR is stored as REST stores it: normalised, with the residual taking
// the inherent score.
func TestCreateRiskScores_FairNormalisedAsREST(t *testing.T) {
	t.Parallel()
	in := CreateRiskInput{Methodology: string(dbx.RiskMethodologyFair), InherentScore: json.RawMessage(`{"lef":2,"lm":1000}`)}
	inherent, residual, err := createRiskScores(in)
	if err != nil {
		t.Fatalf("createRiskScores: %v", err)
	}
	restInherent, restResidual, err := risk.PrepareCreateScores(dbx.RiskMethodologyFair, in.InherentScore, nil)
	if err != nil {
		t.Fatalf("PrepareCreateScores: %v", err)
	}
	if string(inherent) != string(restInherent) || string(residual) != string(restResidual) {
		t.Fatalf("MCP stores %s / %s, REST %s / %s", inherent, residual, restInherent, restResidual)
	}
	if !strings.Contains(string(inherent), `"annualized_loss_exposure":2000`) {
		t.Fatalf("inherent = %s, want the canonical FAIR shape", inherent)
	}
}

// An omitted score files an unscored risk, as create_risk always has; the
// methodology is still checked.
func TestCreateRiskScores_OmittedScore(t *testing.T) {
	t.Parallel()
	inherent, residual, err := createRiskScores(CreateRiskInput{})
	if err != nil || string(inherent) != `{}` || string(residual) != `{}` {
		t.Fatalf("createRiskScores(no score) = %s, %s, %v; want {}, {}, nil", inherent, residual, err)
	}
	if _, _, err := createRiskScores(CreateRiskInput{Methodology: "octave"}); !errors.Is(err, risk.ErrInvalidMethodology) {
		t.Fatalf("createRiskScores(unknown methodology) = %v, want ErrInvalidMethodology", err)
	}
}

// The MCP tool call is refused when the proposal is filed, before any
// database work, so the assistant sees the error.
func TestStoreCreate_RefusesBadCreateRiskScoreBeforeTx(t *testing.T) {
	t.Parallel()
	s := NewStore(nil)
	_, err := s.Create(context.Background(), CreateInput{
		ToolName:       ToolCreateRisk,
		ToolInput:      json.RawMessage(`{"title":"t","category":"c","inherent_score":{"likelihood":4,"impact":5,"severity":"20"}}`),
		AIModelName:    "m",
		AIModelVersion: "v",
		CreatedBy:      "k",
	})
	if !errors.Is(err, risk.ErrScoreNotNumeric) {
		t.Fatalf("Create(string severity) = %v, want ErrScoreNotNumeric", err)
	}
}

// The confirm path refuses too: ApplyCreateRisk checks the score before
// it touches the transaction.
func TestApplyCreateRisk_RefusesBadScore(t *testing.T) {
	t.Parallel()
	p := Proposal{
		ToolName:  ToolCreateRisk,
		ToolInput: json.RawMessage(`{"title":"t","category":"c","inherent_score":{"likelihood":4,"impact":5,"severity":99}}`),
	}
	if _, err := ApplyCreateRisk(context.Background(), nil, p); !errors.Is(err, risk.ErrScoreNotNumeric) {
		t.Fatalf("ApplyCreateRisk(out-of-range severity) = %v, want ErrScoreNotNumeric", err)
	}
}

func TestValidateToolInput_OtherToolsAndUndecodable(t *testing.T) {
	t.Parallel()
	if err := validateToolInput(ToolPushEvidence, json.RawMessage(`{"inherent_score":"x"}`)); err != nil {
		t.Fatalf("validateToolInput(push_evidence) = %v, want nil", err)
	}
	// #2511: undecodable create_risk input is refused when it is filed.
	if err := validateToolInput(ToolCreateRisk, json.RawMessage(`{"title":5}`)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("validateToolInput(undecodable) = %v, want ErrInvalidInput", err)
	}
}

// #2511: MCP and REST store the same row for an unscored risk.
func TestCreateRiskScores_UnscoredMatchesREST(t *testing.T) {
	t.Parallel()
	for _, score := range []string{"", "null", "{}"} {
		in := CreateRiskInput{InherentScore: json.RawMessage(score)}
		inherent, residual, err := createRiskScores(in)
		if err != nil {
			t.Fatalf("createRiskScores(%q): %v", score, err)
		}
		restInherent, restResidual, err := risk.PrepareCreateScores(createRiskMethodology(in), in.InherentScore, nil)
		if err != nil {
			t.Fatalf("PrepareCreateScores(%q): %v", score, err)
		}
		if string(inherent) != string(restInherent) || string(residual) != string(restResidual) {
			t.Fatalf("%q: MCP stores %s / %s, REST %s / %s", score, inherent, residual, restInherent, restResidual)
		}
	}
}
