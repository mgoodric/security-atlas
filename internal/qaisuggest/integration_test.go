//go:build integration

// Slice 441 — integration tests for the AI questionnaire-answer suggestion
// surface. Real Postgres + the real qaisuggest.Store (RLS-backed keyword
// retrieval + citation resolution + draft persistence + approval) + the
// llm.StubClient (NO live Ollama — the slice-498 CI seam). The stub lets us
// craft a VALID draft (cites a real tenant-owned id), a FABRICATED draft
// (cites a non-existent / cross-tenant id), or the insufficient sentinel, and
// assert the deterministic citation-validation + suppression + persistence +
// cross-tenant + approval-guard behavior.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/qaisuggest/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (AC-13..AC-17):
//
//	AC-13  a valid suggestion with a resolvable citation reaches draft state
//	AC-14  a draft citing a non-existent id is rejected (threat-model T)
//	AC-15  a question with no backing evidence returns insufficient (AC-5)
//	AC-16  a Tenant-B suggestion cannot cite a Tenant-A record (cross-tenant)
//	AC-17  an approved answer requires human_approver (the DB guard)
package qaisuggest_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a new tenant id + registers cleanup of the rows this
// slice's tests create (children first for FK order). Pure tenant-scoped
// DELETE, so it delegates to dbtest.SeedTenant (slice 435 / 742 drain).
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"questionnaire_answers",
		"questionnaire_questions",
		"questionnaires",
		"evidence_records",
		"controls",
		"policies",
	)
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedQuestion inserts a questionnaire + one question, returns the question id.
func seedQuestion(t *testing.T, admin *pgxpool.Pool, tenant, text string) uuid.UUID {
	t.Helper()
	qnID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaires (id, tenant_id, name)
		VALUES ($1, $2, 'slice 441 test questionnaire')
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

// seedPolicy inserts an approved policy whose title/body match the keyword, so
// the keyword first-pass surfaces it as a candidate. Returns the policy id.
func seedPolicy(t *testing.T, admin *pgxpool.Pool, tenant, title, body string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO policies
			(id, tenant_id, title, body_md, status, owner_role, approver_role, created_by)
		VALUES ($1, $2, $3, $4, 'approved', 'grc_engineer', 'ciso', 'key_seed')
	`, id, tenant, title, body); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	return id
}

// stubWith builds a Service whose model returns the given crafted draft.
func stubWith(store *qaisuggest.Store, draft string) *qaisuggest.Service {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}
	return qaisuggest.NewService(store, client, store, store)
}

// ----- AC-13: valid draft with a resolvable citation reaches draft state -----

func TestSuggest_ValidCitationDrafts(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	qID := seedQuestion(t, admin, tenant, "Do you encrypt customer data at rest?")
	polID := seedPolicy(t, admin, tenant, "Encryption at rest policy", "All customer data is encrypted at rest with AES-256.")

	store := qaisuggest.NewStore(app)
	draft := "Yes, customer data is encrypted at rest with AES-256 (" + polID.String() + ")."
	svc := stubWith(store, draft)

	out, err := svc.Suggest(tenantCtx(t, tenant), qaisuggest.SuggestParams{QuestionID: qID, AuthoredBy: "key_grc"})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Suppressed || out.InsufficientEvidence {
		t.Fatalf("expected a drafted suggestion, got %+v", out)
	}
	if out.AnswerID == "" || out.Draft != draft {
		t.Fatalf("draft not surfaced: %+v", out)
	}
	if len(out.Citations) != 1 || out.Citations[0].ID != polID.String() {
		t.Fatalf("expected the policy citation resolved, got %+v", out.Citations)
	}

	// The persisted draft is ai_assisted + NOT approved (AC-6).
	ansID := uuid.MustParse(out.AnswerID)
	row, err := store.GetAnswer(tenantCtx(t, tenant), ansID)
	if err != nil {
		t.Fatalf("GetAnswer: %v", err)
	}
	if !row.AiAssisted {
		t.Error("draft must be ai_assisted=TRUE")
	}
	if row.HumanApproved {
		t.Error("a fresh draft must be unapproved (P0-441-1)")
	}
	if row.HumanApprover != nil {
		t.Error("a fresh draft must have NULL human_approver")
	}
	if row.ModelProvider == "" || row.PromptVersion == "" {
		t.Error("model provenance must be persisted (R-mitigation)")
	}
}

// seedEvidence inserts a control + a passing evidence record whose control
// title matches the keyword, so the keyword first-pass surfaces the evidence
// as a candidate. Returns the evidence id.
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant, controlTitle string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr, freshness_class
		)
		VALUES ($1, $2, $3, 'AAA', 'automated', $4, '[]'::jsonb, 'true', 'quarterly')
	`, ctrlID, tenant, controlTitle, "bundle-441-"+ctrlID.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	evID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, ingested_at,
			provenance, result, payload, hash, evidence_kind
		)
		VALUES ($1, $2, $3, $4, now(), now(),
			$5, 'pass', '{}'::jsonb, $6, 'access_review.completion')
	`, evID, tenant, ctrlID, ctrlID.String(),
		`{"connector_id":"test-connector"}`, "hash-441-"+evID.String()[:8]); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return evID
}

// ----- AC-13 (evidence): a draft citing a passing evidence record drafts -----

func TestSuggest_EvidenceCitationDrafts(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	qID := seedQuestion(t, admin, tenant, "Do you run periodic access reviews?")
	evID := seedEvidence(t, admin, tenant, "Periodic access review control")

	store := qaisuggest.NewStore(app)
	draft := "Yes, we run periodic access reviews; latest completion (" + evID.String() + ")."
	svc := stubWith(store, draft)

	out, err := svc.Suggest(tenantCtx(t, tenant), qaisuggest.SuggestParams{QuestionID: qID})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if out.Suppressed || out.InsufficientEvidence {
		t.Fatalf("expected an evidence-backed draft, got %+v", out)
	}
	if len(out.Citations) != 1 || out.Citations[0].Kind != qaisuggest.KindEvidence {
		t.Fatalf("expected one evidence citation, got %+v", out.Citations)
	}
}

// ----- AC-14: a non-existent-id citation is rejected -> suppressed -----

func TestSuggest_FabricatedCitationRejected(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	qID := seedQuestion(t, admin, tenant, "Do you encrypt data at rest?")
	seedPolicy(t, admin, tenant, "Encryption at rest policy", "Encrypted at rest.")

	store := qaisuggest.NewStore(app)
	// The model cites a fabricated policy id that exists nowhere — even though
	// a real candidate WAS retrieved, the grounding+ownership gate rejects it.
	fabricated := uuid.New()
	draft := "Yes, we encrypt (" + fabricated.String() + ")."
	svc := stubWith(store, draft)

	out, err := svc.Suggest(tenantCtx(t, tenant), qaisuggest.SuggestParams{QuestionID: qID})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed || out.Reason != qaisuggest.ReasonUnresolvedCitation {
		t.Fatalf("expected suppression for a fabricated id (AC-14 / P0-441-2), got %+v", out)
	}
	if out.AnswerID != "" {
		t.Fatal("a suppressed draft must NOT be persisted (P0-441-4)")
	}
	if out.Draft != "" {
		t.Error("suppressed suggestion must not surface text")
	}
}

// ----- AC-15: no backing material -> insufficient, not fabricated -----

func TestSuggest_NoEvidenceInsufficient(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	// A question whose keywords match NO policy/evidence in the tenant.
	qID := seedQuestion(t, admin, tenant, "Do you maintain a quantum-resistant lattice cryptography roadmap?")

	store := qaisuggest.NewStore(app)
	// The stub would happily return a draft, but the service never calls it:
	// with zero candidates the surface short-circuits to insufficient (AC-5).
	svc := stubWith(store, "Yes (some-id).")

	out, err := svc.Suggest(tenantCtx(t, tenant), qaisuggest.SuggestParams{QuestionID: qID})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.InsufficientEvidence {
		t.Fatalf("expected insufficient-evidence (no-fabricated-coverage, AC-5), got %+v", out)
	}
	if out.AnswerID != "" {
		t.Fatal("insufficient-evidence must persist nothing (P0-441-2)")
	}
}

// ----- AC-16: cross-tenant isolation (THE headline) -----
//
// Tenant A owns a policy. The model (for a Tenant-B request) produces a draft
// that cites Tenant A's policy id. Under Tenant B's RLS context the Tenant-A
// id is invisible, so the resolver cannot confirm it tenant-owned and the
// suggestion is SUPPRESSED. A Tenant-B suggestion can never cite a Tenant-A
// record (threat-model I, P0-441-3).
func TestSuggest_CrossTenantCitationSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant A's policy.
	polA := seedPolicy(t, admin, tenantA, "Encryption at rest policy", "Tenant A encrypts at rest.")

	// Tenant B's own question + a policy so B has real candidates to draft from.
	qB := seedQuestion(t, admin, tenantB, "Do you encrypt data at rest?")
	seedPolicy(t, admin, tenantB, "Encryption at rest policy", "Tenant B encrypts at rest.")

	store := qaisuggest.NewStore(app)
	// The model, serving Tenant B's request, leaks Tenant A's policy id polA.
	draft := "Yes, see cross-tenant policy (" + polA.String() + ")."
	svc := stubWith(store, draft)

	out, err := svc.Suggest(tenantCtx(t, tenantB), qaisuggest.SuggestParams{QuestionID: qB})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !out.Suppressed {
		t.Fatal("CROSS-TENANT LEAK: a Tenant-B suggestion cited a Tenant-A record without suppression (AC-16 / P0-441-3)")
	}
	if out.Reason != qaisuggest.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", out.Reason, qaisuggest.ReasonUnresolvedCitation)
	}
	if out.AnswerID != "" {
		t.Fatal("cross-tenant suppression must persist nothing")
	}

	// Defense-in-depth on the AC-16 mechanism: the leaked id is genuinely
	// invisible to Tenant B at the resolver level, and visible to Tenant A.
	if _, ok, rerr := store.Resolve(tenantCtx(t, tenantB), polA); rerr != nil {
		t.Fatalf("Resolve(tenantB, polA): %v", rerr)
	} else if ok {
		t.Fatal("CROSS-TENANT LEAK: Tenant B resolved Tenant A's policy id")
	}
	if _, ok, rerr := store.Resolve(tenantCtx(t, tenantA), polA); rerr != nil {
		t.Fatalf("Resolve(tenantA, polA): %v", rerr)
	} else if !ok {
		t.Fatal("Tenant A could not resolve its own policy id — resolver is broken")
	}

	// And prove the keyword retrieval itself never surfaces Tenant A's policy
	// to Tenant B (the retrieval leg of the isolation guarantee).
	cands, rerr := store.RetrieveCandidates(tenantCtx(t, tenantB), []string{"encryption", "rest"})
	if rerr != nil {
		t.Fatalf("RetrieveCandidates(tenantB): %v", rerr)
	}
	for _, c := range cands {
		if c.ID == polA.String() {
			t.Fatal("CROSS-TENANT LEAK: Tenant A's policy surfaced in Tenant B's candidate set")
		}
	}
}

// ----- AC-17: approval requires human_approver (the DB guard) -----

func TestApprove_RequiresApprover(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	qID := seedQuestion(t, admin, tenant, "Do you encrypt data at rest?")
	polID := seedPolicy(t, admin, tenant, "Encryption at rest policy", "Encrypted at rest.")

	store := qaisuggest.NewStore(app)
	draft := "Yes, encrypted at rest (" + polID.String() + ")."
	svc := stubWith(store, draft)

	out, err := svc.Suggest(tenantCtx(t, tenant), qaisuggest.SuggestParams{QuestionID: qID})
	if err != nil || out.AnswerID == "" {
		t.Fatalf("Suggest did not draft: %+v err=%v", out, err)
	}
	ansID := uuid.MustParse(out.AnswerID)

	// Blank approver is rejected at the service (Go mirror of the DB guard).
	if _, err := svc.Approve(tenantCtx(t, tenant), qaisuggest.ApproveParams{
		AnswerID: ansID, Narrative: "Yes.", Approver: "",
	}); err == nil {
		t.Fatal("blank-approver approval must be rejected (P0-441-8)")
	}

	// The draft is still unapproved after the rejected attempt.
	row, err := store.GetAnswer(tenantCtx(t, tenant), ansID)
	if err != nil {
		t.Fatalf("GetAnswer: %v", err)
	}
	if row.HumanApproved {
		t.Fatal("a rejected approval must leave the draft unapproved")
	}

	// A real approver succeeds and records the approver.
	approved, err := svc.Approve(tenantCtx(t, tenant), qaisuggest.ApproveParams{
		AnswerID: ansID, Narrative: "Yes, encrypted at rest.", Approver: "key_grc_engineer",
	})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !approved.HumanApproved || approved.HumanApprover != "key_grc_engineer" {
		t.Fatalf("approval did not record the approver: %+v", approved)
	}

	// And the persisted row reflects it (the DB CHECK accepted it because the
	// approver is present).
	row2, err := store.GetAnswer(tenantCtx(t, tenant), ansID)
	if err != nil {
		t.Fatalf("GetAnswer (post-approve): %v", err)
	}
	if !row2.HumanApproved || row2.HumanApprover == nil || *row2.HumanApprover != "key_grc_engineer" {
		t.Fatalf("persisted approval state wrong: approved=%v approver=%v", row2.HumanApproved, row2.HumanApprover)
	}
}

// ----- DB CHECK direct proof: human_approved without approver is impossible --

func TestDBGuard_RejectsApprovedWithoutApprover(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	qID := seedQuestion(t, admin, tenant, "Do you encrypt at rest?")

	// Attempt to INSERT the forbidden shape directly via the admin role
	// (BYPASSRLS) — the CHECK must reject it regardless of RLS. ai_assisted +
	// human_approved with a NULL human_approver is the one forbidden row.
	_, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaire_answers
			(id, tenant_id, question_id, ai_assisted, human_approved, human_approver,
			 prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, $3, TRUE, TRUE, NULL,
			'qaisuggest-v0', 'stub', '1', 'ollama-local')
	`, uuid.New(), tenant, qID)
	if err == nil {
		t.Fatal("DB CHECK FAILED TO FIRE: an ai_assisted+approved row with NULL approver was accepted (P0-441-8)")
	}

	// The same row WITH an approver is accepted.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaire_answers
			(id, tenant_id, question_id, ai_assisted, human_approved, human_approver,
			 prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, $3, TRUE, TRUE, 'key_grc',
			'qaisuggest-v0', 'stub', '1', 'ollama-local')
	`, uuid.New(), tenant, qID); err != nil {
		t.Fatalf("an approved row WITH an approver must be accepted: %v", err)
	}
}
