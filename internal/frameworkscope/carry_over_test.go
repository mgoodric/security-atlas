package frameworkscope

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// fakeCarryTarget replays one lock result and one read result per attempt.
type fakeCarryTarget struct {
	locks    []error  // nil = the lock returns a row
	reads    []string // state the plain read returns; "" = no row
	lockCall int
	readCall int
}

func (f *fakeCarryTarget) LockOpenFrameworkScope(context.Context, dbx.LockOpenFrameworkScopeParams) (dbx.FrameworkScope, error) {
	err := f.locks[f.lockCall]
	f.lockCall++
	if err != nil {
		return dbx.FrameworkScope{}, err
	}
	return dbx.FrameworkScope{State: StateDraft, Name: "locked"}, nil
}

func (f *fakeCarryTarget) GetFrameworkScopeByID(context.Context, dbx.GetFrameworkScopeByIDParams) (dbx.FrameworkScope, error) {
	st := f.reads[f.readCall]
	f.readCall++
	if st == "" {
		return dbx.FrameworkScope{}, pgx.ErrNoRows
	}
	return dbx.FrameworkScope{State: st, Name: "unlocked"}, nil
}

// lockCarryOverTarget never returns an open revision it has not locked: a
// revision reopened between the lock and the plain read is locked on retry
// (#2179 review).
func TestLockCarryOverTarget(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		locks     []error
		reads     []string
		wantName  string
		wantState string
		wantErr   error
		anyErr    bool
		wantLocks int
	}{
		{name: "open row is locked", locks: []error{nil}, wantName: "locked", wantLocks: 1},
		{name: "frozen row returned unlocked", locks: []error{pgx.ErrNoRows}, reads: []string{StateActivated}, wantName: "unlocked", wantState: StateActivated, wantLocks: 1},
		{name: "approved row returned unlocked", locks: []error{pgx.ErrNoRows}, reads: []string{StateApproved}, wantName: "unlocked", wantState: StateApproved, wantLocks: 1},
		{name: "unknown row", locks: []error{pgx.ErrNoRows}, reads: []string{""}, wantErr: ErrNotFound, wantLocks: 1},
		{name: "reopened to draft is locked on retry", locks: []error{pgx.ErrNoRows, nil}, reads: []string{StateDraft}, wantName: "locked", wantLocks: 2},
		{name: "reopened to review is locked on retry", locks: []error{pgx.ErrNoRows, nil}, reads: []string{StateReview}, wantName: "locked", wantLocks: 2},
		{
			name:   "never settles",
			locks:  []error{pgx.ErrNoRows, pgx.ErrNoRows, pgx.ErrNoRows},
			reads:  []string{StateDraft, StateDraft, StateDraft},
			anyErr: true, wantLocks: carryLockAttempts,
		},
		{name: "lock error", locks: []error{errors.New("boom")}, anyErr: true, wantLocks: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := &fakeCarryTarget{locks: tc.locks, reads: tc.reads}
			row, err := lockCarryOverTarget(context.Background(), f, uuid.New(), uuid.New())
			if f.lockCall != tc.wantLocks {
				t.Fatalf("lock calls = %d, want %d", f.lockCall, tc.wantLocks)
			}
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			case tc.anyErr:
				if err == nil {
					t.Fatalf("err = nil, want an error; row %+v", row)
				}
				return
			case err != nil:
				t.Fatalf("err = %v", err)
			}
			if row.Name != tc.wantName {
				t.Fatalf("row = %q, want %q", row.Name, tc.wantName)
			}
			if tc.wantState != "" && row.State != tc.wantState {
				t.Fatalf("state = %q, want %q", row.State, tc.wantState)
			}
		})
	}
}
