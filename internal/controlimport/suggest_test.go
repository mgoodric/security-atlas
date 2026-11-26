package controlimport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	req61 = Requirement{Code: "CC6.1", RequirementID: "r-61"}
	req62 = Requirement{Code: "CC6.2", RequirementID: "r-62"}
	req71 = Requirement{Code: "CC7.1", RequirementID: "r-71"}
)

func sources(cs []Candidate) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.SCFID + "/" + c.Source
	}
	return strings.Join(parts, " ")
}

func TestBuildCandidatesRanksCrosswalkByCriteriaReachedThenStrength(t *testing.T) {
	t.Parallel()
	row := BatchControl{ID: "row-1", TestedUnder: []Requirement{req61, req62}}
	edges := []CrosswalkEdge{
		{RequirementID: "r-62", AnchorID: "a-strong", SCFID: "IAC-12", Strength: 0.9},
		{RequirementID: "r-61", AnchorID: "a-both", SCFID: "IAC-37", Strength: 0.7},
		{RequirementID: "r-62", AnchorID: "a-both", SCFID: "IAC-37", Strength: 0.6},
		{RequirementID: "r-61", AnchorID: "a-weak", SCFID: "IAC-01", Strength: 0.3},
		{RequirementID: "r-other", AnchorID: "a-unrelated", SCFID: "END-01", Strength: 1},
	}
	keyword := []AnchorCandidate{
		{AnchorID: "a-both", SCFID: "IAC-37"},
		{AnchorID: "k-1", SCFID: "MON-02"},
		{AnchorID: "k-2", SCFID: "MON-03"},
	}
	got := buildCandidates(row, edges, keyword, 4)

	if s := sources(got.Candidates); s != "IAC-37/crosswalk IAC-12/crosswalk IAC-01/crosswalk MON-02/keyword" {
		t.Fatalf("candidates = %s", s)
	}
	top := got.Candidates[0]
	if top.Rank != 1 || top.Fit() != "reaches 2 of 2: CC6.1, CC6.2" || top.Strength != 0.7 {
		t.Errorf("top = %+v fit %q", top, top.Fit())
	}
	if kw := got.Candidates[3]; kw.Rank != 4 || kw.Fit() != "reaches 0 of 2" || len(kw.Reaches) != 0 {
		t.Errorf("keyword candidate = %+v fit %q", kw, kw.Fit())
	}
	if got.NoCandidateReaches {
		t.Error("row with reaching candidates is flagged")
	}
}

func TestBuildCandidatesKeywordOnlyWithoutTestedUnder(t *testing.T) {
	t.Parallel()
	row := BatchControl{ID: "row-1"}
	got := buildCandidates(row, nil, []AnchorCandidate{{AnchorID: "k-1", SCFID: "HRS-04"}}, 8)
	if s := sources(got.Candidates); s != "HRS-04/keyword" {
		t.Fatalf("candidates = %s", s)
	}
	if got.NoCandidateReaches {
		t.Error("row without tested criteria is flagged")
	}
	if got.Candidates[0].Fit() != "" {
		t.Errorf("fit = %q, want empty with no tested criteria", got.Candidates[0].Fit())
	}
}

func TestBuildCandidatesFlagsUnreachedCriteria(t *testing.T) {
	t.Parallel()
	row := BatchControl{ID: "row-1", TestedUnder: []Requirement{req71}}
	got := buildCandidates(row, []CrosswalkEdge{{RequirementID: "r-61", AnchorID: "a", SCFID: "IAC-37"}},
		[]AnchorCandidate{{AnchorID: "k-1", SCFID: "MON-02"}}, 8)
	if !got.NoCandidateReaches {
		t.Error("row whose criteria nothing reaches is not flagged")
	}
	if s := sources(got.Candidates); s != "MON-02/keyword" {
		t.Errorf("candidates = %s, want the keyword fill", s)
	}
}

func suggestStore() *fakeBatchStore {
	store := newFakeBatchStore()
	store.release = &SCFRelease{VersionID: "scf-v", Version: "2026.3"}
	store.edges = []CrosswalkEdge{{RequirementID: "r-61", AnchorID: "a-1", SCFID: "IAC-37", Strength: 0.8}}
	store.keyword = []AnchorCandidate{{AnchorID: "k-1", SCFID: "IAC-01", Title: "Identity and access management", Description: "users"}}
	return store
}

const suggestSheet = "Control ID,Title,Description,Criteria\nAC-1,MFA,Users use MFA,CC6.1\nAC-2,Access,Users reviewed,\n"

func TestStageComputesCandidatesWithoutLLM(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	b, err := NewStager(store).Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.SuggestionsStatus != SuggestionsReady || b.SCFRelease == nil || b.SCFRelease.Version != "2026.3" {
		t.Fatalf("batch suggestions = %q release %+v", b.SuggestionsStatus, b.SCFRelease)
	}
	if s := sources(b.Staged[0].Candidates); s != "IAC-37/crosswalk IAC-01/keyword" {
		t.Errorf("row 1 candidates = %s", s)
	}
	if s := sources(b.Staged[1].Candidates); s != "IAC-01/keyword" {
		t.Errorf("row 2 candidates = %s", s)
	}
	if store.status != SuggestionsReady || len(store.saved) != 2 {
		t.Errorf("saved %d rows with status %q", len(store.saved), store.status)
	}
}

func TestStageWithoutSCFReleaseIsUnavailable(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	store.release = nil
	b, err := NewStager(store).Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.SuggestionsStatus != SuggestionsUnavailable || b.SCFRelease != nil || store.saved != nil {
		t.Errorf("status %q release %+v saved %v", b.SuggestionsStatus, b.SCFRelease, store.saved)
	}
}

func TestStageKeepsBatchWhenSuggestionFails(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	store.crosswalkErr = errors.New("boom")
	b, err := NewStager(store).Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.ID == "" || b.StagedCount != 2 || b.SuggestionsStatus != SuggestionsFailed || store.status != SuggestionsFailed {
		t.Errorf("batch %q staged %d status %q stored %q", b.ID, b.StagedCount, b.SuggestionsStatus, store.status)
	}
}

func TestStageSuggestsInBackgroundAboveThreshold(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	s := NewStager(store)
	s.asyncThreshold = 1
	done := make(chan error, 1)
	s.background = func(_ string, err error) { done <- err }
	b, err := s.Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.SuggestionsStatus != SuggestionsPending {
		t.Errorf("returned status = %q, want pending", b.SuggestionsStatus)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("background suggest: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background suggestion never finished")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.status != SuggestionsReady || len(store.saved) != 2 {
		t.Errorf("background saved %d rows with status %q", len(store.saved), store.status)
	}
}

func TestListRowsFiltersAndValidatesStatus(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	s := NewStager(store)
	b, err := s.Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	store.created.Staged[1].NoCandidateReaches = true
	_, rows, err := s.ListRows(tenantCtx(t, batchTenant), b.ID, RowFilter{Status: "pending", NoCandidateReaches: true})
	if err != nil || len(rows) != 1 || rows[0].ExternalID != "AC-2" {
		t.Errorf("filtered rows = %+v, %v", rows, err)
	}
	if _, _, err := s.ListRows(tenantCtx(t, batchTenant), b.ID, RowFilter{Status: "bogus"}); !errors.Is(err, ErrInvalidRowStatus) {
		t.Errorf("bogus status err = %v", err)
	}
}

func TestKeywordRetrievalIsPinnedToTheBatchRelease(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	b, err := NewStager(store).Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if len(store.keywordVersions) == 0 {
		t.Fatal("no keyword retrieval ran")
	}
	for _, v := range store.keywordVersions {
		if v != b.SCFRelease.VersionID {
			t.Errorf("keyword retrieval pinned to %q, want the batch release %q", v, b.SCFRelease.VersionID)
		}
	}
}

func TestFailedReadySaveMarksTheBatchFailed(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	store.saveErr = func(status string) error {
		if status == SuggestionsReady {
			return errors.New("save boom")
		}
		return nil
	}
	b, err := NewStager(store).Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.SuggestionsStatus != SuggestionsFailed {
		t.Errorf("returned status = %q, want failed", b.SuggestionsStatus)
	}
	if got := strings.Join(store.saveCalls, ","); got != SuggestionsReady+","+SuggestionsFailed {
		t.Errorf("save calls = %s, want ready then failed", got)
	}
}

// A batch the sweeper failed while its run waited for a slot is dropped:
// nothing is computed and nothing is saved.
func TestRunDropsABatchNoLongerPendingOnceItHasASlot(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	s := NewStager(store)
	b, err := s.Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	store.status = SuggestionsFailed // the sweeper got there first
	store.saveCalls, store.keywordVersions = nil, nil
	b.SuggestionsStatus = SuggestionsPending
	_, err = s.suggest(tenantCtx(t, batchTenant), b, 0)
	if !errors.Is(err, ErrSuggestionsNoLongerPending) {
		t.Fatalf("suggest err = %v, want ErrSuggestionsNoLongerPending", err)
	}
	if len(store.keywordVersions) != 0 || len(store.saveCalls) != 0 || store.status != SuggestionsFailed {
		t.Errorf("dropped run retrieved %d times, saved %v, status %q; want nothing and failed",
			len(store.keywordVersions), store.saveCalls, store.status)
	}
}

// A run that computed its candidates after the sweeper failed the batch
// does not overwrite failed, and is not reported as a new failure.
func TestLateReadySaveLeavesAFailedBatchFailed(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	s := NewStager(store)
	b, err := s.Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	store.status = SuggestionsPending
	store.saveCalls = nil
	// The sweeper fails the batch between the slot re-check and the save.
	sweepDuringCompute := &sweepingStore{fakeBatchStore: store}
	s.store = sweepDuringCompute
	_, err = s.suggest(tenantCtx(t, batchTenant), b, 0)
	if !errors.Is(err, ErrSuggestionsNoLongerPending) {
		t.Fatalf("suggest err = %v, want ErrSuggestionsNoLongerPending", err)
	}
	if got := strings.Join(store.saveCalls, ","); got != SuggestionsReady {
		t.Errorf("save calls = %q, want one refused ready save and no failed mark", got)
	}
	if store.status != SuggestionsFailed {
		t.Errorf("status = %q, want failed", store.status)
	}
}

// sweepingStore fails the batch, as the sweeper would, during the run's
// crosswalk read.
type sweepingStore struct{ *fakeBatchStore }

func (s *sweepingStore) CrosswalkAnchors(ctx context.Context, v string, ids []string) ([]CrosswalkEdge, error) {
	s.mu.Lock()
	s.status = SuggestionsFailed
	s.mu.Unlock()
	return s.fakeBatchStore.CrosswalkAnchors(ctx, v, ids)
}

// #2329: a run that cannot get a slot, because another tenant's runs hold
// them all, is deferred to the background and completes once a slot frees;
// the batch is never failed for it.
func TestBusyTenantDefersAnotherTenantsBatch(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	s := NewStager(store).WithRunLimit(1)
	done := make(chan error, 1)
	s.background = func(_ string, err error) { done <- err }
	// Another tenant holds the only slot, as a long rerank would.
	release, ok := s.runs.acquire(context.Background(), "22222222-2222-2222-2222-222222222222", 0)
	if !ok {
		t.Fatal("could not take the only slot")
	}
	// The request's deadline ends before any slot frees.
	ctx, cancel := context.WithTimeout(tenantCtx(t, batchTenant), 50*time.Millisecond)
	defer cancel()
	b, err := s.Stage(ctx, StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if b.SuggestionsStatus != SuggestionsPending {
		t.Errorf("returned status = %q, want pending (deferred, not failed)", b.SuggestionsStatus)
	}
	select {
	case err := <-done:
		t.Fatalf("background run finished (%v) while the other tenant held the slot", err)
	case <-time.After(100 * time.Millisecond):
	}
	store.mu.Lock()
	if store.status != SuggestionsPending || len(store.keywordVersions) != 0 {
		t.Errorf("stored status %q after %d retrievals, want pending with none while waiting", store.status, len(store.keywordVersions))
	}
	store.mu.Unlock()

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("background suggest: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deferred run never finished after the slot freed")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.status != SuggestionsReady {
		t.Errorf("stored status = %q, want ready", store.status)
	}
}

// panickingStore panics during the run's crosswalk read.
type panickingStore struct{ *fakeBatchStore }

func (panickingStore) CrosswalkAnchors(context.Context, string, []string) ([]CrosswalkEdge, error) {
	panic("crosswalk boom")
}

// #2569: an inline run that panics, which the HTTP server recovers, still
// frees its slot.
func TestInlineRunPanicReleasesItsSlot(t *testing.T) {
	t.Parallel()
	s := NewStager(panickingStore{suggestStore()}).WithRunLimit(1)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Stage did not panic")
			}
		}()
		_, _ = s.Stage(tenantCtx(t, batchTenant), StageInput{
			Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
		})
	}()
	release, ok := s.runs.acquire(context.Background(), batchTenant, 100*time.Millisecond)
	if !ok {
		t.Fatal("the only slot was not released after the panic")
	}
	release()
}

// #2569: a background run that waits past backgroundSlotWait gives up,
// leaves the queue, and fails its batch as the sweeper would.
func TestBackgroundSlotWaitIsBounded(t *testing.T) {
	t.Parallel()
	store := suggestStore()
	s := NewStager(store).WithRunLimit(1)
	s.asyncThreshold = 1
	s.backgroundSlotWait = 50 * time.Millisecond
	done := make(chan error, 1)
	s.background = func(_ string, err error) { done <- err }
	release, ok := s.runs.acquire(context.Background(), "22222222-2222-2222-2222-222222222222", 0)
	if !ok {
		t.Fatal("could not take the only slot")
	}
	defer release()
	if _, err := s.Stage(tenantCtx(t, batchTenant), StageInput{
		Filename: "c.csv", Raw: []byte(suggestSheet), FrameworkSlug: "soc2",
	}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "waiting for a suggestion slot") {
			t.Fatalf("background err = %v, want a slot-wait failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background run still waiting for a slot past its bound")
	}
	store.mu.Lock()
	status := store.status
	store.mu.Unlock()
	if status != SuggestionsFailed {
		t.Errorf("stored status = %q, want failed", status)
	}
	s.runs.mu.Lock()
	waiting := len(s.runs.waiters)
	s.runs.mu.Unlock()
	if waiting != 0 {
		t.Errorf("%d runs still queued, want 0", waiting)
	}
}
