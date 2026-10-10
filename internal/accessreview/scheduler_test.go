package accessreview

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReminderSchedulerSweepOnceContinuesAfterTenantFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tenantA := uuid.New()
	tenantB := uuid.New()
	wantErr := errors.New("tenant failed")
	var fired []uuid.UUID

	s := &ReminderScheduler{
		logger: slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		listTenants: func(context.Context, time.Time) ([]uuid.UUID, error) {
			return []uuid.UUID{tenantA, tenantB}, nil
		},
		fireTenant: func(_ context.Context, tenantID uuid.UUID, _ time.Time) (int, error) {
			fired = append(fired, tenantID)
			if tenantID == tenantA {
				return 0, wantErr
			}
			return 2, nil
		},
	}

	rep, err := s.SweepOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.TenantsSwept != 1 || rep.TenantFailures != 1 || rep.NotificationsCreated != 2 {
		t.Fatalf("report = %+v, want 1 swept / 1 failure / 2 notifications", rep)
	}
	if len(fired) != 2 || fired[0] != tenantA || fired[1] != tenantB {
		t.Fatalf("fired tenants = %v, want [%s %s]", fired, tenantA, tenantB)
	}
}
