//go:build integration

package questionnaire

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func answerRunDSN(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skip(name + " not set; skipping integration test")
	}
	return v
}

func answerRunPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func answerRunTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM questionnaire_answer_run_items WHERE tenant_id = $1`,
			`DELETE FROM questionnaire_answer_runs WHERE tenant_id = $1`,
			`DELETE FROM questionnaire_answers WHERE tenant_id = $1`,
			`DELETE FROM questionnaire_questions WHERE tenant_id = $1`,
			`DELETE FROM questionnaires WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func seedQuestionnaire(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaires (id, tenant_id, name)
		VALUES ($1, $2, 'batch answer run test')
	`, id, tenant); err != nil {
		t.Fatalf("seed questionnaire: %v", err)
	}
	return id
}

func seedQuestion(t *testing.T, admin *pgxpool.Pool, tenant string, qnID uuid.UUID, code, text string, sort int, mapped bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var anchor any
	if mapped {
		anchor = "IAC-06"
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaire_questions
			(id, tenant_id, questionnaire_id, code, text, scf_anchor_id, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, tenant, qnID, code, text, anchor, sort); err != nil {
		t.Fatalf("seed question %s: %v", code, err)
	}
	return id
}

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

func seedManualAnswer(t *testing.T, admin *pgxpool.Pool, tenant string, qID uuid.UUID) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaire_answers
			(id, tenant_id, question_id, answer_value, narrative, authored_by)
		VALUES ($1, $2, $3, 'yes', 'manual answer', 'seed')
	`, uuid.New(), tenant, qID); err != nil {
		t.Fatalf("seed answer: %v", err)
	}
}

type sequenceClient struct {
	mu      sync.Mutex
	results []llm.GenerateResult
}

func (c *sequenceClient) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.results) == 0 {
		return llm.GenerateResult{}, errors.New("sequence exhausted")
	}
	out := c.results[0]
	c.results = c.results[1:]
	return out, nil
}

func answerRunService(app *pgxpool.Pool, client llm.Client) *AnswerRunService {
	qaiStore := qaisuggest.NewStore(app)
	qaiSvc := qaisuggest.NewService(qaiStore, client, qaiStore, qaiStore)
	return NewAnswerRunService(NewAnswerRunStore(app), qaiSvc)
}

func TestAnswerRun_MixedOutcomesAndRerunSkips(t *testing.T) {
	app := answerRunPool(t, answerRunDSN(t, "DATABASE_URL_APP"))
	admin := answerRunPool(t, answerRunDSN(t, "DATABASE_URL"))
	tenant := answerRunTenant(t, admin)
	qnID := seedQuestionnaire(t, admin, tenant)
	draftPolicy := seedPolicy(t, admin, tenant, "Encryption at rest", "Customer data is encrypted at rest using AES-256.")
	_ = seedPolicy(t, admin, tenant, "MFA policy", "Administrative access requires MFA.")

	qDraft := seedQuestion(t, admin, tenant, qnID, "Q1", "Do you encrypt customer data at rest?", 1, true)
	qNeedsMapping := seedQuestion(t, admin, tenant, qnID, "Q2", "Do you maintain a vendor access process?", 2, false)
	qAnswered := seedQuestion(t, admin, tenant, qnID, "Q3", "Do you already have a manual answer?", 3, true)
	qInsufficient := seedQuestion(t, admin, tenant, qnID, "Q4", "Do you maintain a lattice cryptography roadmap?", 4, true)
	qSuppressed := seedQuestion(t, admin, tenant, qnID, "Q5", "Do administrators use MFA?", 5, true)
	seedManualAnswer(t, admin, tenant, qAnswered)

	client := &sequenceClient{results: []llm.GenerateResult{
		{Text: "Yes, customer data is encrypted at rest (" + draftPolicy.String() + ").", ModelName: "stub", ModelVersion: "1", ModelProvider: "stub"},
		{Text: "Yes, MFA is required (" + uuid.NewString() + ").", ModelName: "stub", ModelVersion: "1", ModelProvider: "stub"},
	}}
	svc := answerRunService(app, client)
	got, err := svc.Start(tenantCtx(t, tenant), qnID, "key_grc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got.Run.Status != AnswerRunStatusCompleted {
		t.Fatalf("status = %s, want completed", got.Run.Status)
	}
	if got.Run.DraftedCount != 1 || got.Run.InsufficientCount != 1 || got.Run.SuppressedCount != 1 || got.Run.SkippedCount != 2 {
		t.Fatalf("unexpected counts: %+v", got.Run)
	}
	byQ := map[string]AnswerRunItem{}
	for _, item := range got.Items {
		byQ[item.QuestionID] = item
	}
	assertOutcome := func(q uuid.UUID, outcome string) {
		t.Helper()
		if byQ[q.String()].Outcome != outcome {
			t.Fatalf("question %s outcome = %q, want %q; all=%+v", q, byQ[q.String()].Outcome, outcome, got.Items)
		}
	}
	assertOutcome(qDraft, AnswerRunOutcomeDrafted)
	assertOutcome(qNeedsMapping, AnswerRunOutcomeSkippedNeedsMapping)
	assertOutcome(qAnswered, AnswerRunOutcomeSkippedAlreadyAnswered)
	assertOutcome(qInsufficient, AnswerRunOutcomeInsufficientEvidence)
	assertOutcome(qSuppressed, AnswerRunOutcomeSuppressed)

	var approved, guardViolations int
	if err := admin.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE ai_assisted AND human_approved)::int,
			count(*) FILTER (WHERE ai_assisted AND human_approved AND COALESCE(human_approver, '') = '')::int
		FROM questionnaire_answers
		WHERE tenant_id = $1
	`, tenant).Scan(&approved, &guardViolations); err != nil {
		t.Fatalf("approval invariant query: %v", err)
	}
	if approved != 0 || guardViolations != 0 {
		t.Fatalf("approved=%d guardViolations=%d, want zero", approved, guardViolations)
	}
	var suppressedAnswers int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*)::int FROM questionnaire_answers WHERE tenant_id = $1 AND question_id = $2
	`, tenant, qSuppressed).Scan(&suppressedAnswers); err != nil {
		t.Fatalf("suppressed answer query: %v", err)
	}
	if suppressedAnswers != 0 {
		t.Fatalf("suppressed row persisted %d answers, want 0", suppressedAnswers)
	}

	rerunClient := &sequenceClient{results: []llm.GenerateResult{
		{Text: "Yes, MFA is required (" + draftPolicy.String() + ").", ModelName: "stub", ModelVersion: "1", ModelProvider: "stub"},
	}}
	rerunSvc := answerRunService(app, rerunClient)
	rerun, err := rerunSvc.Start(tenantCtx(t, tenant), qnID, "key_grc")
	if err != nil {
		t.Fatalf("rerun Start: %v", err)
	}
	for _, item := range rerun.Items {
		if item.QuestionID == qDraft.String() && item.Outcome != AnswerRunOutcomeSkippedAlreadyAnswered {
			t.Fatalf("rerun drafted question outcome = %s, want skipped_already_answered", item.Outcome)
		}
	}
}

func TestAnswerRun_TwoTenantIsolation(t *testing.T) {
	app := answerRunPool(t, answerRunDSN(t, "DATABASE_URL_APP"))
	admin := answerRunPool(t, answerRunDSN(t, "DATABASE_URL"))
	tenantA := answerRunTenant(t, admin)
	tenantB := answerRunTenant(t, admin)
	qnA := seedQuestionnaire(t, admin, tenantA)
	qnB := seedQuestionnaire(t, admin, tenantB)
	polA := seedPolicy(t, admin, tenantA, "Encryption at rest", "Tenant A secret phrase alpha.")
	polB := seedPolicy(t, admin, tenantB, "Encryption at rest", "Tenant B independent phrase beta.")
	seedQuestion(t, admin, tenantA, qnA, "Q1", "Do you encrypt customer data at rest?", 1, true)
	seedQuestion(t, admin, tenantB, qnB, "Q1", "Do you encrypt customer data at rest?", 1, true)

	runA, err := answerRunService(app, &sequenceClient{results: []llm.GenerateResult{
		{Text: "Yes, encryption is enforced (" + polA.String() + ").", ModelName: "stub", ModelVersion: "1", ModelProvider: "stub"},
	}}).Start(tenantCtx(t, tenantA), qnA, "key_grc")
	if err != nil {
		t.Fatalf("tenant A Start: %v", err)
	}
	runB, err := answerRunService(app, &sequenceClient{results: []llm.GenerateResult{
		{Text: "Yes, encryption is enforced (" + polB.String() + ").", ModelName: "stub", ModelVersion: "1", ModelProvider: "stub"},
	}}).Start(tenantCtx(t, tenantB), qnB, "key_grc")
	if err != nil {
		t.Fatalf("tenant B Start: %v", err)
	}
	if _, err := NewAnswerRunStore(app).GetDetail(tenantCtx(t, tenantA), uuid.MustParse(runB.Run.ID)); !errors.Is(err, ErrAnswerRunNotFound) {
		t.Fatalf("tenant A read tenant B run err = %v, want ErrAnswerRunNotFound", err)
	}
	var tenantBText string
	if err := admin.QueryRow(context.Background(), `
		SELECT narrative FROM questionnaire_answers WHERE tenant_id = $1
	`, tenantB).Scan(&tenantBText); err != nil {
		t.Fatalf("tenant B answer: %v", err)
	}
	if strings.Contains(tenantBText, polA.String()) || strings.Contains(tenantBText, "Tenant A secret") {
		t.Fatalf("tenant B draft leaked tenant A material: %q", tenantBText)
	}
	if len(runA.Items) != 1 || len(runB.Items) != 1 || runB.Items[0].Outcome != AnswerRunOutcomeDrafted {
		t.Fatalf("unexpected run items A=%+v B=%+v", runA.Items, runB.Items)
	}
}

func TestAnswerRun_SecondActiveRunConflicts(t *testing.T) {
	app := answerRunPool(t, answerRunDSN(t, "DATABASE_URL_APP"))
	admin := answerRunPool(t, answerRunDSN(t, "DATABASE_URL"))
	tenant := answerRunTenant(t, admin)
	qnID := seedQuestionnaire(t, admin, tenant)
	store := NewAnswerRunStore(app)
	if _, err := store.CreateRun(tenantCtx(t, tenant), qnID, "first", AnswerRunRowCap); err != nil {
		t.Fatalf("CreateRun first: %v", err)
	}
	_, err := store.CreateRun(tenantCtx(t, tenant), qnID, "second", AnswerRunRowCap)
	if !errors.Is(err, ErrActiveAnswerRun) {
		t.Fatalf("CreateRun second err = %v, want ErrActiveAnswerRun", err)
	}
}
