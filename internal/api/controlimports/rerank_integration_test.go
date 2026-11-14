//go:build integration

// #1781 — integration tests for the optional LLM rerank of control import
// candidates. Real Postgres with RLS and the real controlimport.Store and
// ai_generations writer; the LLM is a stub client, and the opt-in gate is the
// production cloud-LLM routing store (tenant_llm_routing).
package controlimports_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/controlimports"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// reverseStub is a stub LLM that ranks a row's candidates in reverse of the
// order it was given, with a rationale naming each anchor. It counts calls.
type reverseStub struct {
	calls atomic.Int32
	err   error
}

func (s *reverseStub) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	s.calls.Add(1)
	if err := req.Validate(); err != nil {
		return llm.GenerateResult{}, err
	}
	if s.err != nil {
		return llm.GenerateResult{}, s.err
	}
	ids, _ := req.Context["candidate_scf_ids"].([]string)
	text := `{"ranking":[`
	for i := len(ids) - 1; i >= 0; i-- {
		if i != len(ids)-1 {
			text += ","
		}
		text += fmt.Sprintf(`{"scf_id":%q,"rationale":"stub rationale for %s"}`, ids[i], ids[i])
	}
	// An anchor that is not a candidate: the importer must ignore it.
	text += `,{"scf_id":"ZZZ-99","rationale":"invented"}]}`
	return llm.GenerateResult{Text: text, ModelName: "stub-model", ModelVersion: "1", ModelProvider: "anthropic"}, nil
}

// optIn sets the tenant's cloud-LLM routing to a cloud provider.
func optIn(t *testing.T, app *pgxpool.Pool, tenant string) *cloud.Store {
	t.Helper()
	crypter, err := cloud.NewCrypter([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewCrypter: %v", err)
	}
	store := cloud.NewStore(app, crypter)
	if _, err := store.Set(dbtest.WithTenantCtx(t, tenant), cloud.ProviderAnthropic, cloud.Secret("control-import-fake-key-DO-NOT-USE")); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	return store
}

// rerankEnv serves the control import routes with a stager wired to client,
// gated on store's routing config for the tenant. Requests carry the tenant
// the way the platform's tenancy middleware sets it.
func rerankEnv(t *testing.T, app *pgxpool.Pool, tenant string, client llm.Client, store *cloud.Store) testEnv {
	t.Helper()
	gate := func(ctx context.Context) (bool, error) {
		cfg, err := store.Get(ctx)
		return cfg.IsCloud, err
	}
	stager := controlimport.NewStager(controlimport.NewStore(app)).WithReranker(
		controlimport.NewReranker(client, controlimport.AuditRecorder(llm.NewAuditWriter(app)), gate))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx, err := tenancy.WithTenant(req.Context(), tenant)
			if err != nil {
				t.Errorf("WithTenant: %v", err)
				return
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	controlimports.New(stager).RegisterRoutes(r)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return testEnv{server: ts}
}

func rerankTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"control_import_candidates",
		"control_import_rejected_rows",
		"control_import_rows",
		"control_import_batches",
		"ai_generations",
		"tenant_llm_routing",
	)
}

// deterministicRows uploads the fixture with no LLM at all and returns its
// rows by external ID: the baseline the rerank tests compare against.
func deterministicRows(t *testing.T, admin, app *pgxpool.Pool, framework string) map[string]controlimport.BatchControl {
	t.Helper()
	tenant := rerankTenant(t, admin)
	env := rerankEnv(t, app, tenant, nil, cloud.NewStore(app, nil))
	list := listRows(t, env, uploadFixture(t, env, framework, "csv"), "")
	out := map[string]controlimport.BatchControl{}
	for _, r := range list.Rows {
		if r.LLMRerank != controlimport.RerankOff {
			t.Fatalf("baseline row %s llm_rerank = %q, want off", r.ExternalID, r.LLMRerank)
		}
		out[r.ExternalID] = r
	}
	return out
}

func scfIDs(cs []controlimport.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.SCFID
	}
	return out
}

func TestStubLLMReordersCandidatesWithRationales(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	baseline := deterministicRows(t, admin, app, cat.framework)

	tenant := rerankTenant(t, admin)
	stub := &reverseStub{}
	env := rerankEnv(t, app, tenant, stub, optIn(t, app, tenant))
	list := listRows(t, env, uploadFixture(t, env, cat.framework, "csv"), "")

	if stub.calls.Load() == 0 {
		t.Fatal("stub LLM was not called with the cloud-LLM opt-in on")
	}
	for _, row := range list.Rows {
		base := baseline[row.ExternalID]
		if len(base.Candidates) == 0 {
			continue
		}
		if row.LLMRerank != controlimport.RerankApplied {
			t.Errorf("%s llm_rerank = %q, want applied", row.ExternalID, row.LLMRerank)
		}
		want := scfIDs(base.Candidates)
		slices.Reverse(want)
		if got := scfIDs(row.Candidates); !slices.Equal(got, want) {
			t.Errorf("%s order = %v, want the stub's %v", row.ExternalID, got, want)
		}
		gotSet, baseSet := scfIDs(row.Candidates), scfIDs(base.Candidates)
		slices.Sort(gotSet)
		slices.Sort(baseSet)
		if !slices.Equal(gotSet, baseSet) {
			t.Errorf("%s anchors = %v, want the deterministic set %v", row.ExternalID, gotSet, baseSet)
		}
		for i, c := range row.Candidates {
			if c.Rank != i+1 || !c.AIAssisted || c.Rationale != "stub rationale for "+c.SCFID {
				t.Errorf("%s candidate %d = %+v, want rank %d with the stub's rationale", row.ExternalID, i, c, i+1)
			}
		}
	}
}

func TestFailingLLMLeavesDeterministicCandidates(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	baseline := deterministicRows(t, admin, app, cat.framework)

	tenant := rerankTenant(t, admin)
	stub := &reverseStub{err: errors.New("stub outage")}
	env := rerankEnv(t, app, tenant, stub, optIn(t, app, tenant))
	list := listRows(t, env, uploadFixture(t, env, cat.framework, "csv"), "")

	if stub.calls.Load() == 0 {
		t.Fatal("stub LLM was not called with the cloud-LLM opt-in on")
	}
	for _, row := range list.Rows {
		base := baseline[row.ExternalID]
		if len(base.Candidates) == 0 {
			continue
		}
		if row.LLMRerank != controlimport.RerankUnavailable {
			t.Errorf("%s llm_rerank = %q, want unavailable", row.ExternalID, row.LLMRerank)
		}
		if got, want := scfIDs(row.Candidates), scfIDs(base.Candidates); !slices.Equal(got, want) {
			t.Errorf("%s candidates = %v, want the deterministic %v", row.ExternalID, got, want)
		}
		for _, c := range row.Candidates {
			if c.AIAssisted || c.Rationale != "" {
				t.Errorf("%s candidate %s carries LLM output after a failure: %+v", row.ExternalID, c.SCFID, c)
			}
		}
	}
	if n := countGenerations(t, admin, tenant, ""); n != 0 {
		t.Errorf("ai_generations rows after a failed generation = %d, want 0", n)
	}
}

func TestOptInOffDoesNotCallLLM(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)

	tenant := rerankTenant(t, admin)
	stub := &reverseStub{}
	// No routing row: the tenant is on the local default, cloud opt-in off.
	env := rerankEnv(t, app, tenant, stub, cloud.NewStore(app, nil))
	list := listRows(t, env, uploadFixture(t, env, cat.framework, "csv"), "")

	if n := stub.calls.Load(); n != 0 {
		t.Fatalf("LLM called %d times with the cloud-LLM opt-in off, want 0", n)
	}
	for _, row := range list.Rows {
		if row.LLMRerank != controlimport.RerankOff {
			t.Errorf("%s llm_rerank = %q, want off", row.ExternalID, row.LLMRerank)
		}
		for _, c := range row.Candidates {
			if c.AIAssisted || c.Rationale != "" {
				t.Errorf("%s candidate %s carries LLM output: %+v", row.ExternalID, c.SCFID, c)
			}
		}
	}
}

func TestLLMUsageRecordedUnderImportSurface(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)

	tenant := rerankTenant(t, admin)
	stub := &reverseStub{}
	env := rerankEnv(t, app, tenant, stub, optIn(t, app, tenant))
	uploadFixture(t, env, cat.framework, "csv")

	calls := int(stub.calls.Load())
	if calls == 0 {
		t.Fatal("stub LLM was not called")
	}
	if n := countGenerations(t, admin, tenant, string(llm.SurfaceControlImport)); n != calls {
		t.Errorf("control_import ai_generations rows = %d, want one per call (%d)", n, calls)
	}
	if n := countGenerations(t, admin, tenant, string(llm.SurfaceQuestionnaire)); n != 0 {
		t.Errorf("questionnaire ai_generations rows = %d, want 0", n)
	}
}

// countGenerations counts the tenant's ai_generations rows, for one surface
// or (surface "") all of them.
func countGenerations(t *testing.T, admin *pgxpool.Pool, tenant, surface string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM ai_generations
		WHERE tenant_id = $1::uuid AND ($2 = '' OR surface = $2)
	`, tenant, surface).Scan(&n); err != nil {
		t.Fatalf("count ai_generations: %v", err)
	}
	return n
}
