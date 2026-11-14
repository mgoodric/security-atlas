package tenancy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #1913: the anonymous login path reads at most two tenant ids.
func TestResolveImplicitTenantID(t *testing.T) {
	t.Parallel()

	a := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	b := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	t.Run("query is id-only and bounded", func(t *testing.T) {
		t.Parallel()
		q := &fakeQuerier{rows: &fakeRows{}}
		_, _ = tenancy.ResolveImplicitTenantID(context.Background(), q)
		const want = `SELECT id FROM tenants WHERE id <> $1 ORDER BY created_at, id LIMIT 2`
		if q.sql != want {
			t.Fatalf("sql = %q, want %q", q.sql, want)
		}
		if len(q.args) != 1 || q.args[0] != uuid.Nil {
			t.Fatalf("query args = %v, want [uuid.Nil]", q.args)
		}
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()
		_, err := tenancy.ResolveImplicitTenantID(context.Background(), &fakeQuerier{rows: &fakeRows{}})
		if !errors.Is(err, tenancy.ErrNoTenants) {
			t.Fatalf("err = %v, want ErrNoTenants", err)
		}
	})

	t.Run("one", func(t *testing.T) {
		t.Parallel()
		q := &fakeQuerier{rows: &fakeRows{data: [][3]any{{a}}}}
		got, err := tenancy.ResolveImplicitTenantID(context.Background(), q)
		if err != nil || got != a {
			t.Fatalf("got %v, %v; want %v, nil", got, err, a)
		}
	})

	t.Run("two is ambiguous", func(t *testing.T) {
		t.Parallel()
		q := &fakeQuerier{rows: &fakeRows{data: [][3]any{{a}, {b}}}}
		got, err := tenancy.ResolveImplicitTenantID(context.Background(), q)
		var amb *tenancy.AmbiguousTenantError
		if !errors.As(err, &amb) || got != uuid.Nil {
			t.Fatalf("got %v, %v; want uuid.Nil, *AmbiguousTenantError", got, err)
		}
		if len(amb.Tenants) != 2 || !strings.Contains(err.Error(), b.String()) {
			t.Fatalf("tenants = %v", amb.Tenants)
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		_, err := tenancy.ResolveImplicitTenantID(context.Background(), &fakeQuerier{err: boom})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	})
}
