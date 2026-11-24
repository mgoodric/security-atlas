package historicalimport

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func rec(t time.Time) *evidencev1.EvidenceRecord {
	return &evidencev1.EvidenceRecord{ObservedAt: timestamppb.New(t)}
}

func periodRow(id uuid.UUID, status string, start, end time.Time) dbx.AuditPeriod {
	p := dbx.AuditPeriod{
		ID:          pgtype.UUID{Bytes: id, Valid: true},
		Name:        "p-" + id.String()[:4],
		Status:      status,
		PeriodStart: pgtype.Date{Time: start, Valid: true},
		PeriodEnd:   pgtype.Date{Time: end, Valid: true},
	}
	if status == "frozen" {
		// Frozen after the period ends unless a test says otherwise.
		p.FrozenAt = pgtype.Timestamptz{Time: end.Add(30 * 24 * time.Hour), Valid: true}
	}
	return p
}

// B2 (#1742 review): only a record observed at or before the original
// frozen_at belongs to a frozen population, so only such a record counts as
// a frozen hit or may join a revision.
func TestInFrozenPopulation_ObservedAtBound(t *testing.T) {
	t.Parallel()
	p := periodRow(uuid.New(), "frozen", day(2025, 1, 1), day(2025, 12, 31))
	p.FrozenAt = pgtype.Timestamptz{Time: day(2025, 6, 30).Add(12 * time.Hour), Valid: true}
	cases := []struct {
		at   time.Time
		want bool
	}{
		{day(2025, 3, 1), true},
		{day(2025, 6, 30).Add(12 * time.Hour), true},
		{day(2025, 6, 30).Add(13 * time.Hour), false},
		{day(2025, 9, 1), false},
		{day(2024, 9, 1), false},
	}
	for _, c := range cases {
		if got := inFrozenPopulation(p, c.at); got != c.want {
			t.Errorf("inFrozenPopulation(%s) = %v, want %v", c.at, got, c.want)
		}
	}
	open := periodRow(uuid.New(), "open", day(2025, 1, 1), day(2025, 12, 31))
	if inFrozenPopulation(open, day(2025, 3, 1)) {
		t.Error("an open period has a frozen population")
	}
	// A record after frozen_at but inside the window is not a hit.
	if _, err := planAmendments([]dbx.AuditPeriod{p}, Request{Records: []*evidencev1.EvidenceRecord{rec(day(2025, 9, 1))}}); err != nil {
		t.Errorf("post-freeze record counted as a frozen hit: %v", err)
	}
}

func TestInWindow_CalendarDaysUTC(t *testing.T) {
	t.Parallel()
	p := periodRow(uuid.New(), "frozen", day(2025, 1, 1), day(2025, 12, 31))
	cases := []struct {
		at   time.Time
		want bool
	}{
		{day(2025, 1, 1), true},
		{day(2025, 12, 31).Add(23*time.Hour + 59*time.Minute), true},
		{day(2026, 1, 1), false},
		{day(2024, 12, 31).Add(23 * time.Hour), false},
		// 2025-01-01T02:00+05:00 is 2024-12-31 in UTC.
		{time.Date(2025, 1, 1, 2, 0, 0, 0, time.FixedZone("x", 5*3600)), false},
	}
	for _, c := range cases {
		if got := inWindow(p, c.at); got != c.want {
			t.Errorf("inWindow(%s) = %v, want %v", c.at, got, c.want)
		}
	}
}

func TestPlanAmendments(t *testing.T) {
	t.Parallel()
	h1, h2, open := uuid.New(), uuid.New(), uuid.New()
	periods := []dbx.AuditPeriod{
		periodRow(h1, "frozen", day(2025, 1, 1), day(2025, 6, 30)),
		periodRow(h2, "frozen", day(2025, 7, 1), day(2025, 12, 31)),
		periodRow(open, "open", day(2025, 1, 1), day(2025, 12, 31)),
	}
	inH1 := []*evidencev1.EvidenceRecord{rec(day(2025, 3, 1)), rec(day(2025, 4, 1))}
	both := append(append([]*evidencev1.EvidenceRecord{}, inH1...), rec(day(2025, 9, 1)))

	t.Run("no frozen hit", func(t *testing.T) {
		got, err := planAmendments(periods, Request{Records: []*evidencev1.EvidenceRecord{rec(day(2024, 3, 1))}})
		if err != nil || len(got) != 0 {
			t.Fatalf("got %d, %v", len(got), err)
		}
	})
	t.Run("frozen hit not named", func(t *testing.T) {
		_, err := planAmendments(periods, Request{Records: inH1})
		var fpe *FrozenPeriodError
		if !errors.As(err, &fpe) || len(fpe.Hits) != 1 || fpe.Hits[0].PeriodID != h1 || fpe.Hits[0].Records != 2 {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(err.Error(), h1.String()) {
			t.Fatalf("error does not name the period: %v", err)
		}
	})
	t.Run("named", func(t *testing.T) {
		got, err := planAmendments(periods, Request{Records: inH1, AmendFrozen: []uuid.UUID{h1}})
		if err != nil || len(got) != 1 || uuid.UUID(got[0].ID.Bytes) != h1 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("one named, one not", func(t *testing.T) {
		_, err := planAmendments(periods, Request{Records: both, AmendFrozen: []uuid.UUID{h1}})
		var fpe *FrozenPeriodError
		if !errors.As(err, &fpe) || len(fpe.Hits) != 1 || fpe.Hits[0].PeriodID != h2 {
			t.Fatalf("err = %v, want only h2", err)
		}
	})
	t.Run("named period not hit", func(t *testing.T) {
		_, err := planAmendments(periods, Request{Records: inH1, AmendFrozen: []uuid.UUID{h1, h2}})
		var ate *AmendTargetError
		if !errors.As(err, &ate) || ate.PeriodID != h2 {
			t.Fatalf("err = %v, want AmendTargetError for h2", err)
		}
	})
	t.Run("named open period", func(t *testing.T) {
		_, err := planAmendments(periods, Request{Records: []*evidencev1.EvidenceRecord{rec(day(2024, 3, 1))}, AmendFrozen: []uuid.UUID{open}})
		var ate *AmendTargetError
		if !errors.As(err, &ate) || ate.PeriodID != open {
			t.Fatalf("err = %v, want AmendTargetError for the open period", err)
		}
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()
	ok := []*evidencev1.EvidenceRecord{rec(day(2025, 1, 1))}
	cases := []struct {
		name string
		req  Request
		ok   bool
	}{
		{"ok", Request{Source: "FY25", Records: ok}, true},
		{"no source", Request{Source: "  ", Records: ok}, false},
		{"long source", Request{Source: strings.Repeat("x", MaxSourceLen+1), Records: ok}, false},
		{"no records", Request{Source: "s"}, false},
		{"nil observed_at", Request{Source: "s", Records: []*evidencev1.EvidenceRecord{{}}}, false},
		{"too many", Request{Source: "s", Records: make([]*evidencev1.EvidenceRecord, MaxRecords+1)}, false},
		{"amend without reason", Request{Source: "s", Records: ok, AmendFrozen: []uuid.UUID{uuid.New()}, AmendReason: "  "}, false},
		{"amend with reason", Request{Source: "s", Records: ok, AmendFrozen: []uuid.UUID{uuid.New()}, AmendReason: "auditor asked for FY25 results"}, true},
		{"long amend reason", Request{Source: "s", Records: ok, AmendFrozen: []uuid.UUID{uuid.New()}, AmendReason: strings.Repeat("x", MaxAmendReasonLen+1)}, false},
	}
	for _, c := range cases {
		err := validate(c.req)
		if c.ok != (err == nil) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v is not ErrInvalid", c.name, err)
		}
	}
}

func TestDayRange(t *testing.T) {
	t.Parallel()
	first, last := dayRange([]*evidencev1.EvidenceRecord{
		rec(day(2025, 5, 1).Add(5 * time.Hour)), rec(day(2024, 2, 3).Add(23 * time.Hour)), rec(day(2025, 1, 1)),
	})
	if !first.Equal(day(2024, 2, 3)) || !last.Equal(day(2025, 5, 1)) {
		t.Fatalf("range = %s..%s", first, last)
	}
}

func TestImport_RefusesNonAdminBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	// A nil pool and service: a refusal must return before touching either.
	im := New(nil, nil)
	for _, cred := range []credstore.Credential{
		{ID: "key_conn", TenantID: uuid.NewString()},
		{ID: "system:x", TenantID: uuid.NewString(), IsAdmin: true, System: true},
	} {
		if _, err := im.Import(t.Context(), cred, Request{Source: "s", Records: []*evidencev1.EvidenceRecord{rec(day(2025, 1, 1))}}); !errors.Is(err, ErrNotAdmin) {
			t.Fatalf("cred %s: err = %v, want ErrNotAdmin", cred.ID, err)
		}
	}
}
