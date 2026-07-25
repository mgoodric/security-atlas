//go:build integration

// Integration tests for slice 499: the per-tenant cloud-LLM routing layer
// against real Postgres. These are the constitutional proofs:
//
//   - AC-9  : a local-ollama tenant routes to the local client (no cloud
//             provider, no banner); a cloud tenant routes to the cloud adapter
//             and records model_provider = the cloud provider.
//   - AC-10 : cross-tenant isolation — tenant A's generation uses A's config/
//             key and CANNOT use B's (RLS-scoped Resolve under app.current_tenant).
//   - AC-11 : the provider key is never returned by the Store's masked read,
//             and the stored column is ciphertext (never plaintext).
//   - AC-13 : a cloud-generated draft still requires human_approver — proven via
//             the slice-498 reusable ai_assist_human_approver_guard (the
//             approval gate is backend-agnostic).
//
// Memory rule: "Never mock the DB" — every test runs the real migration, the
// real four-policy RLS, and the real sqlc CRUD through the NOBYPASSRLS
// atlas_app role.
//
// Run with:  go test -tags=integration -p 1 ./internal/llm/cloud/...

package cloud_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeProviderKeyA / B are obviously-fake provider keys (no real sk-ant-/sk-/
// AKIA prefix) so GitGuardian never flags them.
const (
	fakeProviderKeyA = "tenant-A-fake-key-DO-NOT-USE-001"
	fakeProviderKeyB = "tenant-B-fake-key-DO-NOT-USE-002"
)

func testCrypter(t *testing.T) *cloud.Crypter {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	c, err := cloud.NewCrypter(key)
	if err != nil {
		t.Fatalf("NewCrypter: %v", err)
	}
	return c
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, "tenant_llm_routing")
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// recordingClient is a fake llm.Client that records the provider it was asked to
// serve as, standing in for a real cloud adapter so the Router's dispatch is
// proven without a live cloud call.
type recordingClient struct {
	provider string
	calls    int
}

func (c *recordingClient) Generate(_ context.Context, _ llm.GenerateRequest) (llm.GenerateResult, error) {
	c.calls++
	return llm.GenerateResult{
		Text:          "cloud draft",
		ModelName:     "fake-cloud-model",
		ModelVersion:  "1",
		ModelProvider: c.provider,
	}, nil
}

func validReq() llm.GenerateRequest {
	return llm.GenerateRequest{
		Surface:       llm.SurfaceQuestionnaire,
		PromptVersion: "v1",
		SystemPrompt:  "sys",
		MaxTokens:     128,
		Timeout:       5 * time.Second,
	}
}

// ----- Store CRUD: default, set cloud, masked read, clear -----

func TestStore_DefaultIsLocalNoRow(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	store := cloud.NewStore(app, testCrypter(t))
	mc, err := store.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if mc.Provider != cloud.ProviderLocalOllama || mc.IsCloud || mc.HasAPIKey {
		t.Fatalf("default config = %+v, want local-ollama/no-key (AC-2 off-by-default)", mc)
	}
}

func TestStore_SetCloud_MaskedRead_KeyNeverReturned(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	crypter := testCrypter(t)
	store := cloud.NewStore(app, crypter)

	mc, err := store.Set(ctx, cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	// AC-11: masked read carries no plaintext and no ciphertext.
	if mc.Provider != cloud.ProviderAnthropic || !mc.IsCloud || !mc.HasAPIKey {
		t.Fatalf("masked config = %+v", mc)
	}
	if strings.Contains(mc.APIKeyMasked, fakeProviderKeyA) {
		t.Fatal("masked config leaked the plaintext key")
	}

	// Re-read via Get: same masking.
	got, err := store.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if strings.Contains(got.APIKeyMasked, fakeProviderKeyA) {
		t.Fatal("Get leaked the plaintext key")
	}

	// AC-11: the stored DB column is CIPHERTEXT (never plaintext). Read it raw
	// via the admin pool.
	var stored *string
	if err := admin.QueryRow(context.Background(),
		`SELECT api_key_ciphertext FROM tenant_llm_routing WHERE tenant_id = $1`, tenant).Scan(&stored); err != nil {
		t.Fatalf("raw read: %v", err)
	}
	if stored == nil || *stored == "" {
		t.Fatal("ciphertext column empty for a cloud row")
	}
	if strings.Contains(*stored, fakeProviderKeyA) {
		t.Fatal("DB column contains the plaintext key (not encrypted)")
	}

	// Resolve (router path) decrypts to the original.
	provider, key, err := store.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if provider != cloud.ProviderAnthropic || key.Reveal() != fakeProviderKeyA {
		t.Fatalf("Resolve = (%q, masked), key mismatch", provider)
	}
}

func TestStore_ClearRevertsToLocal(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, testCrypter(t))

	if _, err := store.Set(ctx, cloud.ProviderOpenAI, cloud.Secret(fakeProviderKeyA)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	removed, err := store.Clear(ctx)
	if err != nil || !removed {
		t.Fatalf("Clear = (%v,%v)", removed, err)
	}
	provider, key, err := store.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve after clear: %v", err)
	}
	if provider != cloud.ProviderLocalOllama || !key.IsZero() {
		t.Fatalf("after clear = (%q, key?), want local-ollama/no-key", provider)
	}
}

func TestStore_LocalProviderRejectsKey_CloudRequiresKey(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, testCrypter(t))

	if _, err := store.Set(ctx, cloud.ProviderLocalOllama, cloud.Secret("unexpected")); !errors.Is(err, cloud.ErrLocalProviderNoKey) {
		t.Fatalf("local+key = %v, want ErrLocalProviderNoKey", err)
	}
	if _, err := store.Set(ctx, cloud.ProviderBedrock, cloud.Secret("")); !errors.Is(err, cloud.ErrCloudKeyRequired) {
		t.Fatalf("cloud+nokey = %v, want ErrCloudKeyRequired", err)
	}
}

func TestStore_NoCrypter_CloudOptInRejected(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, nil) // deployment with no cloud key

	if _, err := store.Set(ctx, cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA)); !errors.Is(err, cloud.ErrCrypterUnconfigured) {
		t.Fatalf("cloud opt-in with no crypter = %v, want ErrCrypterUnconfigured", err)
	}
}

// ----- AC-9: router dispatch (local -> local, cloud -> cloud + provenance) -----

func TestRouter_LocalTenantUsesLocalClient(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	local := &recordingClient{provider: "ollama-local"}
	cloudClient := &recordingClient{provider: "anthropic"}
	router := cloud.NewRouter(local, cloud.NewStore(app, testCrypter(t)),
		func(_ cloud.Provider, _ cloud.Secret) (llm.Client, error) { return cloudClient, nil })

	res, err := router.Generate(ctx, validReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if local.calls != 1 || cloudClient.calls != 0 {
		t.Fatalf("dispatch: local=%d cloud=%d, want local-only", local.calls, cloudClient.calls)
	}
	// AC-9: local provider does NOT trip the banner predicate.
	if cloud.IsCloudProvider(res.ModelProvider) {
		t.Fatalf("local result flagged as cloud: %q", res.ModelProvider)
	}
}

func TestRouter_CloudTenantUsesAdapterAndRecordsProvider(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := cloud.NewStore(app, testCrypter(t))

	if _, err := store.Set(ctx, cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA)); err != nil {
		t.Fatalf("Set: %v", err)
	}

	local := &recordingClient{provider: "ollama-local"}
	cloudClient := &recordingClient{provider: "anthropic"}
	var gotKey string
	router := cloud.NewRouter(local, store,
		func(p cloud.Provider, k cloud.Secret) (llm.Client, error) {
			gotKey = k.Reveal()
			return cloudClient, nil
		})

	res, err := router.Generate(ctx, validReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if cloudClient.calls != 1 || local.calls != 0 {
		t.Fatalf("dispatch: local=%d cloud=%d, want cloud-only", local.calls, cloudClient.calls)
	}
	// AC-5: provenance is the cloud provider; AC-9 banner predicate trips.
	if res.ModelProvider != "anthropic" || !cloud.IsCloudProvider(res.ModelProvider) {
		t.Fatalf("ModelProvider = %q, want cloud anthropic", res.ModelProvider)
	}
	// The router resolved the tenant's own decrypted key into the adapter.
	if gotKey != fakeProviderKeyA {
		t.Fatal("router did not pass the tenant's decrypted key to the adapter")
	}
}

// ----- AC-10: cross-tenant isolation (LOAD-BEARING) -----

func TestCrossTenantIsolation_ConfigAndKey(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	crypter := testCrypter(t)
	store := cloud.NewStore(app, crypter)

	// A opts into anthropic with A's key; B opts into openai with B's key.
	if _, err := store.Set(tenantCtx(t, tenantA), cloud.ProviderAnthropic, cloud.Secret(fakeProviderKeyA)); err != nil {
		t.Fatalf("A set: %v", err)
	}
	if _, err := store.Set(tenantCtx(t, tenantB), cloud.ProviderOpenAI, cloud.Secret(fakeProviderKeyB)); err != nil {
		t.Fatalf("B set: %v", err)
	}

	// A's Resolve sees A's provider + A's key, NEVER B's.
	pA, kA, err := store.Resolve(tenantCtx(t, tenantA))
	if err != nil {
		t.Fatalf("A resolve: %v", err)
	}
	if pA != cloud.ProviderAnthropic || kA.Reveal() != fakeProviderKeyA {
		t.Fatalf("A resolved %q (key match=%v); cross-tenant bleed", pA, kA.Reveal() == fakeProviderKeyA)
	}
	if kA.Reveal() == fakeProviderKeyB {
		t.Fatal("tenant A resolved tenant B's key (RLS leak)")
	}

	// B's Resolve sees B's provider + B's key.
	pB, kB, err := store.Resolve(tenantCtx(t, tenantB))
	if err != nil {
		t.Fatalf("B resolve: %v", err)
	}
	if pB != cloud.ProviderOpenAI || kB.Reveal() != fakeProviderKeyB {
		t.Fatalf("B resolved %q; cross-tenant bleed", pB)
	}

	// A third tenant with no row resolves to the local default (sees neither).
	tenantC := freshTenant(t, admin)
	pC, kC, err := store.Resolve(tenantCtx(t, tenantC))
	if err != nil {
		t.Fatalf("C resolve: %v", err)
	}
	if pC != cloud.ProviderLocalOllama || !kC.IsZero() {
		t.Fatalf("uninitialized tenant resolved %q with a key; isolation broken", pC)
	}
}

// ----- AC-13: cloud-generated draft still requires human_approver -----
//
// The approval gate is backend-agnostic: it lives on the surface consumer
// record via the slice-498 ai_assist_human_approver_guard CHECK, NOT in the
// routing layer. We prove that a row tagged ai_assisted (regardless of whether
// the provider was cloud) cannot be human_approved without an approver — exactly
// the slice-498 invariant, which the cloud routing does not and cannot bypass.

func TestApprovalGate_BackendAgnostic(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()

	// An adopter table shaped like a cloud-generated approvable consumer record.
	_, err := admin.Exec(ctx, `
		CREATE TEMP TABLE cloud_draft_adopter (
			id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			model_provider  TEXT NOT NULL,
			ai_assisted     BOOLEAN NOT NULL DEFAULT TRUE,
			human_approved  BOOLEAN NOT NULL DEFAULT FALSE,
			human_approver  TEXT NULL,
			CONSTRAINT cloud_draft_ai_assist_invariant
				CHECK (ai_assist_human_approver_guard(
					ai_assisted, human_approved, human_approver))
		)`)
	if err != nil {
		t.Fatalf("create adopter: %v", err)
	}

	// A CLOUD-generated draft, approved with NO approver, is REJECTED at the DB.
	_, err = admin.Exec(ctx, `
		INSERT INTO cloud_draft_adopter (model_provider, ai_assisted, human_approved, human_approver)
		VALUES ('anthropic', TRUE, TRUE, NULL)`)
	if err == nil {
		t.Fatal("DB accepted a cloud draft approved without an approver; gate bypassed")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want 23514 check_violation, got %v", err)
	}

	// The same cloud draft, approved WITH an approver, is accepted.
	if _, err := admin.Exec(ctx, `
		INSERT INTO cloud_draft_adopter (model_provider, ai_assisted, human_approved, human_approver)
		VALUES ('anthropic', TRUE, TRUE, 'user-7')`); err != nil {
		t.Fatalf("DB rejected a properly-approved cloud draft: %v", err)
	}

	// And a cloud draft left unapproved (the common state) is accepted.
	if _, err := admin.Exec(ctx, `
		INSERT INTO cloud_draft_adopter (model_provider, ai_assisted, human_approved, human_approver)
		VALUES ('openai', TRUE, FALSE, NULL)`); err != nil {
		t.Fatalf("DB rejected an unapproved cloud draft: %v", err)
	}
}
