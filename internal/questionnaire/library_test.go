// Slice 155 — AnswerLibrary unit tests.
//
// These tests use a fake LibraryReader so we can assert the query shape
// and ranking behavior without a live database. The integration test
// (under //go:build integration) covers the RLS / cross-tenant path.
package questionnaire

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeReader records the SQL it's asked to run and returns canned rows.
// Implements the LibraryReader interface.
type fakeReader struct {
	lastSQL  string
	lastArgs []any
	rows     pgx.Rows
	err      error
}

func (f *fakeReader) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	f.lastSQL = sql
	f.lastArgs = args
	return f.rows, f.err
}

// fakeRows is a minimal pgx.Rows implementation.
type fakeRows struct {
	data [][]any
	idx  int
}

func (r *fakeRows) Next() bool { r.idx++; return r.idx-1 < len(r.data) }
func (r *fakeRows) Scan(dst ...any) error {
	src := r.data[r.idx-1]
	for i, d := range dst {
		switch v := d.(type) {
		case *string:
			*v = src[i].(string)
		default:
			_ = v
			rv := reflect.ValueOf(d).Elem()
			rv.Set(reflect.ValueOf(src[i]))
		}
	}
	return nil
}
func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func TestSuggestForAnchor_RejectsEmptyAnchor(t *testing.T) {
	_, err := SuggestForAnchor(context.Background(), &fakeReader{}, "", 10)
	if err == nil {
		t.Fatal("expected error for empty anchor, got nil")
	}
}

func TestSuggestForAnchor_QueryShape(t *testing.T) {
	r := &fakeReader{rows: &fakeRows{data: nil}}
	_, err := SuggestForAnchor(context.Background(), r, "IAC-06", 5)
	if err != nil {
		t.Fatalf("SuggestForAnchor: %v", err)
	}
	if r.lastSQL == "" {
		t.Fatal("expected SQL to be issued")
	}
	// The SQL must be tenant-scoped via RLS (no tenant_id in WHERE — RLS
	// enforces) and ordered most-recent-first.
	if !containsAll(r.lastSQL, []string{"scf_anchor_id = $1", "ORDER BY updated_at DESC", "LIMIT $2"}) {
		t.Fatalf("query shape regression: %q", r.lastSQL)
	}
	if len(r.lastArgs) != 2 {
		t.Fatalf("expected 2 args, got %d", len(r.lastArgs))
	}
	if got := r.lastArgs[0].(string); got != "IAC-06" {
		t.Fatalf("expected anchor IAC-06, got %q", got)
	}
	if got := r.lastArgs[1].(int); got != 5 {
		t.Fatalf("expected limit 5, got %d", got)
	}
}

func TestSuggestForAnchor_ClampsBadLimits(t *testing.T) {
	r := &fakeReader{rows: &fakeRows{data: nil}}
	_, _ = SuggestForAnchor(context.Background(), r, "IAC-06", -1)
	if got := r.lastArgs[1].(int); got != DefaultSuggestionLimit {
		t.Fatalf("expected DefaultSuggestionLimit, got %d", got)
	}

	_, _ = SuggestForAnchor(context.Background(), r, "IAC-06", 9999)
	if got := r.lastArgs[1].(int); got != DefaultSuggestionLimit {
		t.Fatalf("expected DefaultSuggestionLimit, got %d", got)
	}
}

func TestSuggestForAnchor_PropagatesQueryError(t *testing.T) {
	r := &fakeReader{err: errors.New("boom")}
	_, err := SuggestForAnchor(context.Background(), r, "IAC-06", 10)
	if err == nil {
		t.Fatal("expected error to propagate, got nil")
	}
}

// Slice 319 — exercise the Scan() happy path through the fakeReader so
// the row-iteration branch in SuggestForAnchor is hit without spinning
// up Postgres.
func TestSuggestForAnchor_ScansRows(t *testing.T) {
	now := time.Now().UTC()
	r := &fakeReader{rows: &fakeRows{data: [][]any{
		{"row-uuid-1", "IAC-06", "MFA is enforced.", "ACME-CAIQ-2026", now},
		{"row-uuid-2", "IAC-06", "Second prior.", "OtherSrc", now.Add(-time.Hour)},
	}}}
	got, err := SuggestForAnchor(context.Background(), r, "IAC-06", 10)
	if err != nil {
		t.Fatalf("SuggestForAnchor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 scanned rows, got %d", len(got))
	}
	if got[0].ID != "row-uuid-1" || got[0].CanonicalText != "MFA is enforced." {
		t.Fatalf("first row stitched wrong: %+v", got[0])
	}
	if got[1].SourceLabel != "OtherSrc" {
		t.Fatalf("second row stitched wrong: %+v", got[1])
	}
}

// Slice 319 — when the LIMIT is exactly DefaultSuggestionLimit, the
// clamp must NOT kick in.
func TestSuggestForAnchor_AcceptsDefaultLimit(t *testing.T) {
	r := &fakeReader{rows: &fakeRows{data: nil}}
	_, _ = SuggestForAnchor(context.Background(), r, "IAC-06", DefaultSuggestionLimit)
	if got := r.lastArgs[1].(int); got != DefaultSuggestionLimit {
		t.Fatalf("expected limit %d preserved, got %d", DefaultSuggestionLimit, got)
	}
}

func containsAll(s string, needles []string) bool {
	for _, n := range needles {
		if !contains(s, n) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
