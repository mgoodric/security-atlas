//go:build integration

// #2783 — the needs-attention large-read warning goes to the logger the
// platform attaches (Server.AttachLogger, as cmd/atlas does), not to
// slog.Default(), and repeated polls inside needsattention.WarnInterval do
// not repeat it. Through the production router on real Postgres under RLS,
// over the attention fixture with a threshold of one row.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
)

// syncBuffer is a bytes.Buffer safe for the server's goroutines.
type syncBuffer2783 struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer2783) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer2783) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// warnings returns the large-read warnings logged for tenant, by source.
func (b *syncBuffer2783) warnings(t *testing.T, tenant string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if rec["msg"] == "needs attention: band read passed the row threshold" && rec["tenant_id"] == tenant {
			src, _ := rec["source"].(string)
			out[src]++
		}
	}
	return out
}

func TestNeedsAttention_2783_WarnGoesToAttachedLoggerOncePerInterval(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, migrate, time.Now().UTC())
	ctx := context.Background()
	t.Cleanup(func() {
		for _, table := range []string{"decision_audit_log", "user_roles"} {
			_, _ = migrate.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = $1", fx.A.ID)
		}
	})

	engine, err := authz.NewEngine(ctx, authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	logs := &syncBuffer2783{}
	srv := api.New(api.Config{NeedsAttentionWarnRows: 1})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	srv.AttachLogger(slog.New(slog.NewJSONHandler(logs, nil)))

	if _, err := migrate.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, 'grc_engineer', 'test-2783')`,
		fx.A.ID, fx.A.Owner.String()); err != nil {
		t.Fatalf("grant grc_engineer: %v", err)
	}
	c := testjwt.OwnerFor(uuid.MustParse(fx.A.ID), []string{"grc_engineer"})
	c.Subject = "user:" + fx.A.Owner.String()
	// Minting first attaches the JWT validator the router is built with.
	bearer := srv.IssueTestJWT(t, c)
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	poll := func() {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/needs-attention?for=all", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /v1/needs-attention: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET /v1/needs-attention: status %d body=%s", resp.StatusCode, b)
		}
	}

	poll()
	first := logs.warnings(t, fx.A.ID)
	// The fixture has more than one calendar event, control and risk.
	want := map[string]int{"calendar": 1, "controls": 1, "risks": 1}
	for src, n := range want {
		if first[src] != n {
			t.Errorf("after one poll, %s warnings = %d; want %d (all: %v)", src, first[src], n, first)
		}
	}

	poll()
	poll()
	again := logs.warnings(t, fx.A.ID)
	for src, n := range want {
		if again[src] != n {
			t.Errorf("after three polls, %s warnings = %d; want %d (all: %v)", src, again[src], n, again)
		}
	}
}
