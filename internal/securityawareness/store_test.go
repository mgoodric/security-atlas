package securityawareness

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
)

func completedDetail(completed time.Time) AssignmentDetail {
	source := CompletionSourceManual
	outcome := PhishingReported
	return AssignmentDetail{
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
}

func TestBuildCompletionEvidence_MapsAwarenessControlsAndMetric(t *testing.T) {
	completed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	recorded := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	detail := completedDetail(completed)

	rec, err := BuildCompletionEvidence(detail, "operator-1", recorded)
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
	// #2941: observed_at is when the platform recorded the completion, so a
	// back-dated completion passes ingest's skew check; the completion's own
	// instant is the payload's completed_at, which cc1_4 reads.
	if got := rec.GetObservedAt().AsTime(); !got.Equal(recorded) {
		t.Fatalf("observed_at = %s, want the recording instant %s", got, recorded)
	}
	if got := payload["completed_at"]; got != completed.Format(time.RFC3339Nano) {
		t.Fatalf("payload completed_at = %v, want %s", got, completed.Format(time.RFC3339Nano))
	}
}

func TestBuildCompletionEvidence_RejectsIncompleteAssignment(t *testing.T) {
	_, err := BuildCompletionEvidence(AssignmentDetail{Assignment: Assignment{ID: uuid.New()}}, "operator", time.Now())
	if err == nil {
		t.Fatal("BuildCompletionEvidence succeeded for incomplete assignment")
	}
	if _, err := BuildCompletionEvidence(completedDetail(time.Now()), "operator", time.Time{}); err == nil {
		t.Fatal("BuildCompletionEvidence succeeded without an observed_at")
	}
}

// The record the platform writes conforms to the registered platform schema,
// so ingest's schema validation accepts it (#2941).
func TestBuildCompletionEvidence_ConformsToPlatformSchema(t *testing.T) {
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	for _, withPhish := range []bool{true, false} {
		detail := completedDetail(time.Date(2025, 12, 1, 8, 30, 0, 123456000, time.UTC))
		if !withPhish {
			detail.PhishingOutcome = nil
		}
		rec, err := BuildCompletionEvidence(detail, "operator-1", time.Now())
		if err != nil {
			t.Fatalf("BuildCompletionEvidence: %v", err)
		}
		raw, err := protojson.Marshal(rec.GetPayload())
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := reg.ValidatePayload(context.Background(), "11111111-1111-1111-1111-111111111111",
			rec.GetEvidenceKind(), rec.GetSchemaVersion(), raw); err != nil {
			t.Fatalf("phishing=%v: payload rejected by schema: %v\n%s", withPhish, err, raw)
		}
	}
}

func TestTrainingEvidenceKey_DistinguishesInstantsOnOneDay(t *testing.T) {
	id := uuid.New()
	morning := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	if trainingEvidenceKey(id, morning) == trainingEvidenceKey(id, morning.Add(time.Hour)) {
		t.Fatal("two completions on one day share an idempotency key")
	}
	if trainingEvidenceKey(id, morning) != trainingEvidenceKey(id, morning.In(time.FixedZone("x", 3600))) {
		t.Fatal("one instant in two zones got two keys")
	}
}

func TestCompletedInFuture_AllowsOneHourOfSkew(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		at   time.Time
		want bool
	}{
		{now.Add(-400 * 24 * time.Hour), false},
		{now, false},
		{now.Add(MaxCompletedAtLead), false},
		{now.Add(MaxCompletedAtLead + time.Second), true},
		{time.Date(2027, 11, 22, 12, 0, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		if got := completedInFuture(tc.at, now); got != tc.want {
			t.Errorf("completedInFuture(%s) = %v, want %v", tc.at, got, tc.want)
		}
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
