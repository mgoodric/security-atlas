//go:build integration

// Slice 441 — DB-backed integration tests for the AI suggest/approve HANDLERS.
// These drive AISuggest + AIApprove directly (in-package) against a real
// qaisuggest.Store + an llm.StubClient (the slice-498 CI seam — NO live
// Ollama), so the handler happy paths (draft suggestion -> approve) get
// coverage without the production server's Ollama dependency. The full-server
// wiring + the constitutional RLS/cross-tenant proofs live in
// internal/qaisuggest/integration_test.go.
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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func aiAppDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func aiAdminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func aiPool(t *testing.T, dsn string) *pgxpool.Pool {
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

func aiFreshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
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

func aiSeedQuestion(t *testing.T, admin *pgxpool.Pool, tenant, text string) uuid.UUID {
	t.Helper()
	qnID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO questionnaires (id, tenant_id, name) VALUES ($1, $2, 'ai handler test')
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

func aiSeedPolicy(t *testing.T, admin *pgxpool.Pool, tenant, title, body string) uuid.UUID {
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

// aiHandler builds a Handler wired with a real DB store + a StubClient that
// returns the given crafted draft.
func aiHandler(app *pgxpool.Pool, draft string) *Handler {
	qstore := questionnaire.NewStore(app)
	qaiStore := qaisuggest.NewStore(app)
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}
	svc := qaisuggest.NewService(qaiStore, client, qaiStore, qaiStore)
	return NewWithSuggest(qstore, svc)
}

// aiReq builds a POST request carrying the tenant GUC + an approver credential.
func aiReq(t *testing.T, tenant, body string, params map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	ctx, err := tenancy.WithTenant(r.Context(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	ctx = authctx.WithCredential(ctx, credstore.Credential{
		ID: "key_grc_engineer", TenantID: tenant, IsApprover: true,
	})
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

// TestAIHandlers_DraftThenApprove drives the handler happy path: suggest a
// cited draft, then approve it.
func TestAIHandlers_DraftThenApprove(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)

	qID := aiSeedQuestion(t, admin, tenant, "Do you encrypt customer data at rest?")
	polID := aiSeedPolicy(t, admin, tenant, "Encryption at rest policy", "All customer data encrypted at rest AES-256.")

	draft := "Yes, customer data is encrypted at rest (" + polID.String() + ")."
	h := aiHandler(app, draft)

	// === suggest ===
	w := httptest.NewRecorder()
	h.AISuggest(w, aiReq(t, tenant, "{}", map[string]string{"qid": qID.String()}))
	if w.Code != http.StatusOK {
		t.Fatalf("AISuggest = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var sug qaisuggest.Suggestion
	if err := json.Unmarshal(w.Body.Bytes(), &sug); err != nil {
		t.Fatalf("decode suggestion: %v", err)
	}
	if sug.Suppressed || sug.InsufficientEvidence || sug.AnswerID == "" {
		t.Fatalf("expected a drafted suggestion, got %+v", sug)
	}
	if len(sug.Citations) != 1 {
		t.Fatalf("expected 1 citation, got %d", len(sug.Citations))
	}

	// === approve ===
	approveBody, _ := json.Marshal(map[string]any{
		"answer_id": sug.AnswerID,
		"narrative": "Yes, encrypted at rest (approved).",
	})
	w = httptest.NewRecorder()
	h.AIApprove(w, aiReq(t, tenant, string(approveBody), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("AIApprove = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var approved qaisuggest.ApprovedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approved: %v", err)
	}
	if !approved.HumanApproved || approved.HumanApprover != "key_grc_engineer" {
		t.Fatalf("approval did not record approver: %+v", approved)
	}
}

// TestAIHandlers_Insufficient drives the no-candidate insufficient path
// through the handler (no Ollama call — the service short-circuits).
func TestAIHandlers_Insufficient(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)

	qID := aiSeedQuestion(t, admin, tenant, "Do you maintain a lattice-cryptography roadmap?")
	h := aiHandler(app, "unused")

	w := httptest.NewRecorder()
	h.AISuggest(w, aiReq(t, tenant, "{}", map[string]string{"qid": qID.String()}))
	if w.Code != http.StatusOK {
		t.Fatalf("AISuggest = %d, want 200", w.Code)
	}
	var sug qaisuggest.Suggestion
	_ = json.Unmarshal(w.Body.Bytes(), &sug)
	if !sug.InsufficientEvidence {
		t.Fatalf("expected insufficient-evidence, got %+v", sug)
	}
}

// TestAIHandlers_ApproveNotFound drives the approve not-found path.
func TestAIHandlers_ApproveNotFound(t *testing.T) {
	app := aiPool(t, aiAppDSN(t))
	admin := aiPool(t, aiAdminDSN(t))
	tenant := aiFreshTenant(t, admin)
	h := aiHandler(app, "unused")

	body, _ := json.Marshal(map[string]any{
		"answer_id": uuid.New().String(),
		"narrative": "x",
	})
	w := httptest.NewRecorder()
	h.AIApprove(w, aiReq(t, tenant, string(body), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("AIApprove (missing) = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// Keep time import used even if a future edit drops the only reference.
var _ = time.Second
