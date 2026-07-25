//go:build integration

// Slice 319 — in-package integration tests for the questionnaire Store
// + the AnswerLibrary RLS path.
//
// Load-bearing functions covered:
//   - NewStore                             — pool wiring sanity (covered transitively)
//   - Store.CreateQuestionnaire            — happy path + tenant-missing-ctx error
//   - Store.GetQuestionnaire               — round-trip after Create
//   - Store.ListQuestionnaires             — multi-row + tenant scope
//   - Store.AddQuestionsFromParse          — mapped + unmapped rows; transaction commit
//   - Store.UpsertAnswer                   — insert path + save_to_library=true
//                                            (the citations-pass-through branch)
//   - Store.ListQuestionsWithAnswers       — stitching answers onto questions
//   - Store.SuggestForAnchorWithPool       — RLS-bound suggestion lookup
//   - SuggestForAnchor (in-package call)   — RLS scoping: tenant A does
//                                            not see tenant B's library
//   - rowToQuestionnaire / rowToQuestion /
//     rowToAnswer / pgUUID / uuidToString  — exercised transitively
//
// Distinct from internal/api/questionnaires/integration_test.go (slice
// 155). That suite drives the HTTP surface; this one drives the Store
// methods directly so coverage attributes to internal/questionnaire
// instead of internal/api/questionnaires (slice 319 enrolment).
//
// Run with:
//
//	go test -tags=integration -race ./internal/questionnaire/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).

package questionnaire

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a new tenant id + registers cleanup of the rows this
// slice's tests create (children → parents so FKs do not block the delete).
// Pure tenant-scoped DELETE in FK order, so it delegates to dbtest.SeedTenant
// (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"questionnaire_answers",
		"questionnaire_questions",
		"questionnaires",
		"answer_library",
	)
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// ----- Store CRUD round-trip -----

func TestStore_CreateGetList_RoundTrip(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	q, err := store.CreateQuestionnaire(ctx, CreateQuestionnaireParams{
		Name:           "ACME-CAIQ-2026",
		SourceLabel:    "CAIQ v4.1",
		SourceFilename: "acme.xlsx",
	})
	if err != nil {
		t.Fatalf("CreateQuestionnaire: %v", err)
	}
	if q.ID == "" || q.Name != "ACME-CAIQ-2026" {
		t.Fatalf("unexpected create result: %+v", q)
	}
	if q.Status != "draft" {
		t.Fatalf("expected default status `draft`, got %q", q.Status)
	}

	got, err := store.GetQuestionnaire(ctx, q.ID)
	if err != nil {
		t.Fatalf("GetQuestionnaire: %v", err)
	}
	if got.ID != q.ID || got.SourceLabel != "CAIQ v4.1" {
		t.Fatalf("Get round-trip mismatch: %+v vs %+v", got, q)
	}

	// Insert a second one; List must return both.
	_, err = store.CreateQuestionnaire(ctx, CreateQuestionnaireParams{
		Name:           "VendorB-SIG-2026",
		SourceLabel:    "SIG-Lite",
		SourceFilename: "b.xlsx",
	})
	if err != nil {
		t.Fatalf("CreateQuestionnaire #2: %v", err)
	}
	list, err := store.ListQuestionnaires(ctx)
	if err != nil {
		t.Fatalf("ListQuestionnaires: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 questionnaires, got %d", len(list))
	}
}

func TestStore_CreateQuestionnaire_RejectsMissingTenant(t *testing.T) {
	app := dbtest.NewAppPool(t)
	store := NewStore(app)
	_, err := store.CreateQuestionnaire(context.Background(), CreateQuestionnaireParams{
		Name: "x",
	})
	if err == nil {
		t.Fatal("expected ErrNoTenant for context without tenant")
	}
	if err != tenancy.ErrNoTenant {
		t.Fatalf("expected ErrNoTenant, got %v", err)
	}
}

func TestStore_AddQuestionsFromParse_MappedAndUnmapped(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// scf_anchor_id on questionnaire_questions and answer_library is
	// TEXT (free-form, NOT a FK to scf_anchors.id). Seeding the catalog
	// row is unnecessary for these store tests.

	q, err := store.CreateQuestionnaire(ctx, CreateQuestionnaireParams{
		Name: "Q1", SourceLabel: "src", SourceFilename: "x.xlsx",
	})
	if err != nil {
		t.Fatalf("CreateQuestionnaire: %v", err)
	}
	parsed := []ParsedQuestion{
		{Code: "Q-01", Text: "Mapped?", Domain: "IAC", AnswerType: "yes/no", ScfAnchorID: "IAC-06"},
		{Code: "Q-02", Text: "Unmapped?", Domain: "Misc", AnswerType: "yes/no"}, // no ScfAnchorID
	}
	got, err := store.AddQuestionsFromParse(ctx, q.ID, parsed)
	if err != nil {
		t.Fatalf("AddQuestionsFromParse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(got))
	}
	if got[0].ScfAnchorID != "IAC-06" || got[0].NeedsMapping {
		t.Fatalf("first row should be mapped to IAC-06: %+v", got[0])
	}
	if got[1].ScfAnchorID != "" || !got[1].NeedsMapping {
		t.Fatalf("second row should be needs_mapping: %+v", got[1])
	}
	if got[0].SortOrder != 0 || got[1].SortOrder != 1 {
		t.Fatalf("sort_order should be insertion order: %d, %d",
			got[0].SortOrder, got[1].SortOrder)
	}
}

func TestStore_UpsertAnswer_WithLibrarySave(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	// scf_anchor_id is TEXT here — no scf_anchors row required.
	q, err := store.CreateQuestionnaire(ctx, CreateQuestionnaireParams{Name: "Q", SourceLabel: "s"})
	if err != nil {
		t.Fatalf("CreateQuestionnaire: %v", err)
	}
	qs, err := store.AddQuestionsFromParse(ctx, q.ID, []ParsedQuestion{
		{Code: "Q-01", Text: "MFA?", ScfAnchorID: "IAC-06"},
	})
	if err != nil {
		t.Fatalf("AddQuestionsFromParse: %v", err)
	}
	questionID := qs[0].ID

	// Upsert an answer with save_to_library=true. The Citations field is
	// passed through opaque JSON — slice 319 covers the pass-through
	// branch. Citation-shape validation belongs at the HTTP layer
	// (slice 316 / canvas §4.6.5) and is enforced upstream.
	answer, err := store.UpsertAnswer(ctx, AnswerParams{
		QuestionID:      questionID,
		AnswerValue:     "yes",
		Narrative:       "MFA enforced for all admin roles via Okta.",
		Citations:       []any{map[string]any{"evidence_id": "ev-01"}},
		AuthoredBy:      "alice@acme.test",
		SaveToLibrary:   true,
		SCFAnchorIDHint: "IAC-06",
		SourceLabel:     "ACME-CAIQ-2026",
	})
	if err != nil {
		t.Fatalf("UpsertAnswer: %v", err)
	}
	if answer.AnswerValue != "yes" || answer.Narrative == "" {
		t.Fatalf("unexpected answer projection: %+v", answer)
	}
	if len(answer.Citations) != 1 {
		t.Fatalf("expected 1 citation through pass-through, got %d", len(answer.Citations))
	}

	// Library should have one entry now.
	sugg, err := store.SuggestForAnchorWithPool(ctx, "IAC-06", 10)
	if err != nil {
		t.Fatalf("SuggestForAnchorWithPool: %v", err)
	}
	if len(sugg) != 1 {
		t.Fatalf("expected 1 library entry post-save, got %d", len(sugg))
	}
	if sugg[0].SourceLabel != "ACME-CAIQ-2026" {
		t.Fatalf("library source label mismatch: %q", sugg[0].SourceLabel)
	}
	if !strings.Contains(sugg[0].CanonicalText, "MFA enforced") {
		t.Fatalf("library canonical text missing: %q", sugg[0].CanonicalText)
	}
}

func TestStore_UpsertAnswer_NoLibrarySaveWhenNarrativeEmpty(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	q, _ := store.CreateQuestionnaire(ctx, CreateQuestionnaireParams{Name: "Q", SourceLabel: "s"})
	qs, _ := store.AddQuestionsFromParse(ctx, q.ID, []ParsedQuestion{
		{Code: "Q-01", Text: "MFA?", ScfAnchorID: "IAC-06"},
	})

	// SaveToLibrary=true but Narrative="" — must NOT insert a library entry.
	_, err := store.UpsertAnswer(ctx, AnswerParams{
		QuestionID:      qs[0].ID,
		AnswerValue:     "yes",
		Narrative:       "",
		AuthoredBy:      "alice@acme.test",
		SaveToLibrary:   true,
		SCFAnchorIDHint: "IAC-06",
		SourceLabel:     "src",
	})
	if err != nil {
		t.Fatalf("UpsertAnswer: %v", err)
	}
	sugg, err := store.SuggestForAnchorWithPool(ctx, "IAC-06", 10)
	if err != nil {
		t.Fatalf("SuggestForAnchorWithPool: %v", err)
	}
	if len(sugg) != 0 {
		t.Fatalf("expected 0 library entries (empty narrative), got %d", len(sugg))
	}
}

func TestStore_ListQuestionsWithAnswers_StitchesAnswers(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	q, _ := store.CreateQuestionnaire(ctx, CreateQuestionnaireParams{Name: "Q", SourceLabel: "s"})
	qs, _ := store.AddQuestionsFromParse(ctx, q.ID, []ParsedQuestion{
		{Code: "Q-01", Text: "first", ScfAnchorID: "IAC-06"},
		{Code: "Q-02", Text: "second"}, // intentionally unmapped
	})
	// Answer only the first question.
	if _, err := store.UpsertAnswer(ctx, AnswerParams{
		QuestionID:  qs[0].ID,
		AnswerValue: "yes",
		Narrative:   "see policy",
		AuthoredBy:  "alice@acme.test",
	}); err != nil {
		t.Fatalf("UpsertAnswer: %v", err)
	}

	got, err := store.ListQuestionsWithAnswers(ctx, q.ID)
	if err != nil {
		t.Fatalf("ListQuestionsWithAnswers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(got))
	}
	if got[0].Answer == nil || got[0].Answer.AnswerValue != "yes" {
		t.Fatalf("first question should have answer attached: %+v", got[0])
	}
	if got[1].Answer != nil {
		t.Fatalf("second question must NOT have an answer: %+v", got[1])
	}
	if !got[1].NeedsMapping {
		t.Fatal("second question must be flagged needs_mapping")
	}
}

// ----- RLS isolation — the canvas-§5.4 invariant -----

func TestSuggestForAnchor_RLS_TenantIsolation(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Seed a library entry inside tenant A.
	ctxA := ctxFor(t, tenantA)
	qA, _ := store.CreateQuestionnaire(ctxA, CreateQuestionnaireParams{Name: "A-Q", SourceLabel: "A"})
	qsA, _ := store.AddQuestionsFromParse(ctxA, qA.ID, []ParsedQuestion{
		{Code: "Q-01", Text: "?", ScfAnchorID: "IAC-06"},
	})
	if _, err := store.UpsertAnswer(ctxA, AnswerParams{
		QuestionID:      qsA[0].ID,
		AnswerValue:     "yes",
		Narrative:       "tenant-A confidential narrative",
		AuthoredBy:      "alice@a.test",
		SaveToLibrary:   true,
		SCFAnchorIDHint: "IAC-06",
		SourceLabel:     "tenant-A-source",
	}); err != nil {
		t.Fatalf("seed tenant-A library entry: %v", err)
	}

	// Read from tenant B's context — must see ZERO library entries.
	ctxB := ctxFor(t, tenantB)
	suggB, err := store.SuggestForAnchorWithPool(ctxB, "IAC-06", 10)
	if err != nil {
		t.Fatalf("tenant-B SuggestForAnchorWithPool: %v", err)
	}
	if len(suggB) != 0 {
		t.Fatalf("RLS LEAK: tenant B saw %d tenant-A library entries", len(suggB))
	}

	// Sanity — tenant A still sees its own.
	suggA, err := store.SuggestForAnchorWithPool(ctxA, "IAC-06", 10)
	if err != nil {
		t.Fatalf("tenant-A SuggestForAnchorWithPool: %v", err)
	}
	if len(suggA) != 1 {
		t.Fatalf("tenant A expected 1 library entry, got %d", len(suggA))
	}
}

func TestSuggestForAnchorWithPool_RejectsEmptyAnchor(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	store := NewStore(app)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	_, err := store.SuggestForAnchorWithPool(ctx, "", 10)
	if err == nil {
		t.Fatal("expected empty-anchor error")
	}
}
