package qmapsuggest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// fakeReader is one loaded SCF release, "B" (current), unless byRelease
// names others. candidates and resolve are release B's; byRelease holds
// each other release's anchors, which serve as both its candidates and its
// resolvable codes. noCatalog models an empty catalog.
type fakeReader struct {
	text       string
	textErr    error
	candidates []Candidate
	resolve    map[string]Candidate
	byRelease  map[string][]Candidate
	noCatalog  bool
}

var fakeReleaseIDs = map[string]pgtype.UUID{}

func fakeReleaseID(label string) pgtype.UUID {
	if id, ok := fakeReleaseIDs[label]; ok {
		return id
	}
	return pgtype.UUID{}
}

func init() {
	for _, l := range []string{"A", "B"} {
		fakeReleaseIDs[l] = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
}

func (f fakeReader) QuestionTextForMapping(context.Context, uuid.UUID) (string, error) {
	return f.text, f.textErr
}

func (f fakeReader) LookupRelease(_ context.Context, label string) (scfrelease.Release, error) {
	if f.noCatalog {
		if label == "" {
			return scfrelease.Release{}, scfrelease.ErrNoCurrentRelease
		}
		return scfrelease.Release{}, fmt.Errorf("%w %q", scfrelease.ErrUnknownRelease, label)
	}
	if label == "" {
		label = "B"
	}
	if _, ok := f.byRelease[label]; label != "B" && !ok {
		return scfrelease.Release{}, fmt.Errorf("%w %q", scfrelease.ErrUnknownRelease, label)
	}
	return scfrelease.Release{ID: fakeReleaseID(label), Version: label}, nil
}

func (f fakeReader) labelOf(versionID string) string {
	for l, id := range fakeReleaseIDs {
		if uuid.UUID(id.Bytes).String() == versionID {
			return l
		}
	}
	return ""
}

func (f fakeReader) RetrieveCandidatesInRelease(_ context.Context, versionID string, _ []string) ([]Candidate, error) {
	if l := f.labelOf(versionID); l != "B" {
		return f.byRelease[l], nil
	}
	return f.candidates, nil
}

func (f fakeReader) ResolveAnchorInRelease(_ context.Context, release, scfID string) (Candidate, bool, error) {
	if release != "B" {
		for _, c := range f.byRelease[release] {
			if c.SCFID == scfID {
				return c, true, nil
			}
		}
		return Candidate{}, false, nil
	}
	c, ok := f.resolve[scfID]
	return c, ok, nil
}

type fakeStore struct {
	persisted    int
	suppressed   []string
	approveCalls int
	approver     string
}

func (f *fakeStore) PersistProposal(_ context.Context, qID uuid.UUID, anchor Candidate, rationale string, _ []string, _ Provenance) (string, error) {
	f.persisted++
	return "11111111-1111-1111-1111-111111111111", nil
}

func (f *fakeStore) Approve(_ context.Context, _ uuid.UUID, approver string) (ApprovedProposal, error) {
	f.approveCalls++
	f.approver = approver
	return ApprovedProposal{HumanApproved: true, HumanApprover: approver}, nil
}

func (f *fakeStore) Reject(context.Context, uuid.UUID, string) (RejectedProposal, error) {
	return RejectedProposal{Status: "rejected"}, nil
}

func (f *fakeStore) RecordSuppression(_ context.Context, _ uuid.UUID, _ string, reason string, _ Provenance) error {
	f.suppressed = append(f.suppressed, reason)
	return nil
}

type fakeAudit struct{ writes int }

func (f *fakeAudit) Write(context.Context, llm.Generation) (dbx.AiGeneration, error) {
	f.writes++
	return dbx.AiGeneration{}, nil
}

func stubSvc(reader Reader, store ProposalStore, audit AuditSink, draft string) *Service {
	return NewService(reader, &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}, store, audit)
}

func TestSuggest_NoCandidatesManualOnly(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := stubSvc(fakeReader{text: "quantum lattice roadmap"}, st, &fakeAudit{}, "unused")
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.ManualOnly || out.Reason != ReasonNoCandidates {
		t.Fatalf("want manual-only no-candidates, got %+v", out)
	}
	if st.persisted != 0 {
		t.Fatal("no-candidates path must persist nothing")
	}
	if len(st.suppressed) != 1 || st.suppressed[0] != ReasonNoCandidates {
		t.Fatalf("no-candidates suppression audit not recorded: %+v", st.suppressed)
	}
}

func TestSuggest_FabricatedAnchorSuppressed(t *testing.T) {
	t.Parallel()
	c := Candidate{AnchorUUID: uuid.NewString(), SCFID: "IAC-06", Title: "MFA", Excerpt: "multi-factor authentication"}
	st := &fakeStore{}
	audit := &fakeAudit{}
	svc := stubSvc(fakeReader{
		text:       "Do you enforce MFA?",
		candidates: []Candidate{c},
		resolve:    map[string]Candidate{c.SCFID: c},
	}, st, audit, `{"scf_id":"IAC-99","rationale":"Looks like access control."}`)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), Actor: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed || out.Reason != ReasonOutOfGrounding {
		t.Fatalf("want out_of_grounding suppression, got %+v", out)
	}
	if st.persisted != 0 {
		t.Fatal("fabricated anchor must not persist")
	}
	if len(st.suppressed) != 1 || st.suppressed[0] != ReasonOutOfGrounding {
		t.Fatalf("suppression audit reason not recorded: %+v", st.suppressed)
	}
	if audit.writes != 1 {
		t.Fatalf("generation audit writes = %d, want 1", audit.writes)
	}
}

func TestSuggest_ValidPickPersistsUnapprovedProposal(t *testing.T) {
	t.Parallel()
	c := Candidate{AnchorUUID: uuid.NewString(), SCFID: "IAC-06", Title: "MFA", Excerpt: "multi-factor authentication"}
	st := &fakeStore{}
	svc := stubSvc(fakeReader{
		text:       "Do you enforce MFA?",
		candidates: []Candidate{c},
		resolve:    map[string]Candidate{c.SCFID: c},
	}, st, &fakeAudit{}, `{"scf_id":"IAC-06","rationale":"The question asks about multi-factor access controls."}`)
	out, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), Actor: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Suppressed || out.ManualOnly || out.ProposalID == "" {
		t.Fatalf("want proposal, got %+v", out)
	}
	if out.SCFAnchorID != "IAC-06" || out.Rationale == "" {
		t.Fatalf("proposal fields not surfaced: %+v", out)
	}
	if st.persisted != 1 {
		t.Fatalf("persisted = %d, want 1", st.persisted)
	}
}

// #1772: candidates and the persisted anchor come from one stated release.
// A and B both carry IAC-06 as different anchors; the release the caller
// names decides which one is proposed, and the output says which release.
func TestSuggest_AnchorsFromOneStatedRelease(t *testing.T) {
	t.Parallel()
	a := Candidate{AnchorUUID: uuid.NewString(), SCFID: "IAC-06", Title: "A's MFA", Excerpt: "multi-factor authentication"}
	b := Candidate{AnchorUUID: uuid.NewString(), SCFID: "IAC-06", Title: "B's MFA", Excerpt: "multi-factor authentication"}
	reader := fakeReader{
		text:       "Do you enforce MFA?",
		candidates: []Candidate{b},
		resolve:    map[string]Candidate{b.SCFID: b},
		byRelease:  map[string][]Candidate{"A": {a}},
	}
	pick := `{"scf_id":"IAC-06","rationale":"The question asks about multi-factor access controls."}`
	for _, tc := range []struct {
		release, wantRelease, wantUUID string
	}{
		{"", "B", b.AnchorUUID},
		{"B", "B", b.AnchorUUID},
		{"A", "A", a.AnchorUUID},
	} {
		out, err := stubSvc(reader, &fakeStore{}, &fakeAudit{}, pick).
			Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), SCFRelease: tc.release})
		if err != nil {
			t.Fatalf("release %q: Suggest: %v", tc.release, err)
		}
		if out.SCFRelease != tc.wantRelease || out.SCFAnchorUUID != tc.wantUUID {
			t.Errorf("release %q: got release %q anchor %s; want %q %s",
				tc.release, out.SCFRelease, out.SCFAnchorUUID, tc.wantRelease, tc.wantUUID)
		}
	}
}

func TestSuggest_UnknownReleaseRefused(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	_, err := stubSvc(fakeReader{text: "Do you enforce MFA?"}, st, nil, "x").
		Suggest(context.Background(), SuggestParams{QuestionID: uuid.New(), SCFRelease: "2019.1"})
	if !errors.Is(err, scfrelease.ErrUnknownRelease) {
		t.Fatalf("want ErrUnknownRelease, got %v", err)
	}
	if st.persisted != 0 || len(st.suppressed) != 0 {
		t.Fatalf("an unknown release must write nothing: %+v", st)
	}
}

// With no release named and no SCF catalog loaded, the answer is
// manual-only, as it was before releases could be named.
func TestSuggest_NoCatalogIsManualOnly(t *testing.T) {
	t.Parallel()
	out, err := stubSvc(fakeReader{text: "Do you enforce MFA?", noCatalog: true}, &fakeStore{}, nil, "x").
		Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.ManualOnly || out.Reason != ReasonNoCandidates || out.SCFRelease != "" {
		t.Fatalf("want manual-only with no release, got %+v", out)
	}
}

func TestSuggest_QuestionNotFound(t *testing.T) {
	t.Parallel()
	svc := stubSvc(fakeReader{textErr: ErrQuestionNotFound}, &fakeStore{}, nil, "x")
	_, err := svc.Suggest(context.Background(), SuggestParams{QuestionID: uuid.New()})
	if !errors.Is(err, ErrQuestionNotFound) {
		t.Fatalf("want ErrQuestionNotFound, got %v", err)
	}
}

func TestApprove_BlankApproverRejected(t *testing.T) {
	t.Parallel()
	st := &fakeStore{}
	svc := stubSvc(fakeReader{}, st, nil, "x")
	for _, approver := range []string{"", "  "} {
		if _, err := svc.Approve(context.Background(), uuid.New(), approver); !errors.Is(err, ErrApproverRequired) {
			t.Fatalf("approver %q: want ErrApproverRequired, got %v", approver, err)
		}
	}
	if st.approveCalls != 0 {
		t.Fatal("blank approver must not reach store")
	}
}
