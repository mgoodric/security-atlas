//go:build integration

// #2145: the onboarding hint dismissals store, against real Postgres + RLS.
// Seeds through seedUser (integration_test.go); reads and writes through the
// app pool.
package userprefs_test

import (
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/userprefs"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHints_DismissResetAndShowAgain(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantID, userID := seedUser(t, admin)
	ctx := dbtest.WithTenantCtx(t, tenantID.String())
	s := userprefs.NewStore(app)

	got, err := s.DismissedHints(ctx, tenantID, userID)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("fresh DismissedHints = %v, %v; want [] and nil", got, err)
	}

	dismiss := userprefs.HintChange{Dismissed: map[string]bool{"controls": true}}
	for range 2 { // the second dismissal is a no-op, not a conflict
		if err := s.ApplyHints(ctx, tenantID, userID, dismiss); err != nil {
			t.Fatalf("ApplyHints dismiss: %v", err)
		}
	}
	if got, _ := s.DismissedHints(ctx, tenantID, userID); !slices.Equal(got, []string{"controls"}) {
		t.Fatalf("after dismiss = %v; want [controls]", got)
	}

	if err := s.ApplyHints(ctx, tenantID, userID, userprefs.HintChange{Dismissed: map[string]bool{"controls": false}}); err != nil {
		t.Fatalf("ApplyHints show again: %v", err)
	}
	if got, _ := s.DismissedHints(ctx, tenantID, userID); len(got) != 0 {
		t.Fatalf("after show-again = %v; want []", got)
	}

	if err := s.ApplyHints(ctx, tenantID, userID, dismiss); err != nil {
		t.Fatalf("ApplyHints re-dismiss: %v", err)
	}
	if err := s.ApplyHints(ctx, tenantID, userID, userprefs.HintChange{Reset: true}); err != nil {
		t.Fatalf("ApplyHints reset: %v", err)
	}
	if got, _ := s.DismissedHints(ctx, tenantID, userID); len(got) != 0 {
		t.Fatalf("after reset = %v; want []", got)
	}
}

func TestHints_RLSTenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA, userA := seedUser(t, admin)
	tenantB, _ := seedUser(t, admin)
	s := userprefs.NewStore(app)

	ctxA := dbtest.WithTenantCtx(t, tenantA.String())
	if err := s.ApplyHints(ctxA, tenantA, userA, userprefs.HintChange{Dismissed: map[string]bool{"controls": true}}); err != nil {
		t.Fatalf("ApplyHints: %v", err)
	}
	// Tenant B's context asking for tenant A's user sees nothing.
	ctxB := dbtest.WithTenantCtx(t, tenantB.String())
	if got, err := s.DismissedHints(ctxB, tenantA, userA); err != nil || len(got) != 0 {
		t.Fatalf("cross-tenant DismissedHints = %v, %v; want []", got, err)
	}
}
