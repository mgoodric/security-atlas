//go:build integration

// Slice 757 — DB-backed integration tests for the AI-reject HANDLER (AC-8).
// These drive AISuggest -> AIReject against a real qaisuggest.Store + an
// llm.StubClient (no live Ollama) and prove the reject contract:
//
//   - a rejected draft is DELETED (question returns to unanswered) and the
//     rejection is audit-logged with actor + model provenance;
//   - an approved AI answer is 409 and survives (P0-757-4);
//   - a manually-authored answer is 409 and survives (P0-757-4);
//   - an absent answer id is 404;
//   - Tenant B cannot reject Tenant A's draft (RLS-invisible -> 404, the
//     draft survives).
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/api/questionnaires/...
package questionnaires

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/qaisuggest"
)

// rejectSeedDraft drives the suggest handler to persist a real unapproved
// draft for the given tenant and returns the persisted answer id.
func rejectSeedDraft(t *testing.T, app, admin *pgxpool.Pool, tenant string) (h *Handler, answerID string) {
	t.Helper()
	qID := aiSeedQuestion(t, admin, tenant, "Do you encrypt customer data at rest?")
	polID := aiSeedPolicy(t, admin, tenant, "Encryption at rest policy", "All customer data encrypted at rest AES-256.")

	draft := "Yes, customer data is encrypted at rest (" + polID.String() + ")."
	h = aiHandler(app, draft)

	w := httptest.NewRecorder()
	h.AISuggest(w, aiReq(t, tenant, "{}", map[string]string{"qid": qID.String()}))
	if w.Code != http.StatusOK {
		t.Fatalf("AISuggest = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var sug qaisuggest.Suggestion
	if err := json.Unmarshal(w.Body.Bytes(), &sug); err != nil {
		t.Fatalf("decode suggestion: %v", err)
	}
	if sug.AnswerID == "" {
		t.Fatalf("expected a persisted draft, got %+v", sug)
	}
	return h, sug.AnswerID
}

func rejectBody(t *testing.T, answerID string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"answer_id": answerID})
	if err != nil {
		t.Fatalf("marshal reject body: %v", err)
	}
	return string(b)
}

func rejectCountAnswers(t *testing.T, admin *pgxpool.Pool, tenant, answerID string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM questionnaire_answers WHERE tenant_id = $1 AND id = $2
	`, tenant, answerID).Scan(&n); err != nil {
		t.Fatalf("count answers: %v", err)
	}
	return n
}

// TestAIReject_DiscardsDraftAndWritesAudit is the happy path: the draft row is
// deleted and the audit row records the actor + snapshot provenance.
func TestAIReject_DiscardsDraftAndWritesAudit(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)

	h, answerID := rejectSeedDraft(t, app, admin, tenant)

	w := httptest.NewRecorder()
	h.AIReject(w, aiReq(t, tenant, rejectBody(t, answerID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("AIReject = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var rejected qaisuggest.RejectedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode rejected: %v", err)
	}
	if rejected.AnswerID != answerID || rejected.Status != "rejected" {
		t.Fatalf("unexpected rejected payload: %+v", rejected)
	}

	// The draft is gone — the question is unanswered again.
	if n := rejectCountAnswers(t, admin, tenant, answerID); n != 0 {
		t.Fatalf("draft survived reject: %d rows", n)
	}

	// The audit row preserves the deleted draft's id, the actor, and the
	// snapshot-at-rejection model provenance.
	var (
		actor, action, modelName, provider string
	)
	if err := admin.QueryRow(context.Background(), `
		SELECT actor, action, model_name, model_provider
		FROM questionnaire_answer_reject_audit
		WHERE tenant_id = $1 AND answer_id = $2
	`, tenant, answerID).Scan(&actor, &action, &modelName, &provider); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if actor != "key_grc_engineer" || action != "rejected" {
		t.Errorf("audit actor/action = %q/%q, want key_grc_engineer/rejected", actor, action)
	}
	if modelName != "stub-model" || provider != "ollama-local" {
		t.Errorf("audit provenance = %q/%q, want stub-model/ollama-local", modelName, provider)
	}
}

// TestAIReject_ApprovedAnswer409 proves reject never touches an approved
// answer (P0-757-4): 409 and the row survives.
func TestAIReject_ApprovedAnswer409(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)

	h, answerID := rejectSeedDraft(t, app, admin, tenant)

	// Approve the draft first (the slice-441 one-click path).
	approveBody, _ := json.Marshal(map[string]any{
		"answer_id": answerID,
		"narrative": "Yes, encrypted at rest (approved).",
	})
	w := httptest.NewRecorder()
	h.AIApprove(w, aiReq(t, tenant, string(approveBody), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("AIApprove = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	h.AIReject(w, aiReq(t, tenant, rejectBody(t, answerID), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("AIReject (approved) = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if n := rejectCountAnswers(t, admin, tenant, answerID); n != 1 {
		t.Fatalf("approved answer must survive reject, got %d rows", n)
	}
}

// TestAIReject_ManualAnswer409 proves reject never touches a manually
// authored answer (P0-757-4): 409 and the row survives.
func TestAIReject_ManualAnswer409(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)

	qID := aiSeedQuestion(t, admin, tenant, "Do you run background checks?")
	answerID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaire_answers (id, tenant_id, question_id, narrative, authored_by)
		VALUES ($1, $2, $3, 'Yes, for all employees.', 'key_grc_engineer')
	`, answerID, tenant, qID); err != nil {
		t.Fatalf("seed manual answer: %v", err)
	}

	h := aiHandler(app, "unused")
	w := httptest.NewRecorder()
	h.AIReject(w, aiReq(t, tenant, rejectBody(t, answerID.String()), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("AIReject (manual) = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	if n := rejectCountAnswers(t, admin, tenant, answerID.String()); n != 1 {
		t.Fatalf("manual answer must survive reject, got %d rows", n)
	}
}

// TestAIReject_AbsentAnswer404 proves an unknown answer id is 404.
func TestAIReject_AbsentAnswer404(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)

	h := aiHandler(app, "unused")
	w := httptest.NewRecorder()
	h.AIReject(w, aiReq(t, tenant, rejectBody(t, uuid.New().String()), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("AIReject (absent) = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestAIReject_CrossTenantDenied proves Tenant B cannot reject Tenant A's
// draft: the id is RLS-invisible under B's context (404) and A's draft
// survives untouched.
func TestAIReject_CrossTenantDenied(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenantA := aiFreshTenant(t, admin)
	tenantB := aiFreshTenant(t, admin)

	h, answerID := rejectSeedDraft(t, app, admin, tenantA)

	w := httptest.NewRecorder()
	h.AIReject(w, aiReq(t, tenantB, rejectBody(t, answerID), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("AIReject (cross-tenant) = %d, want 404; body=%s", w.Code, w.Body.String())
	}
	if n := rejectCountAnswers(t, admin, tenantA, answerID); n != 1 {
		t.Fatalf("tenant A draft must survive tenant B reject, got %d rows", n)
	}
	// And no audit row was forged under either tenant for this answer.
	var audits int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM questionnaire_answer_reject_audit WHERE answer_id = $1
	`, answerID).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 0 {
		t.Fatalf("cross-tenant reject must not write an audit row, got %d", audits)
	}
}
