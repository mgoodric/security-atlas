package tenancy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeRows is a minimal pgx.Rows over in-memory tenant tuples.
type fakeRows struct {
	data [][3]any
	i    int
	err  error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map                         { return nil }

func (r *fakeRows) Next() bool {
	if r.i >= len(r.data) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.i-1]
	*dest[0].(*uuid.UUID) = row[0].(uuid.UUID)
	*dest[1].(*string) = row[1].(string)
	*dest[2].(*string) = row[2].(string)
	return nil
}

type fakeQuerier struct {
	rows *fakeRows
	err  error
	args []any
}

func (q *fakeQuerier) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	q.args = args
	if q.err != nil {
		return nil, q.err
	}
	return q.rows, nil
}

func TestResolveImplicitTenant(t *testing.T) {
	t.Parallel()

	a := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	b := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	t.Run("none", func(t *testing.T) {
		t.Parallel()
		q := &fakeQuerier{rows: &fakeRows{}}
		_, err := tenancy.ResolveImplicitTenant(context.Background(), q)
		if !errors.Is(err, tenancy.ErrNoTenants) {
			t.Fatalf("err = %v, want ErrNoTenants", err)
		}
		// The all-zeros tenant is excluded by the query itself.
		if len(q.args) != 1 || q.args[0] != uuid.Nil {
			t.Fatalf("query args = %v, want [uuid.Nil]", q.args)
		}
	})

	t.Run("one", func(t *testing.T) {
		t.Parallel()
		q := &fakeQuerier{rows: &fakeRows{data: [][3]any{{a, "acme", "Acme"}}}}
		got, err := tenancy.ResolveImplicitTenant(context.Background(), q)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != (tenancy.TenantRef{ID: a, Slug: "acme", Name: "Acme"}) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("several lists them", func(t *testing.T) {
		t.Parallel()
		q := &fakeQuerier{rows: &fakeRows{data: [][3]any{{a, "acme", "Acme"}, {b, "", "Legacy"}}}}
		_, err := tenancy.ResolveImplicitTenant(context.Background(), q)
		var amb *tenancy.AmbiguousTenantError
		if !errors.As(err, &amb) {
			t.Fatalf("err = %v, want *AmbiguousTenantError", err)
		}
		if len(amb.Tenants) != 2 {
			t.Fatalf("tenants = %v", amb.Tenants)
		}
		msg := err.Error()
		for _, want := range []string{a.String(), "slug acme", `"Acme"`, b.String(), "slug -", `"Legacy"`} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q missing %q", msg, want)
			}
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		_, err := tenancy.ResolveImplicitTenant(context.Background(), &fakeQuerier{err: boom})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	})

	t.Run("rows error", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		_, err := tenancy.ResolveImplicitTenant(context.Background(), &fakeQuerier{rows: &fakeRows{err: boom}})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	})
}
