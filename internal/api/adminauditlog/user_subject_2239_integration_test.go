//go:build integration

// #2239: callerAllowedUnified probed user_roles with the raw credential
// UserID, so a JWT user ("user:<uuid>") holding auditor or grc_engineer got
// 403 from the unified audit log. The probe now reads the subject as its
// bare uuid, the key role assignment stores.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

func unifiedStatus2239(t *testing.T, tenant uuid.UUID, credID, userID string) int {
	t.Helper()
	h := adminauditlog.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: credID, TenantID: tenant.String(), UserID: userID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/audit-log/unified", h.UnifiedList)

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to), nil))
	return rec.Code
}

func seedRole2239(t *testing.T, tenant uuid.UUID, userID, role string) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant.String()); err != nil {
		t.Fatalf("set_config: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test')`,
		tenant, userID, role); err != nil {
		t.Fatalf("seed user_roles: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestUnified2239_JWTSubjectResolvesAssignedRole(t *testing.T) {
	tenant := uuid.New()
	auditor := uuid.New()
	viewer := uuid.New()
	seedRole2239(t, tenant, auditor.String(), "auditor")
	seedRole2239(t, tenant, viewer.String(), "viewer")
	seedRole2239(t, tenant, "key_auditor2239", "auditor")

	for _, c := range []struct {
		name, credID, userID string
		want                 int
	}{
		{"JWT auditor", "jwt:a", "user:" + auditor.String(), http.StatusOK},
		{"bound key of the auditor", "key_bound2239", auditor.String(), http.StatusOK},
		{"JWT viewer", "jwt:b", "user:" + viewer.String(), http.StatusForbidden},
		{"JWT user with no row", "jwt:c", "user:" + uuid.NewString(), http.StatusForbidden},
		{"unbound key with an auditor row", "key_auditor2239", "key_auditor2239", http.StatusOK},
		{"OAuth client with no row", "jwt:d", "oauth_client:ci2239", http.StatusForbidden},
	} {
		if got := unifiedStatus2239(t, tenant, c.credID, c.userID); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
}

// TestActivity2239_JWTCallerSeesOwnMeRows: a non-privileged JWT caller
// ("user:<uuid>") sees their own me-rows on /v1/activity/unified, and the
// meta-audit row the query writes is recorded under their uuid, not the nil
// uuid. Before #2239 the predicate compared actor_id to the raw subject and
// matched none of them.
func TestActivity2239_JWTCallerSeesOwnMeRows(t *testing.T) {
	tenant := uuid.New()
	self := uuid.New()
	other := uuid.New()
	cleanupUnifiedTables(t, tenant)
	seedMeRowForActor(t, tenant, self, "profile.update")
	seedMeRowForActor(t, tenant, other, "profile.update")

	from := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to)
	r := newActivityRouter(t, tenant, "user:"+self.String(), false)

	query := func() (ownProfile, otherRows, ownMeta int) {
		t.Helper()
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
		}
		var resp adminauditlog.UnifiedListResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, e := range resp.Entries {
			if e.Kind != "me" {
				continue
			}
			switch {
			case e.ActorID == self.String() && e.Action == "profile.update":
				ownProfile++
			case e.ActorID == self.String():
				ownMeta++
			default:
				otherRows++
			}
		}
		return
	}

	query() // writes the first meta-audit row under the caller
	own, others, meta := query()
	if own != 1 {
		t.Errorf("own profile.update me-rows = %d; want 1", own)
	}
	if others != 0 {
		t.Errorf("me-rows of another actor (or the nil uuid) = %d; want 0", others)
	}
	if meta < 1 {
		t.Errorf("own meta-audit me-rows = %d; want at least 1, recorded under the caller's uuid", meta)
	}
}
