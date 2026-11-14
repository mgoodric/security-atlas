package demoprov

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

type fakeQuerier struct {
	calls  int
	gotArg dbx.DemoSeededIDsParams
	rows   []pgtype.UUID
	err    error
}

func (f *fakeQuerier) DemoSeededIDs(_ context.Context, arg dbx.DemoSeededIDsParams) ([]pgtype.UUID, error) {
	f.calls++
	f.gotArg = arg
	return f.rows, f.err
}

func TestLookup_EmptyIDsIssuesNoQuery(t *testing.T) {
	t.Parallel()
	q := &fakeQuerier{}
	got, err := Lookup(context.Background(), q, TableControls, nil)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if q.calls != 0 {
		t.Errorf("queries = %d, want 0", q.calls)
	}
	if got.Has(uuid.New()) {
		t.Error("empty lookup marked an id as demo")
	}
}

func TestLookup_MarksOnlyReturnedIDs(t *testing.T) {
	t.Parallel()
	demo, real := uuid.New(), uuid.New()
	q := &fakeQuerier{rows: []pgtype.UUID{{Bytes: demo, Valid: true}, {}}}
	got, err := Lookup(context.Background(), q, TableEvidence, []uuid.UUID{demo, real})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if q.gotArg.TableName != TableEvidence || len(q.gotArg.Ids) != 2 {
		t.Errorf("arg = %+v, want table %q and 2 ids", q.gotArg, TableEvidence)
	}
	if !got.Has(demo) {
		t.Error("demo id not marked")
	}
	if got.Has(real) {
		t.Error("real id marked as demo")
	}
}

func TestLookup_WrapsQueryError(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	_, err := Lookup(context.Background(), &fakeQuerier{err: boom}, TableControls, []uuid.UUID{uuid.New()})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestSet_ZeroValueMarksNothing(t *testing.T) {
	t.Parallel()
	var s Set
	if s.Has(uuid.New()) {
		t.Error("nil Set marked an id")
	}
}
