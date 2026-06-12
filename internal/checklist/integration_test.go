//go:build integration

// Slice 471 — integration tests for the role-scoped checklist generator. Real
// Postgres + the real checklist.Store (RLS-backed control read + role-split +
// citation resolution + section/item persistence + approval) + the
// llm.StubClient (NO live Ollama — the slice-498 CI seam). The stub lets us
// craft a VALID draft (cites a real tenant-owned control), a FABRICATED draft
// (cites a non-existent / cross-tenant id), and assert the deterministic
// citation-validation + suppression + persistence + cross-tenant + approval-
// guard behaviour.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/checklist/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (AC-13..AC-17):
//
//	AC-13  a valid section with a resolvable control citation persists unapproved
//	AC-14  the deterministic role-split groups controls + the unassigned bucket
//	AC-15  a section citing a non-existent id is suppressed (no fabrication)
//	AC-16  a Tenant-B generation reads zero Tenant-A control rows (cross-tenant)
//	AC-17  approval requires human_approver (the shared DB guard)
package checklist_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/checklist"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
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

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM checklist_items WHERE tenant_id = $1`,
			`DELETE FROM checklist_sections WHERE tenant_id = $1`,
			`DELETE FROM ai_generations WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
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

// seedControl inserts one active control with the given owner_role + evidence
// flag, returning its id. owner_role drives the deterministic role-split.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant, title, ownerRole string, withEvidence bool) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			owner_role, bundle_id, evidence_queries, applicability_expr, freshness_class
		)
		VALUES ($1, $2, $3, 'AAA', 'automated', $4, $5, '[]'::jsonb, 'true', 'quarterly')
	`, ctrlID, tenant, title, ownerRole, "bundle-471-"+ctrlID.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	if withEvidence {
		evID := uuid.New()
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO evidence_records (
				id, tenant_id, control_id, control_ref, observed_at, ingested_at,
				provenance, result, payload, hash, evidence_kind
			)
			VALUES ($1, $2, $3, $4, now(), now(),
				$5, 'pass', '{}'::jsonb, $6, 'access_review.completion')
		`, evID, tenant, ctrlID, ctrlID.String(),
			`{"connector_id":"test-connector"}`, "hash-471-"+evID.String()[:8]); err != nil {
			t.Fatalf("seed evidence: %v", err)
		}
	}
	return ctrlID
}

// stubSvc builds a Service whose model returns the given crafted draft for every
// role section. The real Store backs the reader/resolver/persistence; the real
// AuditWriter records the generation.
func stubSvc(app *pgxpool.Pool, draft string) (*checklist.Service, *checklist.Store) {
	store := checklist.NewStore(app)
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}}
	svc := checklist.NewService(store, client, store, store, llm.NewAuditWriter(app))
	return svc, store
}

// ----- AC-13/AC-14: valid draft persists unapproved; role-split groups -----

func TestGenerate_ValidDraftPersistsUnapproved(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	infraID := seedControl(t, admin, tenant, "Cloud MFA control", "infra", true)

	// The stub draft cites the infra control id (resolves in-tenant).
	draft := "Enable MFA on all infra accounts (" + infraID.String() + ")."
	svc, store := stubSvc(app, draft)

	out, err := svc.Generate(tenantCtx(t, tenant))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Sections) != 1 || out.Sections[0].Role != checklist.RoleInfra {
		t.Fatalf("expected one infra section, got %+v", out.Sections)
	}
	sec := out.Sections[0]
	if sec.Suppressed || !sec.AIAssisted || sec.HumanApproved {
		t.Fatalf("section must be an unapproved AI draft: %+v", sec)
	}
	if sec.SectionID == "" || len(sec.Items) == 0 {
		t.Fatalf("section not persisted with items: %+v", sec)
	}

	// Reload proves the persisted state: ai_assisted, unapproved, provenance.
	sections, err := store.LoadGeneration(tenantCtx(t, tenant), uuid.MustParse(out.GenerationID))
	if err != nil {
		t.Fatalf("LoadGeneration: %v", err)
	}
	if len(sections) != 1 || !sections[0].AIAssisted || sections[0].HumanApproved {
		t.Fatalf("persisted section wrong: %+v", sections)
	}
	if sections[0].ModelProvider == "" {
		t.Error("model provenance must persist (R-mitigation)")
	}

	// An ai_generations audit row was written for the section (R-mitigation).
	aw := llm.NewAuditWriter(app)
	rows, err := aw.ListBySubject(tenantCtx(t, tenant), llm.SurfaceChecklist, sec.SectionID)
	if err != nil {
		t.Fatalf("ListBySubject: %v", err)
	}
	if len(rows) == 0 {
		t.Error("expected an ai_generations audit row for the section")
	}
}

func TestGenerate_RoleSplitAndUnassignedBucket(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	infraID := seedControl(t, admin, tenant, "Infra control", "infra", true)
	seedControl(t, admin, tenant, "Eng control", "engineering", true)
	seedControl(t, admin, tenant, "Sec control", "security", true)
	// A control whose owner_role matches no role -> unassigned bucket.
	seedControl(t, admin, tenant, "Orphan control", "team-zeta-nobody", true)

	// The stub cites the first control id it finds in the prompt; each section
	// has its own control, so each draft resolves. Use a per-prompt stub.
	store := checklist.NewStore(app)
	svc := checklist.NewService(store, perPromptStub{}, store, store, llm.NewAuditWriter(app))

	out, err := svc.Generate(tenantCtx(t, tenant))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	roles := map[checklist.Role]bool{}
	for _, s := range out.Sections {
		roles[s.Role] = true
	}
	for _, want := range []checklist.Role{checklist.RoleInfra, checklist.RoleEngineering, checklist.RoleSecurity, checklist.RoleUnassigned} {
		if !roles[want] {
			t.Errorf("missing section for role %q", want)
		}
	}
	// The unassigned bucket is non-AI + never approvable.
	for _, s := range out.Sections {
		if s.Role == checklist.RoleUnassigned {
			if s.AIAssisted {
				t.Error("unassigned bucket must be ai_assisted=false")
			}
			if _, err := svc.ApproveSection(tenantCtx(t, tenant), uuid.MustParse(s.SectionID), "key_grc"); err != checklist.ErrSectionNotFound {
				t.Errorf("unassigned bucket must not be approvable, got %v", err)
			}
		}
	}
	_ = infraID
}

// perPromptStub cites the first control id present in the system prompt.
type perPromptStub struct{}

func (perPromptStub) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	// Re-run the shared caps via a real stub.
	if _, err := (&llm.StubClient{Result: llm.GenerateResult{Text: "x", ModelName: "s", ModelVersion: "1", ModelProvider: "ollama-local"}}).Generate(context.Background(), req); err != nil {
		return llm.GenerateResult{}, err
	}
	id := firstUUID(req.SystemPrompt)
	return llm.GenerateResult{Text: "Implement this control (" + id + ").", ModelName: "stub-model", ModelVersion: "1", ModelProvider: "ollama-local"}, nil
}

func firstUUID(s string) string {
	// canonical 8-4-4-4-12; cheap scan.
	for i := 0; i+36 <= len(s); i++ {
		cand := s[i : i+36]
		if _, err := uuid.Parse(cand); err == nil {
			return cand
		}
	}
	return ""
}

// ----- AC-15: a fabricated citation suppresses the section -----

func TestGenerate_FabricatedCitationSuppresses(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	infraID := seedControl(t, admin, tenant, "Infra control", "infra", true)
	fab := uuid.New() // never exists

	// The draft cites the real control AND a fabricated id -> the whole section
	// is suppressed; nothing persists for it.
	draft := "Do the thing (" + infraID.String() + ") see (" + fab.String() + ")."
	svc, store := stubSvc(app, draft)

	out, err := svc.Generate(tenantCtx(t, tenant))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Sections) != 1 || !out.Sections[0].Suppressed {
		t.Fatalf("fabricated citation must suppress the section: %+v", out.Sections)
	}
	if out.Sections[0].Reason != checklist.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", out.Sections[0].Reason, checklist.ReasonUnresolvedCitation)
	}
	// Nothing persisted for the suppressed generation.
	n, err := store.CountSectionsForTenant(tenantCtx(t, tenant))
	if err != nil {
		t.Fatalf("CountSectionsForTenant: %v", err)
	}
	if n != 0 {
		t.Fatalf("a suppressed section must persist nothing, got %d sections", n)
	}
}

// ----- AC-16: cross-tenant isolation -----

func TestGenerate_CrossTenantIsolation(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant A has an infra control. Tenant B has its own infra control.
	ctrlA := seedControl(t, admin, tenantA, "Tenant A infra control", "infra", true)
	ctrlB := seedControl(t, admin, tenantB, "Tenant B infra control", "infra", true)

	store := checklist.NewStore(app)
	// The model, serving Tenant B, leaks Tenant A's control id.
	leak := "Do the thing (" + ctrlA.String() + ")."
	client := &llm.StubClient{Result: llm.GenerateResult{Text: leak, ModelName: "stub", ModelVersion: "1", ModelProvider: "ollama-local"}}
	svc := checklist.NewService(store, client, store, store, llm.NewAuditWriter(app))

	out, err := svc.Generate(tenantCtx(t, tenantB))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Tenant A's id is outside Tenant B's grounding set (B's control is ctrlB),
	// so the line is attributed to no in-section control -> suppressed.
	if len(out.Sections) != 1 || !out.Sections[0].Suppressed {
		t.Fatalf("CROSS-TENANT LEAK: a Tenant-B generation accepted a Tenant-A control id: %+v", out.Sections)
	}

	// Defense-in-depth: the leaked id is genuinely invisible to Tenant B at the
	// resolver level, and visible to Tenant A.
	if ok, rerr := store.ResolveControl(tenantCtx(t, tenantB), ctrlA); rerr != nil {
		t.Fatalf("ResolveControl(B, ctrlA): %v", rerr)
	} else if ok {
		t.Fatal("CROSS-TENANT LEAK: Tenant B resolved Tenant A's control id")
	}
	if ok, rerr := store.ResolveControl(tenantCtx(t, tenantA), ctrlA); rerr != nil {
		t.Fatalf("ResolveControl(A, ctrlA): %v", rerr)
	} else if !ok {
		t.Fatal("Tenant A could not resolve its own control id — resolver broken")
	}

	// And Tenant B's in-scope read never surfaces Tenant A's control.
	bControls, rerr := store.InScopeControls(tenantCtx(t, tenantB))
	if rerr != nil {
		t.Fatalf("InScopeControls(B): %v", rerr)
	}
	for _, c := range bControls {
		if c.ID == ctrlA.String() {
			t.Fatal("CROSS-TENANT LEAK: Tenant A's control surfaced in Tenant B's in-scope set")
		}
	}
	_ = ctrlB
}

// ----- AC-17: approval requires human_approver (the shared DB guard) -----

func TestApproveSection_RequiresApprover(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	infraID := seedControl(t, admin, tenant, "Infra control", "infra", true)
	draft := "Enable MFA (" + infraID.String() + ")."
	svc, store := stubSvc(app, draft)

	out, err := svc.Generate(tenantCtx(t, tenant))
	if err != nil || len(out.Sections) != 1 || out.Sections[0].SectionID == "" {
		t.Fatalf("Generate did not draft a section: %+v err=%v", out, err)
	}
	secID := uuid.MustParse(out.Sections[0].SectionID)

	// Blank approver rejected at the service (Go mirror of the DB guard).
	if _, err := svc.ApproveSection(tenantCtx(t, tenant), secID, ""); err != checklist.ErrApproverRequired {
		t.Fatalf("blank approver must be rejected (P0-471-6), got %v", err)
	}

	// Still unapproved after the rejected attempt.
	sections, _ := store.LoadGeneration(tenantCtx(t, tenant), uuid.MustParse(out.GenerationID))
	if sections[0].HumanApproved {
		t.Fatal("a rejected approval must leave the section unapproved")
	}

	// A real approver succeeds + records the approver.
	approved, err := svc.ApproveSection(tenantCtx(t, tenant), secID, "key_grc_engineer")
	if err != nil {
		t.Fatalf("ApproveSection: %v", err)
	}
	if !approved.HumanApproved || approved.HumanApprover != "key_grc_engineer" {
		t.Fatalf("approval did not record approver: %+v", approved)
	}

	// The persisted row reflects it.
	sections2, _ := store.LoadGeneration(tenantCtx(t, tenant), uuid.MustParse(out.GenerationID))
	if !sections2[0].HumanApproved || sections2[0].HumanApprover != "key_grc_engineer" {
		t.Fatalf("persisted approval state wrong: %+v", sections2[0])
	}
}

// ----- DB CHECK direct proof: ai_assisted+approved with NULL approver fails ---

func TestDBGuard_RejectsApprovedWithoutApprover(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	// The forbidden shape via the admin role (BYPASSRLS): ai_assisted +
	// human_approved with NULL human_approver. The shared guard CHECK must
	// reject it regardless of RLS (P0-471-6).
	_, err := admin.Exec(context.Background(), `
		INSERT INTO checklist_sections
			(tenant_id, generation_id, role, ai_assisted, human_approved, human_approver,
			 prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, 'infra', TRUE, TRUE, NULL,
			'checklist-v0', 'stub', '1', 'ollama-local')
	`, tenant, uuid.New())
	if err == nil {
		t.Fatal("DB CHECK FAILED TO FIRE: an ai_assisted+approved row with NULL approver was accepted (P0-471-6)")
	}

	// The same row WITH an approver is accepted.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO checklist_sections
			(tenant_id, generation_id, role, ai_assisted, human_approved, human_approver,
			 prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, 'infra', TRUE, TRUE, 'key_grc',
			'checklist-v0', 'stub', '1', 'ollama-local')
	`, tenant, uuid.New()); err != nil {
		t.Fatalf("an approved row WITH an approver must be accepted: %v", err)
	}
}

// ----- provenance-completeness CHECK: ai_assisted requires full provenance ----

func TestDBGuard_RejectsAIAssistedWithoutProvenance(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	// ai_assisted=TRUE with empty provenance must be rejected by the
	// provenance-nonempty CHECK.
	_, err := admin.Exec(context.Background(), `
		INSERT INTO checklist_sections
			(tenant_id, generation_id, role, ai_assisted, prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, 'security', TRUE, '', '', '', '')
	`, tenant, uuid.New())
	if err == nil {
		t.Fatal("provenance CHECK FAILED TO FIRE: an ai_assisted row with empty provenance was accepted")
	}

	// The unassigned bucket (ai_assisted=FALSE) with empty provenance is fine.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO checklist_sections
			(tenant_id, generation_id, role, ai_assisted, prompt_version, model_name, model_version, model_provider)
		VALUES ($1, $2, 'unassigned', FALSE, '', '', '', '')
	`, tenant, uuid.New()); err != nil {
		t.Fatalf("unassigned bucket with empty provenance must be accepted: %v", err)
	}
}
