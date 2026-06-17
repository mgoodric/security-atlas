// Pure-Go unit tests for the slice-749 period-scoped service (no Postgres, no
// build tag — slice-353 Q-2 fast tier). They prove the period surface reuses the
// shared runSummary pipeline (suppression branches + always-return-the-frozen-set
// graceful degradation) and that the horizon-bound citation resolver receives the
// correct (controlID, frozenAt) so a post-freeze id is rejected at the resolver
// gate too. The full request path (real RLS, real frozen-population integrity,
// cross-tenant) lives in integration_test.go.
package evidencesummary

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// fakePeriodReader returns a fixed frozen evidence set (or error). It models
// *PeriodStore.PeriodEvidenceSet: the Records it returns are ALREADY the
// frozen-population top-N (the production store applies observed_at <= frozen_at),
// so a post-freeze record is absent here by construction.
type fakePeriodReader struct {
	set PeriodEvidenceSet
	err error
}

func (f fakePeriodReader) PeriodEvidenceSet(_ context.Context, _, _ uuid.UUID) (PeriodEvidenceSet, error) {
	return f.set, f.err
}

// fakePeriodResolver records the (controlID, frozenAt) it was called with and
// resolves only ids in `owned`. It is the horizon-bound resolver seam: a
// production *PeriodStore would refuse a post-freeze id; this fake lets the test
// assert the binding propagated AND drive the suppression branches.
type fakePeriodResolver struct {
	owned       map[uuid.UUID]string // id -> kind (frozen-population members only)
	err         error
	gotControl  uuid.UUID
	gotFrozenAt time.Time
	calls       int
}

func (f *fakePeriodResolver) ResolveBeforeHorizon(_ context.Context, id, controlID uuid.UUID, frozenAt time.Time) (Citation, bool, error) {
	f.calls++
	f.gotControl = controlID
	f.gotFrozenAt = frozenAt
	if f.err != nil {
		return Citation{}, false, f.err
	}
	kind, ok := f.owned[id]
	if !ok {
		return Citation{}, false, nil
	}
	return Citation{Kind: kind, ID: id.String()}, true, nil
}

func frozenSetWith(ctrlID uuid.UUID, periodID uuid.UUID, frozenAt time.Time, evIDs ...uuid.UUID) PeriodEvidenceSet {
	inner := EvidenceSet{ControlID: ctrlID, ControlTitle: "frozen control", TotalCount: len(evIDs)}
	for i, ev := range evIDs {
		inner.Records = append(inner.Records, EvidenceFact{
			EvidenceID:   ev,
			EvidenceKind: "access_review.completion",
			Result:       "pass",
			// All pre-freeze by construction (the production reader guarantees it).
			ObservedAt: frozenAt.Add(-time.Duration(i+1) * time.Hour),
		})
	}
	return PeriodEvidenceSet{EvidenceSet: inner, AuditPeriodID: periodID, FrozenAt: frozenAt}
}

func periodSvcWith(reader PeriodEvidenceReader, draft string, resolver PeriodCitationResolver) *PeriodService {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text: draft, ModelName: "stub-model", ModelVersion: "1", ModelProvider: "stub",
	}}
	return NewPeriodService(reader, client, resolver)
}

func TestPeriodSummarize_ValidCitationRendersAndBindsHorizon(t *testing.T) {
	ctrlID, evID, periodID := uuid.New(), uuid.New(), uuid.New()
	frozenAt := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	set := frozenSetWith(ctrlID, periodID, frozenAt, evID)
	draft := fmt.Sprintf("Control (%s) frozen evidence (%s) passed.", ctrlID, evID)
	resolver := &fakePeriodResolver{owned: map[uuid.UUID]string{ctrlID: KindControl, evID: KindEvidence}}

	sum, err := periodSvcWith(fakePeriodReader{set: set}, draft, resolver).
		PeriodSummarize(context.Background(), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodSummarize: %v", err)
	}
	if sum.Suppressed {
		t.Fatalf("expected rendered summary, got suppressed (%q)", sum.Reason)
	}
	if sum.Text != draft {
		t.Error("summary text not surfaced verbatim")
	}
	if len(sum.Citations) != 2 {
		t.Errorf("want 2 resolved citations, got %d", len(sum.Citations))
	}
	// AC-4: frozen-horizon metadata travels with the summary.
	if sum.AuditPeriodID != periodID || !sum.FrozenAt.Equal(frozenAt) {
		t.Errorf("frozen metadata mislabeled: period=%s frozen_at=%s", sum.AuditPeriodID, sum.FrozenAt)
	}
	// P0-749-1: the resolver MUST have been bound with the control + freeze
	// horizon, so its evidence membership check is frozen-population-scoped.
	if resolver.gotControl != ctrlID {
		t.Errorf("resolver bound control = %s, want %s", resolver.gotControl, ctrlID)
	}
	if !resolver.gotFrozenAt.Equal(frozenAt) {
		t.Errorf("resolver bound frozen_at = %s, want %s", resolver.gotFrozenAt, frozenAt)
	}
}

// A post-freeze id can never even reach the resolver: the frozen EvidenceSet
// excludes it, so the grounding gate (allowedIDs) fails it first (P0-749-1).
func TestPeriodSummarize_PostFreezeIDFailsGroundingGate(t *testing.T) {
	ctrlID, evID, periodID := uuid.New(), uuid.New(), uuid.New()
	frozenAt := time.Now().UTC()
	set := frozenSetWith(ctrlID, periodID, frozenAt, evID)
	// The model cites a post-freeze id that was NEVER in the frozen set.
	postFreezeID := uuid.New()
	draft := fmt.Sprintf("Control (%s) cites post-freeze (%s).", ctrlID, postFreezeID)
	// Even if the resolver WOULD own it, the grounding gate fails it first.
	resolver := &fakePeriodResolver{owned: map[uuid.UUID]string{
		ctrlID: KindControl, evID: KindEvidence, postFreezeID: KindEvidence,
	}}

	sum, _ := periodSvcWith(fakePeriodReader{set: set}, draft, resolver).
		PeriodSummarize(context.Background(), ctrlID, periodID)
	if !sum.Suppressed || sum.Reason != ReasonUnresolvedCitation {
		t.Fatalf("want suppressed/unresolved_citation for a post-freeze id, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	if sum.Text != "" {
		t.Error("post-freeze-citation summary must not surface text (P0-749-1)")
	}
}

func TestPeriodSummarize_NoFrozenEvidenceSuppressed(t *testing.T) {
	ctrlID, periodID := uuid.New(), uuid.New()
	frozenAt := time.Now().UTC()
	set := frozenSetWith(ctrlID, periodID, frozenAt) // no records
	resolver := &fakePeriodResolver{owned: map[uuid.UUID]string{ctrlID: KindControl}}

	sum, err := periodSvcWith(fakePeriodReader{set: set}, "ignored", resolver).
		PeriodSummarize(context.Background(), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodSummarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonNoEvidence {
		t.Fatalf("want suppressed/no_evidence, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	if resolver.calls != 0 {
		t.Error("no model/resolver call should happen when there is no frozen evidence")
	}
}

func TestPeriodSummarize_GenerationUnavailableStillReturnsFrozenSet(t *testing.T) {
	ctrlID, evID, periodID := uuid.New(), uuid.New(), uuid.New()
	frozenAt := time.Now().UTC()
	set := frozenSetWith(ctrlID, periodID, frozenAt, evID)
	client := &llm.StubClient{Err: errors.New("ollama unreachable")}
	svc := NewPeriodService(fakePeriodReader{set: set}, client,
		&fakePeriodResolver{owned: map[uuid.UUID]string{ctrlID: KindControl, evID: KindEvidence}})

	sum, err := svc.PeriodSummarize(context.Background(), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodSummarize must not error on backend failure (graceful degradation): %v", err)
	}
	if !sum.Suppressed || sum.Reason != ReasonGenerationUnavailable {
		t.Fatalf("want suppressed/generation_unavailable, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	// AC-7 / P0-502-7: the deterministic frozen evidence list never blocks on the LLM.
	if len(sum.EvidenceSet.Records) != 1 {
		t.Error("frozen evidence set must still render when generation is unavailable")
	}
}

func TestPeriodSummarize_ReadErrorIsReturned(t *testing.T) {
	ctrlID, periodID := uuid.New(), uuid.New()
	for _, e := range []error{ErrNoPeriod, ErrPeriodNotFrozen, ErrNoControl, errors.New("db down")} {
		_, err := periodSvcWith(fakePeriodReader{err: e}, "x", &fakePeriodResolver{}).
			PeriodSummarize(context.Background(), ctrlID, periodID)
		if err == nil {
			t.Fatalf("a read failure (%v) must be returned as an error", e)
		}
		if !errors.Is(err, e) {
			t.Errorf("error must wrap/equal the read error: got %v, want %v", err, e)
		}
	}
}
