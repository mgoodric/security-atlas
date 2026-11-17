//go:build integration

// #2110 — GET /v1/controls/{id}/bundle against real Postgres (RLS on the
// app role): a tenant downloads its own control's stored control.yaml, and
// another tenant's control id is 404.

package controls_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const bundleDownload2110Manifest = `bundle_schema_version: "1"
bundle_id: download_2110_ctrl
title: "Download 2110"
anchor_fit: none
anchor_fit_note: "integration fixture"
implementation_type: automated
`

func bundleDownloadServer(t *testing.T, app *pgxpool.Pool, tenant string) *httptest.Server {
	t.Helper()
	h := controlsapi.NewBundleDownloadHandler(control.NewStore(app))
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "jwt:download-2110", UserID: "user:" + uuid.NewString(), TenantID: tenant,
			})))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/controls/{id}/bundle", h.Download)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func getBundle2110(t *testing.T, ts *httptest.Server, id uuid.UUID) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/v1/controls/"+id.String()+"/bundle", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET bundle: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func TestBundleDownload2110_TenantScoped(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	owner := dbtest.SeedTenant(t, migrate, "controls")
	other := dbtest.SeedTenant(t, migrate, "controls")

	id := uuid.New()
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO controls (id, tenant_id, bundle_id, title, control_family, implementation_type, bundle_manifest_yaml)
		 VALUES ($1, $2, 'download_2110_ctrl', 'Download 2110', 'AAA', 'automated', $3)`,
		id, owner, bundleDownload2110Manifest); err != nil {
		t.Fatalf("seed control: %v", err)
	}

	code, body := getBundle2110(t, bundleDownloadServer(t, app, owner), id)
	if code != http.StatusOK {
		t.Fatalf("own tenant: status = %d; body %s", code, body)
	}
	b, err := control.ParseTarball(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("ParseTarball: %v", err)
	}
	if string(b.ManifestYAMLRaw) != bundleDownload2110Manifest {
		t.Fatalf("manifest = %q", b.ManifestYAMLRaw)
	}

	if code, body := getBundle2110(t, bundleDownloadServer(t, app, other), id); code != http.StatusNotFound {
		t.Fatalf("other tenant: status = %d; want 404; body %s", code, body)
	}
}
