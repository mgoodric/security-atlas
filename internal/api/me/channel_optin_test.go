// Pure-Go branch coverage for the slice 543 /v1/me/{slack,webhook}-channel
// opt-in handler (internal/api/me). Per the slice-353 Q-2 fast-loop
// convention: no Postgres, no build tag. The deny branches return BEFORE
// invoking the get/set funcs, so funcs that t.Fatal-on-call prove no store
// call is reachable on the guard path (the slice-445 pattern).
package me

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

func TestChannelOptInHandler_IdentityRejection(t *testing.T) {
	t.Parallel()
	// get/set must NOT be reached on the deny paths.
	getNever := func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
		t.Error("get reached on a deny path")
		return false, nil
	}
	setNever := func(context.Context, uuid.UUID, uuid.UUID, bool) error {
		t.Error("set reached on a deny path")
		return nil
	}
	h := NewChannelOptIn("slack", getNever, setNever)

	t.Run("GET no auth → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		h.Get(rec, httptest.NewRequest(http.MethodGet, "/v1/me/slack-channel", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET no auth = %d, want 401", rec.Code)
		}
	})

	t.Run("PUT no auth → 401", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		h.Put(rec, httptest.NewRequest(http.MethodPut, "/v1/me/slack-channel",
			strings.NewReader(`{"enabled":true}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("PUT no auth = %d, want 401", rec.Code)
		}
	})

	t.Run("GET auth but empty user id → 404", func(t *testing.T) {
		t.Parallel()
		tenant := uuid.NewString()
		base := httptest.NewRequest(http.MethodGet, "/v1/me/slack-channel", nil)
		ctx := authctx.WithCredential(base.Context(),
			credstore.Credential{ID: "k", TenantID: tenant})
		ctx = mustTenant(t, ctx, tenant)
		rec := httptest.NewRecorder()
		h.Get(rec, base.WithContext(ctx))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET empty user id = %d, want 404", rec.Code)
		}
	})

	t.Run("PUT invalid JSON after auth → 400", func(t *testing.T) {
		t.Parallel()
		tenant := uuid.NewString()
		user := uuid.NewString()
		base := httptest.NewRequest(http.MethodPut, "/v1/me/webhook-channel",
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

// The happy path reaches get/set with the parsed (tenant, user).
func TestChannelOptInHandler_HappyPath(t *testing.T) {
	t.Parallel()
	tenant := uuid.NewString()
	user := uuid.NewString()

	var setTenant, setUser uuid.UUID
	var setEnabled bool
	get := func(_ context.Context, tn, u uuid.UUID) (bool, error) { return true, nil }
	set := func(_ context.Context, tn, u uuid.UUID, enabled bool) error {
		setTenant, setUser, setEnabled = tn, u, enabled
		return nil
	}
	h := NewChannelOptIn("webhook", get, set)

	// GET → 200 echoing get's value.
	base := httptest.NewRequest(http.MethodGet, "/v1/me/webhook-channel", nil)
	ctx := authctx.WithCredential(base.Context(),
		credstore.Credential{ID: "k", TenantID: tenant, UserID: user})
	ctx = mustTenant(t, ctx, tenant)
	rec := httptest.NewRecorder()
	h.Get(rec, base.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("GET body = %s", rec.Body.String())
	}

	// PUT → 200; set receives the parsed identity + flag.
	putBase := httptest.NewRequest(http.MethodPut, "/v1/me/webhook-channel",
		strings.NewReader(`{"enabled":true}`))
	pctx := authctx.WithCredential(putBase.Context(),
		credstore.Credential{ID: "k", TenantID: tenant, UserID: user})
	pctx = mustTenant(t, pctx, tenant)
	prec := httptest.NewRecorder()
	h.Put(prec, putBase.WithContext(pctx))
	if prec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, want 200", prec.Code)
	}
	if setTenant.String() != tenant || setUser.String() != user || !setEnabled {
		t.Fatalf("set got (%s,%s,%v); want (%s,%s,true)", setTenant, setUser, setEnabled, tenant, user)
	}
}
