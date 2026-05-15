// Unit tests for the platform/Status read/write helpers (slice 073).
//
// These are pure unit tests against fakes for the DBExecutor interface —
// they cover the small bits of logic that don't require a real Postgres:
// the WritePoolUnset error paths, the IsFirstInstall NULL-vs-non-NULL
// branch, and the ResetBootstrap force gate. The cross-process RLS
// semantics + the singleton CHECK constraint live in
// status_integration_test.go (real DB).

package platform_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/platform"
)

// fakeRow is a minimal pgx.Row implementation that returns either a value
// scanned into the first dest or an error.
type fakeRow struct {
	val *time.Time
	err error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 0 {
		return errors.New("no scan dest")
	}
	tp, ok := dest[0].(**time.Time)
	if !ok {
		return errors.New("scan dest is not **time.Time")
	}
	*tp = r.val
	return nil
}

type fakeExec struct {
	rows         []*fakeRow
	execTags     []pgconn.CommandTag
	execErrs     []error
	queryCalls   int
	execCalls    int
	lastExecArgs []any
}

func (f *fakeExec) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	r := f.rows[f.queryCalls]
	f.queryCalls++
	return r
}

func (f *fakeExec) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tag := f.execTags[f.execCalls]
	err := f.execErrs[f.execCalls]
	f.execCalls++
	f.lastExecArgs = args
	return tag, err
}

func TestIsFirstInstall_NullMeansFresh(t *testing.T) {
	read := &fakeExec{rows: []*fakeRow{{val: nil}}}
	s := platform.NewStatus(read, nil)
	got, err := s.IsFirstInstall(context.Background())
	if err != nil {
		t.Fatalf("IsFirstInstall: %v", err)
	}
	if !got {
		t.Fatalf("IsFirstInstall = false; want true on NULL first_signin_at")
	}
}

func TestIsFirstInstall_NonNullMeansAlreadyUsed(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	read := &fakeExec{rows: []*fakeRow{{val: &now}}}
	s := platform.NewStatus(read, nil)
	got, err := s.IsFirstInstall(context.Background())
	if err != nil {
		t.Fatalf("IsFirstInstall: %v", err)
	}
	if got {
		t.Fatalf("IsFirstInstall = true; want false when first_signin_at is set")
	}
}

func TestIsFirstInstall_ReadError(t *testing.T) {
	read := &fakeExec{rows: []*fakeRow{{err: errors.New("kaboom")}}}
	s := platform.NewStatus(read, nil)
	_, err := s.IsFirstInstall(context.Background())
	if err == nil {
		t.Fatalf("IsFirstInstall succeeded; want error")
	}
}

func TestMarkFirstSignin_WritePoolUnset(t *testing.T) {
	s := platform.NewStatus(&fakeExec{}, nil)
	_, err := s.MarkFirstSignin(context.Background(), time.Now())
	if !errors.Is(err, platform.ErrWriteNotConfigured) {
		t.Fatalf("MarkFirstSignin err = %v; want ErrWriteNotConfigured", err)
	}
}

func TestMarkFirstSignin_FirstWrite(t *testing.T) {
	write := &fakeExec{
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 1")},
		execErrs: []error{nil},
	}
	s := platform.NewStatus(&fakeExec{}, write)
	did, err := s.MarkFirstSignin(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("MarkFirstSignin: %v", err)
	}
	if !did {
		t.Fatalf("didWrite = false; want true on the first flip")
	}
}

func TestMarkFirstSignin_IdempotentNoop(t *testing.T) {
	write := &fakeExec{
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 0")},
		execErrs: []error{nil},
	}
	s := platform.NewStatus(&fakeExec{}, write)
	did, err := s.MarkFirstSignin(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("MarkFirstSignin: %v", err)
	}
	if did {
		t.Fatalf("didWrite = true; want false on a subsequent idempotent call")
	}
}

func TestResetBootstrap_ForbiddenWithoutForce(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	write := &fakeExec{rows: []*fakeRow{{val: &now}}}
	s := platform.NewStatus(&fakeExec{}, write)
	err := s.ResetBootstrap(context.Background(), false)
	if !errors.Is(err, platform.ErrResetForbidden) {
		t.Fatalf("ResetBootstrap err = %v; want ErrResetForbidden", err)
	}
}

func TestResetBootstrap_AllowedWithForce(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	write := &fakeExec{
		rows:     []*fakeRow{{val: &now}},
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 1")},
		execErrs: []error{nil},
	}
	s := platform.NewStatus(&fakeExec{}, write)
	if err := s.ResetBootstrap(context.Background(), true); err != nil {
		t.Fatalf("ResetBootstrap force=true: %v", err)
	}
}

func TestResetBootstrap_AllowedOnFreshInstall(t *testing.T) {
	write := &fakeExec{
		rows:     []*fakeRow{{val: nil}},
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("UPDATE 1")},
		execErrs: []error{nil},
	}
	s := platform.NewStatus(&fakeExec{}, write)
	if err := s.ResetBootstrap(context.Background(), false); err != nil {
		t.Fatalf("ResetBootstrap on fresh install: %v", err)
	}
}
