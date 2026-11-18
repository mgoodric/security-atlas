//go:build integration

// #2414 — GET /v1/activity/actors lists the machine and system actors in
// the caller's own tenant's activity, under the same visibility rules as
// GET /v1/activity/unified.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

// seedActorRow inserts one decision_audit_log row (or, for kind
// "feature_flag", one feature_flag_audit_log row) by actor.
func seedActorRow(t *testing.T, tenantID uuid.UUID, kind, actor, action string) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed set_config: %v", err)
	}
	switch kind {
	case "decision":
		_, err = tx.Exec(ctx,
			`INSERT INTO decision_audit_log
			   (decision_id, tenant_id, user_id, action, resource_type, resource_id, result)
			 VALUES (gen_random_uuid(), $1, $2, $3, 'evidence', 'r-2414', 'allow')`,
			tenantID, actor, action)
	case "feature_flag":
		_, err = tx.Exec(ctx,
			`INSERT INTO feature_flag_audit_log
			   (id, tenant_id, flag_key, from_enabled, to_enabled, actor)
			 VALUES (gen_random_uuid(), $1, 'risk.enabled', true, false, $2)`,
			tenantID, actor)
	default:
		t.Fatalf("seedActorRow: unsupported kind %q", kind)
	}
	if err != nil {
		t.Fatalf("seed %s by %s: %v", kind, actor, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
}

func queryActors(t *testing.T, tenantID uuid.UUID, isAdmin, includeReads bool) []string {
	t.Helper()
	h := adminauditlog.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_actors",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   uuid.NewString(),
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/activity/actors", h.ActivityActors)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/actors?from=%s&to=%s", from, to)
	if includeReads {
		url += "&include_reads=true"
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminauditlog.ActivityActorsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Actors
}

func TestActivityActors_TenantScopedMachineActors(t *testing.T) {
	tenant, other := uuid.New(), uuid.New()
	cleanupUnifiedTables(t, tenant)
	cleanupUnifiedTables(t, other)

	seedActorRow(t, tenant, "decision", "system:scheduler", "write")
	seedActorRow(t, tenant, "decision", "oauth_client:ci", "write")
	seedActorRow(t, tenant, "decision", "system", "approve")
	seedActorRow(t, tenant, "decision", "system:reader", "read")      // read telemetry
	seedActorRow(t, tenant, "feature_flag", "system:flags", "")       // admin-only kind
	seedActorRow(t, tenant, "decision", uuid.NewString(), "write")    // a person: never listed
	seedActorRow(t, tenant, "decision", "key_2414", "write")          // an API key: never listed
	seedActorRow(t, other, "decision", "oauth_client:other", "write") // another tenant

	cases := []struct {
		name         string
		isAdmin      bool
		includeReads bool
		want         []string
	}{
		{"viewer", false, false, []string{"oauth_client:ci", "system", "system:scheduler"}},
		{"viewer with reads", false, true, []string{"oauth_client:ci", "system", "system:reader", "system:scheduler"}},
		{"admin", true, false, []string{"oauth_client:ci", "system", "system:flags", "system:scheduler"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := queryActors(t, tenant, tc.isAdmin, tc.includeReads)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("actors = %v; want %v", got, tc.want)
			}
		})
	}

	if got := queryActors(t, other, true, true); !slices.Equal(got, []string{"oauth_client:other"}) {
		t.Fatalf("other tenant actors = %v; want only its own", got)
	}
}
