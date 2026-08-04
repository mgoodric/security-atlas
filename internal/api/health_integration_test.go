//go:build integration

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestReadyFlipsWithPostgresUnavailableAndRecovers(t *testing.T) {
	badPool, err := pgxpool.New(context.Background(), "postgres://atlas:atlas@127.0.0.1:1/atlas")
	if err != nil {
		t.Fatalf("pgxpool.New bad pool: %v", err)
	}
	t.Cleanup(badPool.Close)

	srv := New(Config{})
	srv.AttachDB(badPool)
	ts := httptest.NewServer(srv.buildRouter())
	t.Cleanup(ts.Close)

	readyCode, readyBody := getProbe(t, ts.URL+"/ready")
	if readyCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /ready unavailable status = %d body = %q; want 503", readyCode, readyBody)
	}
	if !strings.Contains(readyBody, `"status":"not_ready"`) || !strings.Contains(readyBody, `"db":"degraded"`) {
		t.Fatalf("GET /ready unavailable body = %q; want not_ready degraded", readyBody)
	}

	healthCode, healthBody := getProbe(t, ts.URL+"/health")
	if healthCode != http.StatusOK {
		t.Fatalf("GET /health unavailable status = %d body = %q; want 200", healthCode, healthBody)
	}
	if !strings.Contains(healthBody, `"status":"ok"`) || !strings.Contains(healthBody, `"db":"degraded"`) {
		t.Fatalf("GET /health unavailable body = %q; want ok degraded", healthBody)
	}

	srv.AttachDB(dbtest.NewAppPool(t))
	readyCode, readyBody = getProbe(t, ts.URL+"/ready")
	if readyCode != http.StatusOK {
		t.Fatalf("GET /ready recovered status = %d body = %q; want 200", readyCode, readyBody)
	}
	if !strings.Contains(readyBody, `"status":"ready"`) || !strings.Contains(readyBody, `"db":"ok"`) {
		t.Fatalf("GET /ready recovered body = %q; want ready ok", readyBody)
	}
}

func getProbe(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s body: %v", url, err)
	}
	return resp.StatusCode, string(body)
}
