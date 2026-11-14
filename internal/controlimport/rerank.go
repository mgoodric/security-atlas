package controlimport

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// Row LLM rerank states (#1781). A row is off when no LLM was asked (none
// configured, or the organisation's cloud-LLM opt-in is off), applied when
// the LLM reordered its candidates and explained them, and unavailable when
// the LLM was asked but its answer could not be used: the deterministic
// order stands and no rationale is shown.
const (
	RerankOff         = "off"
	RerankApplied     = "applied"
	RerankUnavailable = "unavailable"
)

const (
	rerankPromptVersion = "controlimport-rerank-v0"
	rerankMaxTokens     = 1024
	rerankTimeout       = 30 * time.Second
	maxRationaleRunes   = 400

	// rerankAsyncThreshold is the staged-row count above which a batch
	// whose rows will be reranked computes its candidates in the
	// background: one LLM call per row is too slow to hold the upload open.
	rerankAsyncThreshold = 25
)

// RerankGate reports whether the caller's organisation lets the importer
// call an LLM. Production wires the tenant's cloud-LLM routing config: only
// a tenant that has opted in to a cloud provider is reranked.
type RerankGate func(ctx context.Context) (bool, error)

// GenerationRecorder persists the forensic record of one generation
// (llm.AuditWriter in production).
type GenerationRecorder interface {
	Write(ctx context.Context, g llm.Generation) error
}

// Reranker asks an LLM to reorder a row's deterministic candidates and
// explain each placement. It can only reorder: an anchor the LLM names that
// is not in the row's list is ignored, and a candidate it omits keeps its
// deterministic place after the ones it ranked.
type Reranker struct {
	client   llm.Client
	recorder GenerationRecorder
	gate     RerankGate
}

func NewReranker(client llm.Client, recorder GenerationRecorder, gate RerankGate) *Reranker {
	return &Reranker{client: client, recorder: recorder, gate: gate}
}

// WithReranker enables the optional LLM rerank on staged candidates.
func (s *Stager) WithReranker(r *Reranker) *Stager {
	s.reranker = r
	return s
}

// allowed reports whether this batch's rows are reranked. A gate error is
// logged and read as "no": the deterministic candidates never depend on it.
func (r *Reranker) allowed(ctx context.Context) bool {
	if r == nil || r.client == nil || r.gate == nil {
		return false
	}
	ok, err := r.gate(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "controlimport: read cloud-LLM opt-in", "error", err)
		return false
	}
	return ok
}

// rerankAll reranks every row with candidates. After the first failed
// generation the remaining rows are marked unavailable without a call, so an
// outage costs one timeout per batch, not one per row.
func (r *Reranker) rerankAll(ctx context.Context, staged []BatchControl, rows []RowSuggestions) {
	byID := make(map[string]BatchControl, len(staged))
	for _, c := range staged {
		byID[c.ID] = c
	}
	down := false
	for i := range rows {
		if len(rows[i].Candidates) == 0 {
			continue
		}
		if down {
			rows[i].LLMRerank = RerankUnavailable
			continue
		}
		var err error
		rows[i], err = r.rerankRow(ctx, byID[rows[i].RowID], rows[i])
		if err != nil {
			slog.WarnContext(ctx, "controlimport: LLM rerank unavailable", "row_id", rows[i].RowID, "error", err)
			rows[i].LLMRerank = RerankUnavailable
			down = true
		}
	}
}

// rerankRow returns the row reranked, or the row unchanged with
// RerankUnavailable when the answer can't be used (malformed, or naming no
// listed anchor). An error means the LLM itself failed.
func (r *Reranker) rerankRow(ctx context.Context, row BatchControl, sug RowSuggestions) (RowSuggestions, error) {
	req := llm.GenerateRequest{
		Surface:       llm.SurfaceControlImport,
		PromptVersion: rerankPromptVersion,
		SystemPrompt:  buildRerankPrompt(row, sug.Candidates),
		Context:       rerankContext(row, sug.Candidates),
		MaxTokens:     rerankMaxTokens,
		Timeout:       rerankTimeout,
	}
	res, err := r.client.Generate(ctx, req)
	if err != nil {
		return sug, err
	}
	if err := r.recorder.Write(ctx, llm.Generation{
		Surface:        req.Surface,
		PromptVersion:  req.PromptVersion,
		ModelName:      res.ModelName,
		ModelVersion:   res.ModelVersion,
		ModelProvider:  res.ModelProvider,
		SystemPrompt:   req.SystemPrompt,
		ContextInputs:  req.Context,
		RawDraft:       res.Text,
		SurfaceSubject: row.ID,
	}); err != nil {
		// An unrecorded draft is never shown.
		return sug, fmt.Errorf("record generation: %w", err)
	}
	out, ok := applyRerank(sug.Candidates, res.Text)
	if !ok {
		sug.LLMRerank = RerankUnavailable
		return sug, nil
	}
	sug.Candidates = out
	sug.LLMRerank = RerankApplied
	return sug, nil
}

type rerankAnswer struct {
	Ranking []struct {
		SCFID     string `json:"scf_id"`
		Rationale string `json:"rationale"`
	} `json:"ranking"`
}

// applyRerank orders cands as the model's answer lists them, attaching each
// rationale. Unknown or repeated anchors are skipped; candidates the answer
// omits follow in their deterministic order without a rationale. The anchor
// set is always the input set. ok is false when the answer is malformed or
// names no listed anchor, and cands is then returned unchanged.
func applyRerank(cands []Candidate, text string) ([]Candidate, bool) {
	var ans rerankAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &ans); err != nil {
		return cands, false
	}
	bySCF := make(map[string]int, len(cands))
	for i, c := range cands {
		bySCF[strings.ToUpper(c.SCFID)] = i
	}
	placed := make([]bool, len(cands))
	out := make([]Candidate, 0, len(cands))
	for _, item := range ans.Ranking {
		i, ok := bySCF[strings.ToUpper(strings.TrimSpace(item.SCFID))]
		if !ok || placed[i] {
			continue
		}
		placed[i] = true
		c := cands[i]
		c.AIAssisted = true
		c.Rationale = clipRationale(item.Rationale)
		out = append(out, c)
	}
	if len(out) == 0 {
		return cands, false
	}
	for i, c := range cands {
		if !placed[i] {
			out = append(out, c)
		}
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, true
}

func clipRationale(s string) string {
	s = oneLine(s)
	if r := []rune(s); len(r) > maxRationaleRunes {
		s = string(r[:maxRationaleRunes])
	}
	return s
}

func buildRerankPrompt(row BatchControl, cands []Candidate) string {
	var b strings.Builder
	b.WriteString(`You rerank Secure Controls Framework (SCF) anchor candidates for ONE imported control and explain each placement in one short sentence. The result is a suggestion: a human reviewer chooses the anchor.

Rules:
1. Use only the scf_id values in the candidate list. Do not invent SCF ids.
2. Order the candidates from best to worst fit for the imported control.
3. Each rationale is one factual sentence naming why the anchor fits or does not.
4. Return only compact JSON: {"ranking":[{"scf_id":"IAC-06","rationale":"..."}]}.

Imported control:
`)
	fmt.Fprintf(&b, "id: %s\n", oneLine(row.ExternalID))
	fmt.Fprintf(&b, "title: %s\n", oneLine(row.Title))
	fmt.Fprintf(&b, "description: %s\n", oneLine(row.Description))
	if len(row.TestedUnder) > 0 {
		codes := make([]string, len(row.TestedUnder))
		for i, r := range row.TestedUnder {
			codes[i] = r.Code
		}
		fmt.Fprintf(&b, "tested under: %s\n", strings.Join(codes, ", "))
	}
	b.WriteString("\nCandidates (current order):\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "- %s: %s (%s", c.SCFID, oneLine(c.Title), c.Source)
		if fit := c.Fit(); fit != "" {
			fmt.Fprintf(&b, "; %s", fit)
		}
		b.WriteString(")\n")
	}
	return b.String()
}

func rerankContext(row BatchControl, cands []Candidate) map[string]any {
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.SCFID
	}
	return map[string]any{
		"import_row_id":       row.ID,
		"imported_control_id": row.ExternalID,
		"candidate_scf_ids":   ids,
	}
}

// AuditRecorder adapts the shared ai_generations writer to
// GenerationRecorder.
func AuditRecorder(w *llm.AuditWriter) GenerationRecorder { return auditRecorder{w} }

type auditRecorder struct{ w *llm.AuditWriter }

func (a auditRecorder) Write(ctx context.Context, g llm.Generation) error {
	_, err := a.w.Write(ctx, g)
	return err
}
