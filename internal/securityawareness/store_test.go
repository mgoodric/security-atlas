package securityawareness

import (
	"testing"
	"time"

	"github.com/google/uuid"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

func TestBuildCompletionEvidence_MapsAwarenessControlsAndMetric(t *testing.T) {
	completed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	source := CompletionSourceManual
	outcome := PhishingReported
	detail := AssignmentDetail{
		Assignment: Assignment{
			ID:               uuid.New(),
			CampaignID:       uuid.New(),
			PersonID:         uuid.New(),
			CompletedAt:      &completed,
			CompletionSource: &source,
		},
		CourseCode:      "SAT-ANNUAL",
		CourseTitle:     "Annual security awareness",
		CampaignName:    "2026 annual awareness",
		PhishingOutcome: &outcome,
	}

	rec, err := BuildCompletionEvidence(detail, "operator-1")
	if err != nil {
		t.Fatalf("BuildCompletionEvidence: %v", err)
	}
	if rec.GetEvidenceKind() != EvidenceKindTrainingCompletion {
		t.Fatalf("evidence kind = %q, want %q", rec.GetEvidenceKind(), EvidenceKindTrainingCompletion)
	}
	if rec.GetSchemaVersion() != EvidenceVersionTraining {
		t.Fatalf("schema version = %q", rec.GetSchemaVersion())
	}
	if rec.GetControlId() != "soc2_cc1_4_competence_training" {
		t.Fatalf("control id = %q", rec.GetControlId())
	}
	if rec.GetResult() != evidencev1.Result_RESULT_PASS {
		t.Fatalf("result = %s", rec.GetResult())
	}
	scope := map[string][]string{}
	for _, dim := range rec.GetScope() {
		scope[dim.GetKey()] = dim.GetValues()
	}
	if !contains(scope["framework_controls"], "SOC2:CC1.4") || !contains(scope["framework_controls"], "ISO27001:A.6.3") {
		t.Fatalf("framework scope missing awareness controls: %+v", scope)
	}
	if !contains(scope["metric"], MetricTrainingCompletionRate) {
		t.Fatalf("metric scope missing %q: %+v", MetricTrainingCompletionRate, scope)
	}
	payload := rec.GetPayload().AsMap()
	if payload["completion_source"] != CompletionSourceManual || payload["phishing_outcome"] != PhishingReported {
		t.Fatalf("payload mismatch: %+v", payload)
	}
}

func TestBuildCompletionEvidence_RejectsIncompleteAssignment(t *testing.T) {
	_, err := BuildCompletionEvidence(AssignmentDetail{Assignment: Assignment{ID: uuid.New()}}, "operator")
	if err == nil {
		t.Fatal("BuildCompletionEvidence succeeded for incomplete assignment")
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
