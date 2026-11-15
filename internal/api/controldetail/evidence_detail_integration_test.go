//go:build integration

// #2137 — integration tests for GET /v1/evidence/{id} over real Postgres
// with RLS: the payload comes back, a redaction rule is applied on read,
// another tenant's id is a 404, and a same-tenant caller without evidence
// read is a 404.

package controldetail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/controldetail"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/redact"
)

const detailKind = "test2137.attestation.v1"

func detailTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"evidence_records",
		"evidence_kind_schemas",
		"controls",
	)
}

// seedRedactingSchema registers a tenant-private schema for detailKind whose
// x-redaction-rules hide $.attestor.
func seedRedactingSchema(t *testing.T, admin *pgxpool.Pool, tenant string) {
	t.Helper()
	schema := `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"x-redaction-rules": ["$.attestor"],
		"additionalProperties": true
	}`
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_kind_schemas
			(id, tenant_id, kind, semver, major, minor, patch,
			 schema_json, owner, default_scf_anchors, created_by)
		VALUES (gen_random_uuid(), $1, $2, '1.0.0', 1, 0, 0,
		        $3::jsonb, 'test-2137', '{}', 'test-2137')
	`, tenant, detailKind, schema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
}

// seedAttestation stores a record whose payload still holds the attestor,
// as a record ingested before the redaction rule existed would.
func seedAttestation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, ingested_at,
			provenance, result, payload, hash, evidence_kind, schema_version
		)
		VALUES ($1, $2, $3, $4, $5, now(), '{"connector":"manual"}'::jsonb, 'pass',
		        '{"attestor":"alice@example.test","statement":"Quarterly access review done"}'::jsonb,
		        $6, $7, '1.0.0')
	`, id, tenant, ctrlID, ctrlID.String(), time.Now().UTC().Add(-time.Hour),
		"hash-2137-"+id.String()[:8], detailKind); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

func detailServer(t *testing.T, app, admin *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{SchemaRegistry: schemaregistry.NewService(admin)})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"}))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func TestEvidenceDetail_ReturnsRedactedPayload(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := detailTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	seedRedactingSchema(t, admin, tenant)
	id := seedAttestation(t, admin, tenant, ctrlID)

	resp, body := get(t, detailServer(t, app, admin, tenant), "/v1/evidence/"+id.String())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200; body %v", resp.StatusCode, body)
	}
	ev, _ := body["evidence"].(map[string]any)
	payload, _ := ev["payload"].(map[string]any)
	if payload["statement"] != "Quarterly access review done" {
		t.Fatalf("payload statement = %v", payload["statement"])
	}
	if payload["attestor"] != redact.Marker {
		t.Fatalf("attestor = %v, want %q", payload["attestor"], redact.Marker)
	}
	if ev["result"] != "pass" || ev["control_id"] != ctrlID.String() {
		t.Fatalf("record = %v", ev)
	}
}

func TestEvidenceDetail_CrossTenantIsNotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := detailTenant(t, admin)
	tenantB := detailTenant(t, admin)
	id := seedAttestation(t, admin, tenantA, seedControl(t, admin, tenantA))

	resp, body := get(t, detailServer(t, app, admin, tenantB), "/v1/evidence/"+id.String())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %v", resp.StatusCode, body)
	}
	if _, leaked := body["evidence"]; leaked {
		t.Fatalf("cross-tenant record leaked: %v", body)
	}
}

func TestEvidenceDetail_SameTenantWithoutAccessIsNotFound(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := detailTenant(t, admin)
	id := seedAttestation(t, admin, tenant, seedControl(t, admin, tenant))

	h := controldetail.New(controldetail.NewStore(app))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID: tenant,
				UserID:   "viewer-test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/evidence/{id}", h.EvidenceDetail)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/evidence/"+id.String(), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Quarterly") {
		t.Fatalf("payload leaked: %s", rec.Body.String())
	}
}
