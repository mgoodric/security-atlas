//go:build integration

// #2643: a mapping's idp_config_id must name one of the caller's own
// oidc_idp_configs. A foreign or nonexistent id gets the same 400 (no
// existence oracle), and deleting a config cascades to its mappings.
package admingroupmappings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/api/admingroupmappings"
)

func seedIDPConfig2643(t *testing.T, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO oidc_idp_configs (id, tenant_id, name, issuer_url, client_id, client_secret_enc, redirect_url)
		 VALUES ($1, $2, $3, 'https://idp.example', 'cid', '\x00', 'https://app/cb')`,
		id, tenantID, name); err != nil {
		t.Fatalf("seed idp config: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DELETE FROM oidc_idp_configs WHERE id = $1`, id)
	})
	return id
}

func postMapping2643(t *testing.T, router http.Handler, cfg string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"group_ref":"Engineering","role":"viewer","idp_config_id":"` + cfg + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings", bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func mappingCount2643(t *testing.T, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM oidc_idp_group_mappings WHERE tenant_id = $1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	return n
}

func TestCreate_OwnIDPConfigAccepted_2643(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "agm-2643-own")
	cfg := seedIDPConfig2643(t, tenant, "okta")
	rec := postMapping2643(t, newRouter(t, tenant, true), cfg.String())
	if rec.Code != http.StatusCreated {
		t.Fatalf("own config: status = %d; want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var created admingroupmappings.MappingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.IDPConfigID == nil || *created.IDPConfigID != cfg.String() {
		t.Fatalf("idp_config_id = %v; want %s", created.IDPConfigID, cfg)
	}
}

func TestCreate_ForeignAndNonexistentIDPConfigSame400_2643(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "agm-2643-a")
	other := seedTenant(t, "agm-2643-b")
	foreign := seedIDPConfig2643(t, other, "entra")
	router := newRouter(t, tenant, true)

	foreignRec := postMapping2643(t, router, foreign.String())
	missingRec := postMapping2643(t, router, uuid.New().String())

	for name, rec := range map[string]*httptest.ResponseRecorder{"foreign": foreignRec, "nonexistent": missingRec} {
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s config: status = %d; want 400 (body=%s)", name, rec.Code, rec.Body.String())
		}
	}
	if foreignRec.Body.String() != missingRec.Body.String() {
		t.Fatalf("foreign and nonexistent bodies differ (oracle):\n foreign=%s\n missing=%s",
			foreignRec.Body.String(), missingRec.Body.String())
	}
	if n := mappingCount2643(t, tenant); n != 0 {
		t.Fatalf("rejected creates stored %d mappings; want 0", n)
	}
}

// The FK is the backstop under the handler check: even a BYPASSRLS writer
// cannot store a mapping naming another tenant's config.
func TestFKRejectsCrossTenantIDPConfig_2643(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "agm-2643-fk-a")
	other := seedTenant(t, "agm-2643-fk-b")
	foreign := seedIDPConfig2643(t, other, "entra")
	_, err := adminPool.Exec(context.Background(),
		`INSERT INTO oidc_idp_group_mappings (tenant_id, idp_config_id, group_ref, role)
		 VALUES ($1, $2, 'Engineering', 'viewer')`, tenant, foreign)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("cross-tenant insert err = %v; want SQLSTATE 23503", err)
	}
}

func TestDeleteIDPConfigCascadesToMappings_2643(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "agm-2643-cascade")
	cfg := seedIDPConfig2643(t, tenant, "okta")
	router := newRouter(t, tenant, true)
	if rec := postMapping2643(t, router, cfg.String()); rec.Code != http.StatusCreated {
		t.Fatalf("seed mapping: status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	// A SCIM-source (NULL config) mapping must survive the cascade.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings",
		bytes.NewReader([]byte(`{"group_ref":"Scim","role":"viewer"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed scim mapping: status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	if n := mappingCount2643(t, tenant); n != 2 {
		t.Fatalf("before delete: %d mappings; want 2", n)
	}

	if _, err := adminPool.Exec(context.Background(), `DELETE FROM oidc_idp_configs WHERE id = $1`, cfg); err != nil {
		t.Fatalf("delete config: %v", err)
	}

	var remaining int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM oidc_idp_group_mappings WHERE tenant_id = $1 AND idp_config_id = $2`,
		tenant, cfg).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("after delete: %d mappings still reference the config; want 0", remaining)
	}
	if n := mappingCount2643(t, tenant); n != 1 {
		t.Fatalf("after delete: %d mappings; want 1 (the SCIM one)", n)
	}
}
