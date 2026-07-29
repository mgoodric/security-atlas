package questionnaire

import (
	"fmt"
	"time"
)

const ExportExclusionSummaryFormat = "%d drafted answers pending approval were excluded"

// AnswerClass is the export-facing approval classification derived from the
// slice-441 approval columns.
type AnswerClass string

const (
	AnswerClassManual          AnswerClass = "manual"
	AnswerClassApprovedAI      AnswerClass = "approved_ai"
	AnswerClassUnapprovedDraft AnswerClass = "unapproved_draft"
)

type ExportCounts struct {
	Manual          int `json:"manual"`
	ApprovedAI      int `json:"approved_ai"`
	ExcludedDrafts  int `json:"excluded_drafts"`
	ExportedAnswers int `json:"exported_answers"`
}

type ExportBuildResult struct {
	Input  PDFInput
	Counts ExportCounts
}

// BuildExportPDFInput is the shared export hydration gate. It is the only
// questionnaire answer classification step: manual answers and approved AI
// answers pass through unchanged; unapproved AI drafts become unanswered rows.
func BuildExportPDFInput(q *Questionnaire, questions []Question, generatedAt time.Time) ExportBuildResult {
	in := PDFInput{
		GeneratedAt: generatedAt.UTC().Format(time.RFC3339),
		Items:       make([]PDFItem, 0, len(questions)),
	}
	if q != nil {
		in.QuestionnaireName = q.Name
		in.SourceLabel = q.SourceLabel
	}

	var counts ExportCounts
	for _, it := range questions {
		pi := PDFItem{
			Code:         it.Code,
			Text:         it.Text,
			Domain:       it.Domain,
			ScfAnchorID:  it.ScfAnchorID,
			NeedsMapping: it.NeedsMapping,
		}
		if it.Answer != nil {
			switch ClassifyAnswer(*it.Answer) {
			case AnswerClassManual:
				counts.Manual++
				counts.ExportedAnswers++
				pi.AnswerValue = it.Answer.AnswerValue
				pi.Narrative = it.Answer.Narrative
			case AnswerClassApprovedAI:
				counts.ApprovedAI++
				counts.ExportedAnswers++
				pi.AnswerValue = it.Answer.AnswerValue
				pi.Narrative = it.Answer.Narrative
			case AnswerClassUnapprovedDraft:
				counts.ExcludedDrafts++
			}
		}
		in.Items = append(in.Items, pi)
	}
	return ExportBuildResult{Input: in, Counts: counts}
}

func ClassifyAnswer(a Answer) AnswerClass {
	if !a.AIAssisted {
		return AnswerClassManual
	}
	if a.HumanApproved {
		return AnswerClassApprovedAI
	}
	return AnswerClassUnapprovedDraft
}

func ExportExclusionSummary(count int) string {
	return fmt.Sprintf(ExportExclusionSummaryFormat, count)
}
