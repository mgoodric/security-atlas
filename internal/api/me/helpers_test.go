// helpers_test.go — slice 426 pure-Go branch coverage for internal/api/me.
//
// Per the slice-353 Q-2 fast-loop convention: no Postgres, no
// `//go:build integration` tag, fast `t.Parallel()` table tests. The `me`
// package resolves the caller's own identity, so AC-4 asks specifically that
// the identity-resolution branches reject a missing/invalid auth context.
// These tests assert exactly that, plus the pure-Go wire converters:
//
//   - authnContext: deny on missing credential, deny on empty TenantID, deny
//     on a credential whose tenancy context was never applied
//   - the per-handler missing-context (401) and missing-user-id (401) deny
//     branches, driven with nil stores so NO DB call is reachable (the
//     handler returns before the store — P0-426-4) — this is the
//     identity-resolution rejection AC-4 names
//   - profileWireFrom (admin vs user role; nil vs set TimeZone)
//   - sessionWireFrom + last4OfSessionID + currentSessionIDFromRequest
//   - notificationWireFrom (nil vs set ReadAt)
//   - assignmentWireFrom (nil vs set FrozenAt)
//   - mapsEqual
//
// The happy-path store reads stay in the *_integration_test.go suites.
package me

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// mustTenant applies a tenancy context for tenantID, failing the test on a
// malformed id. Used by the user-id-guard test where authnContext must pass
// so the user-id rejection is the branch under test.
func mustTenant(t *testing.T, ctx context.Context, tenantID string) context.Context {
	t.Helper()
	out, err := tenancy.WithTenant(ctx, tenantID)
	if err != nil {
		t.Fatalf("tenancy.WithTenant(%q): %v", tenantID, err)
	}
	return out
}

func TestAuthnContext_DenyBranches(t *testing.T) {
	t.Parallel()
	t.Run("no credential", func(t *testing.T) {
		t.Parallel()
		r := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		if _, _, ok := authnContext(r); ok {
			t.Fatal("authnContext should deny a request with no credential")
		}
	})
	t.Run("empty tenant id", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCredential(
			httptest.NewRequest(http.MethodGet, "/v1/me", nil).Context(),
			credstore.Credential{ID: "k"}, // TenantID empty
		)
		r := httptest.NewRequest(http.MethodGet, "/v1/me", nil).WithContext(ctx)
		if _, _, ok := authnContext(r); ok {
			t.Fatal("authnContext should deny a credential with empty TenantID")
		}
	})
	t.Run("tenant id set but no tenancy context", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCredential(
			httptest.NewRequest(http.MethodGet, "/v1/me", nil).Context(),
			credstore.Credential{ID: "k", TenantID: uuid.NewString()},
		)
		r := httptest.NewRequest(http.MethodGet, "/v1/me", nil).WithContext(ctx)
		if _, _, ok := authnContext(r); ok {
			t.Fatal("authnContext should deny when the tenancy context was never applied")
		}
	})
}

// TestAuditPeriodHandlers_IdentityRejection covers AC-4: the audit-period
// handlers reject missing tenant context AND a credential carrying no user
// id, both before any store call. A nil store proves no DB is touched.
func TestAuditPeriodHandlers_IdentityRejection(t *testing.T) {
	t.Parallel()
	h := New(nil) // *auditor.Store nil; handlers return before using it

	t.Run("no auth context → 401", func(t *testing.T) {
		t.Parallel()
		for _, hf := range []http.HandlerFunc{h.AuditPeriod, h.AuditPeriods} {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/me/audit-period", nil)
			hf(rec, r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (no auth context)", rec.Code)
			}
		}
	})

	t.Run("auth context but empty user id → 401", func(t *testing.T) {
		t.Parallel()
		// Credential + tenancy context present, but UserID empty: the handler
		// must reject at the user-id guard. We must supply a real tenancy
		// context so authnContext passes and the UserID guard is the one that
		// fires.
		tenant := uuid.NewString()
		base := httptest.NewRequest(http.MethodGet, "/v1/me/audit-period", nil)
		ctx := authctx.WithCredential(base.Context(),
			credstore.Credential{ID: "k", TenantID: tenant}) // UserID empty
		ctx = mustTenant(t, ctx, tenant)
		r := base.WithContext(ctx)

		rec := httptest.NewRecorder()
		h.AuditPeriod(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("AuditPeriod with empty user id = %d, want 401", rec.Code)
		}
	})
}

func TestProfileWireFrom(t *testing.T) {
	t.Parallel()
	u := users.User{
		ID:          uuid.New(),
		TenantID:    uuid.New(),
		DisplayName: "Marcus Webb",
		Email:       "m@example.test",
		IdpSubject:  "sub-1",
	}
	t.Run("admin role + timezone set", func(t *testing.T) {
		t.Parallel()
		u2 := u
		u2.TimeZone = "America/Los_Angeles"
		w := profileWireFrom(u2, credstore.Credential{IsAdmin: true, OwnerRoles: []string{"grc_engineer"}})
		if w.TenantRole != "admin" || !w.IsAdmin {
			t.Fatalf("admin not reflected: %+v", w)
		}
		if w.TimeZone == nil || *w.TimeZone != "America/Los_Angeles" {
			t.Fatalf("TimeZone = %v, want pointer to America/Los_Angeles", w.TimeZone)
		}
		if len(w.OwnerRoles) != 1 || w.OwnerRoles[0] != "grc_engineer" {
			t.Fatalf("OwnerRoles not carried: %v", w.OwnerRoles)
		}
	})
	t.Run("user role + no timezone", func(t *testing.T) {
		t.Parallel()
		w := profileWireFrom(u, credstore.Credential{IsAdmin: false})
		if w.TenantRole != "user" || w.IsAdmin {
			t.Fatalf("non-admin should map to user role: %+v", w)
		}
		if w.TimeZone != nil {
			t.Fatalf("empty TimeZone should map to nil, got %v", *w.TimeZone)
		}
	})
}

func TestSessionWireFrom_AndLast4(t *testing.T) {
	t.Parallel()
	issued := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	lastSeen := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	s := sessions.Session{
		ID:         "abcdEFGH1234",
		IssuedAt:   issued,
		LastSeenAt: lastSeen,
		UserAgent:  "ua",
		IPAddress:  "203.0.113.7",
		GeoCountry: "US",
		GeoCity:    "SF",
	}
	t.Run("current session, last-seen set", func(t *testing.T) {
		t.Parallel()
		w := sessionWireFrom(s, "abcdEFGH1234")
		if !w.IsCurrent {
			t.Fatal("IsCurrent should be true when ids match")
		}
		if w.Last4 != "1234" {
			t.Fatalf("Last4 = %q, want 1234", w.Last4)
		}
		if w.LastUsedAt == nil {
			t.Fatal("LastUsedAt should be set when LastSeenAt is non-zero")
		}
	})
	t.Run("non-current session, no last-seen", func(t *testing.T) {
		t.Parallel()
		s2 := s
		s2.LastSeenAt = time.Time{}
		w := sessionWireFrom(s2, "different-id")
		if w.IsCurrent {
			t.Fatal("IsCurrent should be false when ids differ")
		}
		if w.LastUsedAt != nil {
			t.Fatal("LastUsedAt should be nil when LastSeenAt is zero")
		}
	})
	t.Run("empty currentID never marks current", func(t *testing.T) {
		t.Parallel()
		w := sessionWireFrom(s, "")
		if w.IsCurrent {
			t.Fatal("empty currentID must not flag IsCurrent")
		}
	})
}

func TestLast4OfSessionID(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"abcd1234": "1234",
		"abcd":     "abcd", // len <= 4 returns whole
		"abc":      "abc",
		"":         "",
	}
	for in, want := range cases {
		if got := last4OfSessionID(in); got != want {
			t.Fatalf("last4OfSessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCurrentSessionIDFromRequest(t *testing.T) {
	t.Parallel()
	t.Run("no cookie → empty", func(t *testing.T) {
		t.Parallel()
		r := httptest.NewRequest(http.MethodGet, "/v1/me/sessions", nil)
		if got := currentSessionIDFromRequest(r); got != "" {
			t.Fatalf("no cookie should yield empty, got %q", got)
		}
	})
	t.Run("cookie present → value", func(t *testing.T) {
		t.Parallel()
		r := httptest.NewRequest(http.MethodGet, "/v1/me/sessions", nil)
		r.AddCookie(&http.Cookie{Name: sessions.CookieName, Value: "sess-xyz"})
		if got := currentSessionIDFromRequest(r); got != "sess-xyz" {
			t.Fatalf("got %q, want sess-xyz", got)
		}
	})
}

func TestNotificationWireFrom(t *testing.T) {
	t.Parallel()
	read := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	base := notifications.Notification{
		ID:              uuid.New(),
		RecipientUserID: "user-1",
		Type:            "audit_note_reply",
		Payload:         map[string]any{"k": "v"},
		CreatedAt:       time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC),
	}
	t.Run("unread → nil ReadAt", func(t *testing.T) {
		t.Parallel()
		w := notificationWireFrom(base)
		if w.ReadAt != nil {
			t.Fatalf("unread notification should have nil ReadAt, got %v", *w.ReadAt)
		}
		if w.Type != "audit_note_reply" || w.Payload["k"] != "v" {
			t.Fatalf("fields not carried: %+v", w)
		}
	})
	t.Run("read → ReadAt set", func(t *testing.T) {
		t.Parallel()
		n := base
		n.ReadAt = &read
		w := notificationWireFrom(n)
		if w.ReadAt == nil {
			t.Fatal("read notification should have ReadAt set")
		}
	})
}

func TestAssignmentWireFrom(t *testing.T) {
	t.Parallel()
	frozen := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	base := auditor.Assignment{
		AuditPeriodID:      uuid.New(),
		Name:               "FY26 SOC 2",
		FrameworkVersionID: uuid.New(),
		PeriodStart:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		Status:             "active",
		GrantedAt:          time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		GrantedBy:          "ciso",
	}
	t.Run("not frozen", func(t *testing.T) {
		t.Parallel()
		w := assignmentWireFrom(base)
		if w.FrozenAt != nil {
			t.Fatalf("FrozenAt should be nil, got %v", *w.FrozenAt)
		}
		if w.PeriodStart != "2026-01-01" || w.PeriodEnd != "2026-12-31" {
			t.Fatalf("period dates not formatted: %+v", w)
		}
	})
	t.Run("frozen", func(t *testing.T) {
		t.Parallel()
		a := base
		a.FrozenAt = &frozen
		w := assignmentWireFrom(a)
		if w.FrozenAt == nil {
			t.Fatal("FrozenAt should be set when assignment is frozen")
		}
	})
}

func TestMapsEqual(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b map[string]any
		want bool
	}{
		{"both empty", map[string]any{}, map[string]any{}, true},
		{"equal", map[string]any{"x": 1}, map[string]any{"x": 1}, true},
		{"different length", map[string]any{"x": 1}, map[string]any{"x": 1, "y": 2}, false},
		{"different value", map[string]any{"x": 1}, map[string]any{"x": 2}, false},
		{"missing key", map[string]any{"x": 1}, map[string]any{"y": 1}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mapsEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("mapsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
