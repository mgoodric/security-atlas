package qmapsuggest

import (
	"fmt"
	"strings"
)

const systemPrompt = `You choose ONE Secure Controls Framework anchor for ONE unmapped security-questionnaire question. Your choice will be reviewed by a human operator and is NOT canonical until that operator approves it.

Rules you must follow:
1. Choose ONLY from the candidate SCF anchors below.
2. Return JSON only, with this exact shape: {"scf_id":"IAC-06","rationale":"one short sentence"}.
3. Do not invent SCF ids. The scf_id must exactly match a candidate scf_id.
4. Keep rationale to one sentence and base it only on the question text and candidate excerpts.`

func buildPrompt(questionText string, cands []Candidate) string {
	var b strings.Builder
	b.WriteString("Question:\n")
	b.WriteString(questionText)
	b.WriteString("\n\nCandidate SCF anchors (choose exactly one scf_id from this list):\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "  - scf_id %s: %s\n", c.SCFID, oneLine(c.Title))
		if c.Excerpt != "" {
			fmt.Fprintf(&b, "      excerpt: %s\n", oneLine(c.Excerpt))
		}
	}
	return b.String()
}

func candidateContext(questionText string, cands []Candidate) map[string]any {
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.SCFID)
	}
	return map[string]any{
		"question_text":        questionText,
		"candidate_anchor_ids": ids,
	}
}
