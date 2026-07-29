package questionnaire

import (
	"strings"
	"testing"
	"time"
)

func TestBuildExportPDFInput_ClassifiesAndExcludesUnapprovedDraft(t *testing.T) {
	generatedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	draftNarrative := "UNAPPROVED draft narrative must not leave the system"
	got := BuildExportPDFInput(&Questionnaire{Name: "SIG", SourceLabel: "customer"}, []Question{
		{
			ID:   "q-manual",
			Code: "M-1",
			Text: "Manual?",
			Answer: &Answer{
				AnswerValue: "yes",
				Narrative:   "Manual answer exports.",
			},
		},
		{
			ID:   "q-approved",
			Code: "A-1",
			Text: "Approved AI?",
			Answer: &Answer{
				AnswerValue:   "yes",
				Narrative:     "Approved AI answer exports.",
				AIAssisted:    true,
				HumanApproved: true,
			},
		},
		{
			ID:   "q-draft",
			Code: "D-1",
			Text: "Draft?",
			Answer: &Answer{
				AnswerValue: "yes",
				Narrative:   draftNarrative,
				AIAssisted:  true,
			},
		},
	}, generatedAt)

	if got.Counts.Manual != 1 || got.Counts.ApprovedAI != 1 || got.Counts.ExcludedDrafts != 1 {
		t.Fatalf("counts = %+v, want manual/approvedAI/excluded 1/1/1", got.Counts)
	}
	if got.Counts.ExportedAnswers != 2 {
		t.Fatalf("exported answers = %d, want 2", got.Counts.ExportedAnswers)
	}
	if got.Input.Items[2].AnswerValue != "" || got.Input.Items[2].Narrative != "" {
		t.Fatalf("unapproved draft reached PDF input: %+v", got.Input.Items[2])
	}
	htmlDoc := buildHTML(got.Input)
	if strings.Contains(htmlDoc, draftNarrative) {
		t.Fatal("unapproved draft narrative reached rendered HTML")
	}
	if !strings.Contains(htmlDoc, "(unanswered)") {
		t.Fatal("excluded draft question should render unanswered")
	}
}

func TestBuildExportPDFInput_ApprovedDraftReexportIncludesAnswer(t *testing.T) {
	generatedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	approvedNarrative := "Approved draft narrative may export."
	got := BuildExportPDFInput(&Questionnaire{Name: "SIG"}, []Question{
		{
			ID:   "q-approved",
			Code: "A-1",
			Text: "Approved?",
			Answer: &Answer{
				AnswerValue:   "yes",
				Narrative:     approvedNarrative,
				AIAssisted:    true,
				HumanApproved: true,
			},
		},
	}, generatedAt)

	if got.Counts.ApprovedAI != 1 || got.Counts.ExcludedDrafts != 0 {
		t.Fatalf("counts = %+v, want approvedAI=1 excluded=0", got.Counts)
	}
	if got.Input.Items[0].Narrative != approvedNarrative {
		t.Fatalf("approved narrative = %q, want %q", got.Input.Items[0].Narrative, approvedNarrative)
	}
}
