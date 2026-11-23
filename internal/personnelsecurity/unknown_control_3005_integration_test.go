//go:build integration

// #3005 — a worker event bound to a control UUID the tenant does not have
// still creates its checklist, without a control (D2's fallback), against
// the real foreign key. The delivery is counted and acked, and a
// redelivery finds the same checklist instead of creating a second one.
//
// In package personnelsecurity so it drives handle() with the unit tests'
// fakeMsg against a real Store.

package personnelsecurity

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestHandleUnknownBoundControlCreatesNilControlChecklist(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, migrate,
		"personnel_security_checklist_items",
		"personnel_security_checklists",
	)
	s := newTestSubscriber(NewStore(app))
	now := time.Now().UTC()
	w := terminatedWorker(now, now.Add(-24*time.Hour))
	bound := uuid.New()

	before := UnknownControlEvents()
	for delivery := 1; delivery <= 2; delivery++ {
		msg := buildWorkerMsg(t, w, bound.String(), tenant)
		s.handle(context.Background(), msg)
		if acks, naks, terms := msg.outcome(); acks != 1 || naks != 0 || terms != 0 {
			t.Fatalf("delivery %d: acks/naks/terms = %d/%d/%d, want 1/0/0", delivery, acks, naks, terms)
		}
	}
	if got := UnknownControlEvents() - before; got < 1 {
		t.Fatalf("unknown control count delta = %d, want >= 1", got)
	}

	var n, withControl int
	if err := migrate.QueryRow(context.Background(), `
		SELECT count(*), count(control_id) FROM personnel_security_checklists WHERE tenant_id = $1`,
		tenant).Scan(&n, &withControl); err != nil {
		t.Fatalf("count checklists: %v", err)
	}
	if n != 1 || withControl != 0 {
		t.Fatalf("checklists = %d (with a control: %d), want 1 with a NULL control_id", n, withControl)
	}
}
