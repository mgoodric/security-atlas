package controlimport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/llm"
)

func threeCands() []Candidate {
	return []Candidate{
		{Rank: 1, AnchorID: "a", SCFID: "IAC-37", Title: "MFA", Source: SourceCrosswalk, Reaches: []Requirement{req61}, TestedCount: 1},
		{Rank: 2, AnchorID: "b", SCFID: "IAC-12", Title: "Review", Source: SourceCrosswalk},
		{Rank: 3, AnchorID: "c", SCFID: "MON-01", Title: "Monitoring", Source: SourceKeyword},
	}
}

func TestApplyRerankReordersWithinTheSet(t *testing.T) {
	t.Parallel()
	text := `{"ranking":[
		{"scf_id":"mon-01","rationale":"  watches   events "},
		{"scf_id":"ZZZ-99","rationale":"invented"},
		{"scf_id":"IAC-37","rationale":"mfa"},
		{"scf_id":"MON-01","rationale":"repeat"}]}`
	got, ok := applyRerank(threeCands(), text)
	if !ok {
		t.Fatal("applyRerank rejected a usable answer")
	}
	if s := sources(got); s != "MON-01/keyword IAC-37/crosswalk IAC-12/crosswalk" {
		t.Fatalf("order = %s", s)
	}
	for i, c := range got {
		if c.Rank != i+1 {
			t.Errorf("candidate %s rank = %d, want %d", c.SCFID, c.Rank, i+1)
		}
	}
	if got[0].Rationale != "watches events" || !got[0].AIAssisted {
		t.Errorf("first = %+v, want the trimmed rationale", got[0])
	}
	if got[2].AIAssisted || got[2].Rationale != "" {
		t.Errorf("an omitted candidate carries LLM output: %+v", got[2])
	}
}

func TestApplyRerankRejectsUnusableAnswers(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"malformed": `not json`,
		"empty":     `{"ranking":[]}`,
		"unknown":   `{"ranking":[{"scf_id":"ZZZ-99","rationale":"x"}]}`,
	} {
		got, ok := applyRerank(threeCands(), text)
		if ok || sources(got) != sources(threeCands()) {
			t.Errorf("%s: ok=%v order=%s, want the input unchanged", name, ok, sources(got))
		}
	}
}

func TestClipRationale(t *testing.T) {
	t.Parallel()
	if got := clipRationale(strings.Repeat("é", maxRationaleRunes+10)); len([]rune(got)) != maxRationaleRunes {
		t.Errorf("clipped length = %d runes", len([]rune(got)))
	}
}

type fnClient func(llm.GenerateRequest) (llm.GenerateResult, error)

func (f fnClient) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	if err := req.Validate(); err != nil {
		return llm.GenerateResult{}, err
	}
	return f(req)
}

type memRecorder struct {
	got []llm.Generation
	err error
}

func (m *memRecorder) Write(_ context.Context, g llm.Generation) error {
	m.got = append(m.got, g)
	return m.err
}

func stubResult(text string) llm.GenerateResult {
	return llm.GenerateResult{Text: text, ModelName: "m", ModelVersion: "1", ModelProvider: "anthropic"}
}

func rerankRows() ([]BatchControl, []RowSuggestions) {
	staged := []BatchControl{
		{ID: "r1", ExternalID: "ORG-1", Title: "MFA", TestedUnder: []Requirement{req61}},
		{ID: "r2", ExternalID: "ORG-2", Title: "Empty"},
		{ID: "r3", ExternalID: "ORG-3", Title: "Monitoring"},
	}
	rows := []RowSuggestions{
		{RowID: "r1", Candidates: threeCands(), LLMRerank: RerankOff},
		{RowID: "r2", LLMRerank: RerankOff},
		{RowID: "r3", Candidates: threeCands(), LLMRerank: RerankOff},
	}
	return staged, rows
}

func TestRerankAllRecordsEachGenerationUnderImportSurface(t *testing.T) {
	t.Parallel()
	var prompts []string
	client := fnClient(func(req llm.GenerateRequest) (llm.GenerateResult, error) {
		prompts = append(prompts, req.SystemPrompt)
		return stubResult(`{"ranking":[{"scf_id":"IAC-12","rationale":"r"}]}`), nil
	})
	rec := &memRecorder{}
	staged, rows := rerankRows()
	NewReranker(client, rec, nil).rerankAll(context.Background(), staged, rows)

	if len(prompts) != 2 {
		t.Fatalf("calls = %d, want 2 (the row without candidates is skipped)", len(prompts))
	}
	if !strings.Contains(prompts[0], "tested under: CC6.1") || !strings.Contains(prompts[0], "IAC-37: MFA (crosswalk; reaches 1 of 1: CC6.1)") {
		t.Errorf("prompt lacks the row's criteria or candidate fit:\n%s", prompts[0])
	}
	for _, g := range rec.got {
		if g.Surface != llm.SurfaceControlImport || g.PromptVersion != rerankPromptVersion {
			t.Errorf("recorded %s/%s, want control_import", g.Surface, g.PromptVersion)
		}
	}
	if rec.got[0].SurfaceSubject != "r1" || rec.got[1].SurfaceSubject != "r3" {
		t.Errorf("subjects = %s, %s", rec.got[0].SurfaceSubject, rec.got[1].SurfaceSubject)
	}
	if rows[0].LLMRerank != RerankApplied || rows[1].LLMRerank != RerankOff || rows[2].LLMRerank != RerankApplied {
		t.Errorf("states = %s %s %s", rows[0].LLMRerank, rows[1].LLMRerank, rows[2].LLMRerank)
	}
	if rows[0].Candidates[0].SCFID != "IAC-12" {
		t.Errorf("r1 top = %s, want IAC-12", rows[0].Candidates[0].SCFID)
	}
}

func TestRerankAllStopsCallingAfterAFailure(t *testing.T) {
	t.Parallel()
	calls := 0
	client := fnClient(func(llm.GenerateRequest) (llm.GenerateResult, error) {
		calls++
		return llm.GenerateResult{}, llm.ErrTimeout
	})
	rec := &memRecorder{}
	staged, rows := rerankRows()
	NewReranker(client, rec, nil).rerankAll(context.Background(), staged, rows)

	if calls != 1 {
		t.Errorf("calls = %d, want 1: an outage costs one call per batch", calls)
	}
	if len(rec.got) != 0 {
		t.Errorf("recorded %d generations for a failed call", len(rec.got))
	}
	for _, i := range []int{0, 2} {
		if rows[i].LLMRerank != RerankUnavailable || sources(rows[i].Candidates) != sources(threeCands()) {
			t.Errorf("row %s = %s %s, want unavailable with the deterministic order", rows[i].RowID, rows[i].LLMRerank, sources(rows[i].Candidates))
		}
	}
}

func TestRerankRowDiscardsUnrecordedOrUnusableDrafts(t *testing.T) {
	t.Parallel()
	staged, rows := rerankRows()
	good := fnClient(func(llm.GenerateRequest) (llm.GenerateResult, error) {
		return stubResult(`{"ranking":[{"scf_id":"IAC-12","rationale":"r"}]}`), nil
	})
	got, err := NewReranker(good, &memRecorder{err: errors.New("db down")}, nil).rerankRow(context.Background(), staged[0], rows[0])
	if err == nil || sources(got.Candidates) != sources(threeCands()) {
		t.Errorf("unrecorded draft: err=%v order=%s, want an error and the deterministic order", err, sources(got.Candidates))
	}

	bad := fnClient(func(llm.GenerateRequest) (llm.GenerateResult, error) { return stubResult("prose"), nil })
	got, err = NewReranker(bad, &memRecorder{}, nil).rerankRow(context.Background(), staged[0], rows[0])
	if err != nil || got.LLMRerank != RerankUnavailable || sources(got.Candidates) != sources(threeCands()) {
		t.Errorf("malformed draft: err=%v state=%s order=%s", err, got.LLMRerank, sources(got.Candidates))
	}
}

func TestRerankerAllowed(t *testing.T) {
	t.Parallel()
	client := fnClient(func(llm.GenerateRequest) (llm.GenerateResult, error) { return stubResult("{}"), nil })
	yes := func(context.Context) (bool, error) { return true, nil }
	cases := map[string]struct {
		r    *Reranker
		want bool
	}{
		"nil reranker": {nil, false},
		"no client":    {NewReranker(nil, &memRecorder{}, yes), false},
		"no gate":      {NewReranker(client, &memRecorder{}, nil), false},
		"opted out":    {NewReranker(client, &memRecorder{}, func(context.Context) (bool, error) { return false, nil }), false},
		"gate error":   {NewReranker(client, &memRecorder{}, func(context.Context) (bool, error) { return true, errors.New("db") }), false},
		"opted in":     {NewReranker(client, &memRecorder{}, yes), true},
	}
	for name, tc := range cases {
		if got := tc.r.allowed(context.Background()); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", name, got, tc.want)
		}
	}
}

func TestStageReranksWhenOptedIn(t *testing.T) {
	t.Parallel()
	store := newFakeBatchStore()
	store.release = &SCFRelease{VersionID: "scf-1", Version: "2025.2"}
	store.edges = []CrosswalkEdge{
		{RequirementID: "r-61", AnchorID: "a-1", SCFID: "IAC-37", Strength: 0.9},
		{RequirementID: "r-61", AnchorID: "a-2", SCFID: "IAC-12", Strength: 0.5},
	}
	client := fnClient(func(req llm.GenerateRequest) (llm.GenerateResult, error) {
		ids, _ := req.Context["candidate_scf_ids"].([]string)
		return stubResult(`{"ranking":[{"scf_id":"` + ids[len(ids)-1] + `","rationale":"last first"}]}`), nil
	})
	stager := NewStager(store).WithReranker(NewReranker(client, &memRecorder{},
		func(context.Context) (bool, error) { return true, nil }))
	b, err := stager.Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", FrameworkSlug: "soc2",
		Raw: []byte("Control ID,Title,Description,Tested Under\nORG-1,MFA,Multi-factor authentication,CC6.1\n"),
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.SuggestionsStatus != SuggestionsReady || len(b.Staged) != 1 {
		t.Fatalf("batch = %+v", b)
	}
	row := b.Staged[0]
	if row.LLMRerank != RerankApplied || sources(row.Candidates) != "IAC-12/crosswalk IAC-37/crosswalk" || row.Candidates[0].Rationale != "last first" {
		t.Errorf("row = %+v, want reranked with the stub's rationale", row)
	}
}
