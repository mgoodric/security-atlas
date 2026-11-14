package controlimport

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Candidate sources (#1780). Crosswalk candidates come from the framework's
// SCF mappings of the row's tested-under criteria; keyword candidates from
// the SCF catalog keyword retrieval, filling the list.
const (
	SourceCrosswalk = "crosswalk"
	SourceKeyword   = "keyword"
)

// Batch suggestion states. A batch is pending while its candidates are
// computed in the background, ready once they are stored, failed if the
// computation errored, and unavailable when no current SCF release exists.
const (
	SuggestionsPending     = "pending"
	SuggestionsReady       = "ready"
	SuggestionsFailed      = "failed"
	SuggestionsUnavailable = "unavailable"
)

// CandidateListSize is how many candidates each staged row keeps.
const CandidateListSize = maxAnchorCands

// SCFRelease is the SCF catalog version candidates are drawn from.
type SCFRelease struct {
	VersionID string `json:"version_id"`
	Version   string `json:"version"`
}

// Candidate is one ranked SCF anchor suggestion for a staged row. Reaches
// lists the row's tested-under criteria whose framework mappings reach the
// anchor; TestedCount is how many criteria the row has ("reaches N of M").
type Candidate struct {
	Rank        int           `json:"rank"`
	AnchorID    string        `json:"scf_anchor_id"`
	SCFID       string        `json:"scf_id"`
	Title       string        `json:"title"`
	Source      string        `json:"source"`
	Reaches     []Requirement `json:"reaches"`
	TestedCount int           `json:"tested_count"`
	Strength    float64       `json:"strength,omitempty"`
	// AIAssisted marks a candidate the LLM placed and explained (#1781);
	// Rationale is its explanation. Both are unset when no LLM reranked
	// the row.
	AIAssisted bool   `json:"ai_assisted"`
	Rationale  string `json:"rationale,omitempty"`
}

// Fit renders the candidate's coverage of the row's tested criteria, e.g.
// "reaches 2 of 2: CC6.1, CC6.2". It is empty for a row with no criteria.
func (c Candidate) Fit() string {
	if c.TestedCount == 0 {
		return ""
	}
	s := fmt.Sprintf("reaches %d of %d", len(c.Reaches), c.TestedCount)
	if len(c.Reaches) == 0 {
		return s
	}
	codes := make([]string, len(c.Reaches))
	for i, r := range c.Reaches {
		codes[i] = r.Code
	}
	return s + ": " + strings.Join(codes, ", ")
}

// CrosswalkEdge is one mapping from a framework requirement to an SCF anchor
// in the batch's SCF release.
type CrosswalkEdge struct {
	RequirementID string
	AnchorID      string
	SCFID         string
	Title         string
	Strength      float64
}

// RowSuggestions is the computed candidate list for one staged row.
type RowSuggestions struct {
	RowID              string
	Candidates         []Candidate
	NoCandidateReaches bool
	// LLMRerank is the row's rerank state: RerankOff, RerankApplied or
	// RerankUnavailable.
	LLMRerank string
}

// buildCandidates ranks one row's candidates: crosswalk anchors first, by
// tested criteria reached and then the strongest reaching edge; keyword
// anchors (already ranked by keyword overlap) fill the list to limit. An
// anchor appears once. The row is flagged when it has tested criteria and
// no candidate reaches any of them.
func buildCandidates(row BatchControl, edges []CrosswalkEdge, keyword []AnchorCandidate, limit int) RowSuggestions {
	byReq := make(map[string]Requirement, len(row.TestedUnder))
	for _, r := range row.TestedUnder {
		byReq[r.RequirementID] = r
	}
	type agg struct {
		c       Candidate
		reached map[string]bool
	}
	found := map[string]*agg{}
	for _, e := range edges {
		req, ok := byReq[e.RequirementID]
		if !ok {
			continue
		}
		a := found[e.AnchorID]
		if a == nil {
			a = &agg{
				c:       Candidate{AnchorID: e.AnchorID, SCFID: e.SCFID, Title: e.Title, Source: SourceCrosswalk},
				reached: map[string]bool{},
			}
			found[e.AnchorID] = a
		}
		if e.Strength > a.c.Strength {
			a.c.Strength = e.Strength
		}
		a.reached[req.RequirementID] = true
	}
	crosswalk := make([]Candidate, 0, len(found))
	for _, a := range found {
		c := a.c
		for _, r := range row.TestedUnder {
			if a.reached[r.RequirementID] {
				c.Reaches = append(c.Reaches, r)
			}
		}
		crosswalk = append(crosswalk, c)
	}
	sort.Slice(crosswalk, func(i, j int) bool {
		a, b := crosswalk[i], crosswalk[j]
		if len(a.Reaches) != len(b.Reaches) {
			return len(a.Reaches) > len(b.Reaches)
		}
		if a.Strength != b.Strength {
			return a.Strength > b.Strength
		}
		return a.SCFID < b.SCFID
	})

	out := make([]Candidate, 0, limit)
	seen := map[string]bool{}
	for _, c := range crosswalk {
		if len(out) == limit {
			break
		}
		seen[c.AnchorID] = true
		out = append(out, c)
	}
	for _, k := range keyword {
		if len(out) == limit {
			break
		}
		if k.AnchorID == "" || seen[k.AnchorID] {
			continue
		}
		seen[k.AnchorID] = true
		out = append(out, Candidate{AnchorID: k.AnchorID, SCFID: k.SCFID, Title: k.Title, Source: SourceKeyword})
	}
	for i := range out {
		out[i].Rank = i + 1
		out[i].TestedCount = len(row.TestedUnder)
		out[i].Reaches = nonNilReqs(out[i].Reaches)
	}
	return RowSuggestions{
		RowID:              row.ID,
		Candidates:         out,
		NoCandidateReaches: len(row.TestedUnder) > 0 && len(crosswalk) == 0,
		LLMRerank:          RerankOff,
	}
}

// suggest computes and stores every staged row's candidates, drawn from the
// batch's SCF release, then marks the batch's suggestions ready (or failed).
// With rerank set, the LLM reorders and explains each row's candidates; its
// failure leaves the deterministic candidates as computed.
//
// Every error path marks the batch failed (best effort), so a run that ends
// never leaves it pending. A run that never ends (the process stops) is the
// sweeper's to fail. Failed is final: a run whose batch the sweeper failed,
// before the run started or before it saved, is dropped with
// ErrSuggestionsNoLongerPending and writes nothing.
func (s *Stager) suggest(ctx context.Context, b Batch, rerank bool) ([]RowSuggestions, error) {
	select {
	case s.runs <- struct{}{}:
	case <-ctx.Done():
		return nil, s.markFailed(ctx, b.ID, fmt.Errorf("controlimport: waiting for a suggestion slot: %w", ctx.Err()))
	}
	defer func() { <-s.runs }()

	// The wait for a slot can outlast the sweeper's cutoff.
	cur, err := s.store.GetBatch(ctx, b.ID)
	if err != nil {
		return nil, s.markFailed(ctx, b.ID, fmt.Errorf("controlimport: re-read batch: %w", err))
	}
	if cur.SuggestionsStatus != SuggestionsPending {
		return nil, ErrSuggestionsNoLongerPending
	}

	out, err := s.computeSuggestions(ctx, b)
	if err != nil {
		return nil, s.markFailed(ctx, b.ID, err)
	}
	if rerank {
		s.reranker.rerankAll(ctx, b.Staged, out)
	}
	if err := s.store.SaveSuggestions(ctx, b.ID, out, SuggestionsReady); err != nil {
		if errors.Is(err, ErrSuggestionsNoLongerPending) {
			return nil, err
		}
		return nil, s.markFailed(ctx, b.ID, err)
	}
	return out, nil
}

// markFailedTimeout bounds the best-effort failed mark, which runs even when
// the run's own context is done.
const markFailedTimeout = 10 * time.Second

// markFailed records the batch's suggestions as failed and returns err,
// annotated if the mark itself failed.
func (s *Stager) markFailed(ctx context.Context, batchID string, err error) error {
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markFailedTimeout)
	defer cancel()
	if markErr := s.store.SaveSuggestions(mctx, batchID, nil, SuggestionsFailed); markErr != nil {
		return fmt.Errorf("%w (and marking the batch failed: %v)", err, markErr)
	}
	return err
}

func (s *Stager) computeSuggestions(ctx context.Context, b Batch) ([]RowSuggestions, error) {
	var reqIDs []string
	seen := map[string]bool{}
	for _, c := range b.Staged {
		for _, r := range c.TestedUnder {
			if !seen[r.RequirementID] {
				seen[r.RequirementID] = true
				reqIDs = append(reqIDs, r.RequirementID)
			}
		}
	}
	edges, err := s.store.CrosswalkAnchors(ctx, b.SCFRelease.VersionID, reqIDs)
	if err != nil {
		return nil, fmt.Errorf("controlimport: crosswalk anchors: %w", err)
	}
	out := make([]RowSuggestions, 0, len(b.Staged))
	for _, c := range b.Staged {
		sc := StagedControl{ExternalID: c.ExternalID, Title: c.Title, Description: c.Description}
		keywords := keywordsFrom(sc.ExternalID + " " + sc.Title + " " + sc.Description)
		retrieved, err := s.store.KeywordAnchors(ctx, b.SCFRelease.VersionID, keywords)
		if err != nil {
			return nil, fmt.Errorf("controlimport: keyword anchors for row %d: %w", c.RowNumber, err)
		}
		ranked := rankAnchorCandidates(sc, retrieved, 0)
		out = append(out, buildCandidates(c, edges, ranked, CandidateListSize))
	}
	return out, nil
}
