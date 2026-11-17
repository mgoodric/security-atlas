package oscal

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scope"
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// #2269: an excepted cell is linked to the exception that was in force at
// the evaluation's instant and whose predicate covers the cell.
func TestExceptedCells_ResolvesTheAcceptingException(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	ctrl := uuid.New()
	prod := scope.Cell{ID: uuid.New(), Dimensions: map[string]string{"environment": "prod"}}
	dev := scope.Cell{ID: uuid.New(), Dimensions: map[string]string{"environment": "dev"}}
	cells := map[uuid.UUID]scope.Cell{prod.ID: prod, dev.ID: dev}

	approver := "user-approver"
	base := dbx.ListActivatedExceptionsForExportRow{
		ControlID:   pgUUID(ctrl),
		ApprovedBy:  &approver,
		ApprovedAt:  ts(at.Add(-48 * time.Hour)),
		ActivatedAt: ts(at.Add(-24 * time.Hour)),
		ExpiresAt:   ts(at.Add(30 * 24 * time.Hour)),
		Status:      "active",
	}
	mk := func(predicate string, mut func(*dbx.ListActivatedExceptionsForExportRow)) dbx.ListActivatedExceptionsForExportRow {
		x := base
		x.ID = pgUUID(uuid.New())
		x.ScopeCellPredicate = []byte(predicate)
		if mut != nil {
			mut(&x)
		}
		return x
	}
	prodOnly := `{"op":"eq","dim":"environment","value":"prod"}`
	covering := mk(prodOnly, nil)
	otherCell := mk(`{"op":"eq","dim":"environment","value":"dev"}`, nil)
	revokedBefore := mk(prodOnly, func(x *dbx.ListActivatedExceptionsForExportRow) { x.RevokedAt = ts(at.Add(-time.Hour)) })
	expiredBefore := mk(prodOnly, func(x *dbx.ListActivatedExceptionsForExportRow) { x.ExpiresAt = ts(at.Add(-time.Hour)) })
	startsLater := mk(prodOnly, func(x *dbx.ListActivatedExceptionsForExportRow) { x.EffectiveFrom = ts(at.Add(time.Hour)) })
	revokedAfter := mk(prodOnly, func(x *dbx.ListActivatedExceptionsForExportRow) {
		x.RevokedAt = ts(at.Add(time.Hour))
		x.Status = "revoked"
	})
	otherControl := mk(`{}`, func(x *dbx.ListActivatedExceptionsForExportRow) { x.ControlID = pgUUID(uuid.New()) })

	rows := []dbx.ListExceptedEvaluationsAsOfRow{{
		ID:          pgUUID(uuid.New()),
		ControlID:   pgUUID(ctrl),
		ScopeCellID: pgUUID(prod.ID),
		EvaluatedAt: ts(at),
	}}
	got := exceptedCells(rows, []dbx.ListActivatedExceptionsForExportRow{
		covering, otherCell, revokedBefore, expiredBefore, startsLater, revokedAfter, otherControl,
	}, cells)

	if len(got) != 1 {
		t.Fatalf("got %d cells, want 1", len(got))
	}
	ids := map[string]bool{}
	for _, x := range got[0].Exceptions {
		ids[x.Id] = true
	}
	want := map[string]bool{
		uuid.UUID(covering.ID.Bytes).String():     true,
		uuid.UUID(revokedAfter.ID.Bytes).String(): true,
	}
	if len(ids) != len(want) {
		t.Fatalf("exceptions = %v, want %v", ids, want)
	}
	for id := range want {
		if !ids[id] {
			t.Fatalf("exceptions = %v, missing %s", ids, id)
		}
	}
	x := got[0].Exceptions[0]
	if x.ApprovedBy != approver || x.ExpiresAt == "" {
		t.Fatalf("accepted exception = %+v, want approver and expiry", x)
	}
	if got[0].ScopeCellId != prod.ID.String() {
		t.Fatalf("scope_cell_id = %q, want %q", got[0].ScopeCellId, prod.ID)
	}
}

// A whole-tenant row is covered only by an always-true predicate, and a row
// whose cell or exception cannot be resolved is still emitted.
func TestExceptedCells_WholeTenantAndUnresolved(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	ctrl := uuid.New()
	everyCell := dbx.ListActivatedExceptionsForExportRow{
		ID:                 pgUUID(uuid.New()),
		ControlID:          pgUUID(ctrl),
		ScopeCellPredicate: []byte(`{}`),
		ActivatedAt:        ts(at.Add(-time.Hour)),
		ExpiresAt:          ts(at.Add(time.Hour)),
	}
	narrow := everyCell
	narrow.ID = pgUUID(uuid.New())
	narrow.ScopeCellPredicate = []byte(`{"op":"eq","dim":"environment","value":"prod"}`)

	rows := []dbx.ListExceptedEvaluationsAsOfRow{
		{ID: pgUUID(uuid.New()), ControlID: pgUUID(ctrl), EvaluatedAt: ts(at)},
		{ID: pgUUID(uuid.New()), ControlID: pgUUID(ctrl), ScopeCellID: pgUUID(uuid.New()), EvaluatedAt: ts(at)},
	}
	got := exceptedCells(rows, []dbx.ListActivatedExceptionsForExportRow{everyCell, narrow}, map[uuid.UUID]scope.Cell{})
	if len(got) != 2 {
		t.Fatalf("got %d cells, want 2", len(got))
	}
	if n := len(got[0].Exceptions); n != 1 || got[0].Exceptions[0].Id != uuid.UUID(everyCell.ID.Bytes).String() {
		t.Fatalf("whole-tenant row exceptions = %v, want only the always-true one", got[0].Exceptions)
	}
	if got[0].ScopeCellId != "" {
		t.Fatalf("whole-tenant row scope_cell_id = %q, want empty", got[0].ScopeCellId)
	}
	if n := len(got[1].Exceptions); n != 0 {
		t.Fatalf("unknown cell exceptions = %d, want 0 (cell no longer exists)", n)
	}
}
