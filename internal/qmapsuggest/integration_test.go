//go:build integration

package qmapsuggest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/qmapsuggest"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

type testAnchor struct {
	UUID  string
	SCFID string
	Title string
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"ai_generations",
		"questionnaire_mapping_proposal_audit",
		"questionnaire_mapping_proposals",
		"answer_library",
		"questionnaire_answers",
		"questionnaire_questions",
		"questionnaires",
	)
}

func seededAnchor(t *testing.T, admin *pgxpool.Pool) testAnchor {
	t.Helper()
	var a testAnchor
	err := admin.QueryRow(context.Background(), `
		SELECT a.id::text, a.scf_id, a.title
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.status = 'current'
		  AND (a.title ILIKE '%access%' OR a.description ILIKE '%access%')
		ORDER BY a.scf_id
		LIMIT 1
	`).Scan(&a.UUID, &a.SCFID, &a.Title)
	if err != nil {
		t.Skipf("current seeded SCF catalog not available: %v", err)
	}
	return a
}

func seedQuestion(t *testing.T, admin *pgxpool.Pool, tenant, text string) uuid.UUID {
	t.Helper()
	qnID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaires (id, tenant_id, name)
		VALUES ($1, $2, 'slice 755 test questionnaire')
	`, qnID, tenant); err != nil {
		t.Fatalf("seed questionnaire: %v", err)
	}
	qID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaire_questions (id, tenant_id, questionnaire_id, code, text, sort_order)
		VALUES ($1, $2, $3, 'Q1', $4, 1)
	`, qID, tenant, qnID, text); err != nil {
		t.Fatalf("seed question: %v", err)
	}
	return qID
}

func serviceWith(app *pgxpool.Pool, draft string) *qmapsuggest.Service {
	store := qmapsuggest.NewStore(app)
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}
	return qmapsuggest.NewService(store, client, store, llm.NewAuditWriter(app))
}

func TestSuggest_FabricatedAnchorSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	anchor := seededAnchor(t, admin)
	tenant := freshTenant(t, admin)
	qID := seedQuestion(t, admin, tenant, "Do you maintain access control for privileged users?")

	out, err := serviceWith(app, `{"scf_id":"IAC-99","rationale":"Looks like identity access."}`).
		Suggest(dbtest.WithTenantCtx(t, tenant), qmapsuggest.SuggestParams{QuestionID: qID, Actor: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed || out.Reason != qmapsuggest.ReasonOutOfGrounding {
		t.Fatalf("want out_of_grounding suppression, got %+v (anchor fixture %s)", out, anchor.SCFID)
	}
	store := qmapsuggest.NewStore(app)
	n, err := store.CountProposals(dbtest.WithTenantCtx(t, tenant))
	if err != nil {
		t.Fatalf("CountProposals: %v", err)
	}
	if n != 0 {
		t.Fatalf("suppressed suggestion persisted %d proposals", n)
	}
}

func TestApprove_GoAndDBApproverGuards(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	anchor := seededAnchor(t, admin)
	tenant := freshTenant(t, admin)
	qID := seedQuestion(t, admin, tenant, "Do you maintain access control for privileged users?")

	svc := serviceWith(app, `{"scf_id":"`+anchor.SCFID+`","rationale":"The question asks about access control."}`)
	out, err := svc.Suggest(dbtest.WithTenantCtx(t, tenant), qmapsuggest.SuggestParams{QuestionID: qID, Actor: "key_grc"})
	if err != nil || out.ProposalID == "" {
		t.Fatalf("Suggest: out=%+v err=%v", out, err)
	}
	if _, err := svc.Approve(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(out.ProposalID), ""); !errors.Is(err, qmapsuggest.ErrApproverRequired) {
		t.Fatalf("blank approver: want ErrApproverRequired, got %v", err)
	}

	_, err = admin.Exec(context.Background(), `
		INSERT INTO questionnaire_mapping_proposals
			(id, tenant_id, question_id, scf_anchor_uuid, scf_anchor_id,
			 scf_anchor_title, rationale, candidate_anchor_ids, status,
			 ai_assisted, human_approved, human_approver,
			 prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, $3, $4, $5,
		        $6, 'bad approved shape', '[]'::jsonb, 'approved',
		        TRUE, TRUE, NULL,
		        'qmapsuggest-v0', 'stub', '1', 'ollama-local')
	`, uuid.New(), tenant, qID, anchor.UUID, anchor.SCFID, anchor.Title)
	if err == nil {
		t.Fatal("DB CHECK accepted ai_assisted+approved without human_approver")
	}
}

func TestApprove_CanonicalThereafter(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	anchor := seededAnchor(t, admin)
	tenant := freshTenant(t, admin)
	qID := seedQuestion(t, admin, tenant, "Do you maintain access control for privileged users?")

	svc := serviceWith(app, `{"scf_id":"`+anchor.SCFID+`","rationale":"The question asks about access control."}`)
	out, err := svc.Suggest(dbtest.WithTenantCtx(t, tenant), qmapsuggest.SuggestParams{QuestionID: qID, Actor: "key_grc"})
	if err != nil || out.ProposalID == "" {
		t.Fatalf("Suggest: out=%+v err=%v", out, err)
	}
	approved, err := svc.Approve(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(out.ProposalID), "key_grc")
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.NeedsMapping || approved.SCFAnchorID != anchor.SCFID || !approved.HumanApproved {
		t.Fatalf("approval did not canonicalize: %+v", approved)
	}
	var storedAnchor *string
	if err := admin.QueryRow(context.Background(), `
		SELECT scf_anchor_id FROM questionnaire_questions WHERE id = $1
	`, qID).Scan(&storedAnchor); err != nil {
		t.Fatalf("read question anchor: %v", err)
	}
	if storedAnchor == nil || *storedAnchor != anchor.SCFID {
		t.Fatalf("question anchor = %v, want %s", storedAnchor, anchor.SCFID)
	}

	qstore := questionnaire.NewStore(app)
	if _, err := qstore.UpsertAnswer(dbtest.WithTenantCtx(t, tenant), questionnaire.AnswerParams{
		QuestionID:      qID.String(),
		Narrative:       "Access control answer now eligible for reuse.",
		AuthoredBy:      "key_grc",
		SaveToLibrary:   true,
		SCFAnchorIDHint: anchor.SCFID,
		SourceLabel:     "slice 755 canonical",
	}); err != nil {
		t.Fatalf("UpsertAnswer: %v", err)
	}
	suggestions, err := qstore.SuggestForAnchorWithPool(dbtest.WithTenantCtx(t, tenant), anchor.SCFID, 10)
	if err != nil {
		t.Fatalf("SuggestForAnchorWithPool: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("canonical mapping did not make answer-library lookup eligible")
	}
}

func TestRLS_TenantBCannotReadOrApproveTenantAProposal(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	anchor := seededAnchor(t, admin)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	qA := seedQuestion(t, admin, tenantA, "Do you maintain access control for privileged users?")

	svc := serviceWith(app, `{"scf_id":"`+anchor.SCFID+`","rationale":"The question asks about access control."}`)
	out, err := svc.Suggest(dbtest.WithTenantCtx(t, tenantA), qmapsuggest.SuggestParams{QuestionID: qA, Actor: "key_grc"})
	if err != nil || out.ProposalID == "" {
		t.Fatalf("Suggest tenant A: out=%+v err=%v", out, err)
	}
	store := qmapsuggest.NewStore(app)
	if _, err := store.GetProposal(dbtest.WithTenantCtx(t, tenantB), uuid.MustParse(out.ProposalID)); !errors.Is(err, qmapsuggest.ErrProposalNotFound) {
		t.Fatalf("tenant B GetProposal: want ErrProposalNotFound, got %v", err)
	}
	if _, err := svc.Approve(dbtest.WithTenantCtx(t, tenantB), uuid.MustParse(out.ProposalID), "key_grc_b"); !errors.Is(err, qmapsuggest.ErrProposalNotFound) {
		t.Fatalf("tenant B Approve: want ErrProposalNotFound, got %v", err)
	}
}
