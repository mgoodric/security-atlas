// Pure-Go branch coverage for the slice 445 /v1/me/email-channel handler
// (internal/api/me). Per the slice-353 Q-2 fast-loop convention: no
// Postgres, no build tag. The deny branches return BEFORE touching the
// email.Channel, so a nil channel proves no store call is reachable on
// the guard path (P0-426-4 pattern).
package me

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestEmailChannelHandler_IdentityRejection(t *testing.T) {
	t.Parallel()
	h := NewEmailChannel(nil, false) // nil *email.Channel; deny branches return first

	t.Run("GET no auth context → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/me/email-channel", nil)
		h.Get(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET no auth = %d, want 401", rec.Code)
		}
	})

	t.Run("PUT no auth context → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/v1/me/email-channel",
			strings.NewReader(`{"enabled":true}`))
		h.Put(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("PUT no auth = %d, want 401", rec.Code)
		}
	})

	t.Run("GET auth but empty user id → 404", func(t *testing.T) {
		t.Parallel()
		tenant := uuid.NewString()
		base := httptest.NewRequest(http.MethodGet, "/v1/me/email-channel", nil)
		ctx := authctx.WithCredential(base.Context(),
			credstore.Credential{ID: "k", TenantID: tenant}) // UserID empty
		ctx = mustTenant(t, ctx, tenant)
		rec := httptest.NewRecorder()
		h.Get(rec, base.WithContext(ctx))
		// parseCredIDs rejects the empty user id (uuid.Parse fails) → 404.
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET empty user id = %d, want 404", rec.Code)
		}
	})

	t.Run("PUT invalid JSON after auth → 400", func(t *testing.T) {
		t.Parallel()
		tenant := uuid.NewString()
		user := uuid.NewString()
		base := httptest.NewRequest(http.MethodPut, "/v1/me/email-channel",
			strings.NewReader(`not json`))
		ctx := authctx.WithCredential(base.Context(),
			credstore.Credential{ID: "k", TenantID: tenant, UserID: user})
		ctx = mustTenant(t, ctx, tenant)
		rec := httptest.NewRecorder()
		h.Put(rec, base.WithContext(ctx))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT invalid JSON = %d, want 400", rec.Code)
		}
	})
}
