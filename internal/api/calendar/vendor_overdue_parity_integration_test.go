//go:build integration

// #2232 — one vendor overdue rule. The vendor overdue list
// (ListOverdueVendors), the burndown (CountVendorsForBurndown), the
// compliance calendar's vendor-review events and the Go flag
// (vendor.IsOverdueAsOf) agree at the UTC midnight that ends a review's due
// day: nobody is overdue at its last instant, everybody is at the next.

package calendar_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

func TestVendorOverdue_ListBurndownCalendarAgreeAtMidnight(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)

	// Every vendor's review falls due on dueDay. The tenant has no
	// `tenants` row, so the initial-review window is the 30-day default.
	dueDay := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	seed := func(name, cadence string, lastReview *time.Time, createdAt time.Time) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO vendors (id, tenant_id, name, criticality, review_cadence, last_review_date, created_at)
			VALUES ($1, $2, $3, 'high', $4, $5, $6)
		`, id, tenant, name, cadence, lastReview, createdAt); err != nil {
			t.Fatalf("seed vendor %s: %v", name, err)
		}
		return id
	}
	d := func(y int, m time.Month, day int) *time.Time {
		v := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
		return &v
	}
	ids := map[uuid.UUID]string{}
	// Monthly, reviewed a calendar month before. The old Go flag (a 30-day
	// Duration compared as a timestamp) put its due at 2026-03-12 00:00.
	ids[seed("monthly", "monthly", d(2026, 2, 10), dueDay.AddDate(-1, 0, 0))] = "monthly"
	// Quarterly: Dec 10 + 3 months. The old 91-day Duration put it on Mar 11.
	ids[seed("quarterly", "quarterly", d(2025, 12, 10), dueDay.AddDate(-1, 0, 0))] = "quarterly"
	// Annual: reviewed a year before.
	ids[seed("annual", "annual", d(2025, 3, 10), dueDay.AddDate(-2, 0, 0))] = "annual"
	// Never reviewed, created late on the day 30 days before: its first
	// review is due on dueDay (the UTC date of created_at + 30).
	ids[seed("never reviewed", "annual", nil, dueDay.AddDate(0, 0, -30).Add(22*time.Hour))] = "never reviewed"

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	vendors := vendor.NewStore(app)
	cal := calendar.NewStore(app)

	for _, tc := range []struct {
		name        string
		now         time.Time
		wantOverdue bool
	}{
		{"last instant of the due day", dueDay.Add(24*time.Hour - time.Nanosecond), false},
		{"first instant after the due day", dueDay.Add(24 * time.Hour), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := []string{}
			if tc.wantOverdue {
				for _, n := range ids {
					want = append(want, n)
				}
			}
			sort.Strings(want)

			// 1. The overdue list.
			listed, err := vendors.List(ctx, vendor.ListFilter{OverdueOnly: true, Cutoff: tc.now})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			got := []string{}
			for _, v := range listed {
				got = append(got, v.Name)
			}
			sort.Strings(got)
			if !equal(got, want) {
				t.Errorf("overdue list = %v, want %v", got, want)
			}

			// 2. The burndown.
			bd, err := vendors.Burndown(ctx, tc.now, nil)
			if err != nil {
				t.Fatalf("Burndown: %v", err)
			}
			var overdue int64
			for _, b := range bd.Bands {
				overdue += b.Overdue
			}
			if overdue != int64(len(want)) {
				t.Errorf("burndown overdue = %d, want %d", overdue, len(want))
			}

			// 3. The calendar: one review event per vendor, on dueDay.
			rows, err := cal.ListEvents(ctx, dueDay.AddDate(0, 0, -1), dueDay.AddDate(0, 0, 2), tc.now, "vendor", 100)
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			got = []string{}
			seen := 0
			for _, r := range rows {
				id, err := uuid.Parse(r.EventID)
				if err != nil {
					continue // a renewal row; none are seeded
				}
				name, ok := ids[id]
				if !ok {
					continue
				}
				seen++
				if !r.StartsAt.Time.Equal(dueDay) {
					t.Errorf("%s calendar event on %s, want %s", name, r.StartsAt.Time.UTC(), dueDay)
				}
				if r.Status == "overdue" {
					got = append(got, name)
				}
			}
			if seen != len(ids) {
				t.Errorf("calendar has %d of the %d vendor reviews", seen, len(ids))
			}
			sort.Strings(got)
			if !equal(got, want) {
				t.Errorf("calendar overdue = %v, want %v", got, want)
			}

			// 4. The Go flag, on the rows the store hydrates.
			for id, name := range ids {
				v, err := vendors.Get(ctx, id)
				if err != nil {
					t.Fatalf("Get %s: %v", name, err)
				}
				if due := v.ReviewDueDate(); due == nil || !due.Equal(dueDay) {
					t.Errorf("%s ReviewDueDate = %v, want %s", name, due, dueDay)
				}
				if got := v.IsOverdueAsOf(tc.now); got != tc.wantOverdue {
					t.Errorf("%s IsOverdueAsOf(%s) = %v, want %v", name, tc.now, got, tc.wantOverdue)
				}
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
