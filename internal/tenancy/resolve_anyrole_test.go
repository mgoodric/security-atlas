package tenancy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeTx is the slice of pgx.Tx that ResolveImplicitTenantAnyRole drives:
// one role lookup, an optional SET LOCAL ROLE, the tenants query, then
// commit or rollback. Unused pgx.Tx methods panic via the nil embed.
type fakeTx struct {
	pgx.Tx
	bypass    bool
	roleErr   error
	setErr    error
	rows      *fakeRows
	execs     []string
	committed bool
}

type fakeRow struct {
	bypass bool
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*bool) = r.bypass
	return nil
}

func (tx *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeRow{bypass: tx.bypass, err: tx.roleErr}
}

func (tx *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, sql)
	return pgconn.CommandTag{}, tx.setErr
}

func (tx *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return tx.rows, nil
}

func (tx *fakeTx) Commit(context.Context) error   { tx.committed = true; return nil }
func (tx *fakeTx) Rollback(context.Context) error { return nil }

type fakeBeginner struct {
	tx  *fakeTx
	err error
}

func (b fakeBeginner) Begin(context.Context) (pgx.Tx, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.tx, nil
}

func TestResolveImplicitTenantAnyRole(t *testing.T) {
	t.Parallel()
	a := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	b := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	one := func() *fakeRows { return &fakeRows{data: [][3]any{{a, "acme", "Acme"}}} }

	t.Run("RLS-bound role reads as atlas_service_account", func(t *testing.T) {
		t.Parallel()
		tx := &fakeTx{rows: one()}
		got, err := tenancy.ResolveImplicitTenantAnyRole(context.Background(), fakeBeginner{tx: tx})
		if err != nil || got.ID != a {
			t.Fatalf("got %+v, %v; want tenant %s", got, err, a)
		}
		if len(tx.execs) != 1 || tx.execs[0] != "SET LOCAL ROLE atlas_service_account" {
			t.Fatalf("execs = %v, want the role switch", tx.execs)
		}
		if !tx.committed {
			t.Error("transaction not committed")
		}
	})

	t.Run("BYPASSRLS role reads directly", func(t *testing.T) {
		t.Parallel()
		tx := &fakeTx{bypass: true, rows: one()}
		got, err := tenancy.ResolveImplicitTenantAnyRole(context.Background(), fakeBeginner{tx: tx})
		if err != nil || got.ID != a {
			t.Fatalf("got %+v, %v; want tenant %s", got, err, a)
		}
		if len(tx.execs) != 0 {
			t.Fatalf("execs = %v, want no role switch", tx.execs)
		}
	})

	t.Run("several tenants stay an AmbiguousTenantError", func(t *testing.T) {
		t.Parallel()
		tx := &fakeTx{rows: &fakeRows{data: [][3]any{{a, "acme", "Acme"}, {b, "beta", "Beta"}}}}
		_, err := tenancy.ResolveImplicitTenantAnyRole(context.Background(), fakeBeginner{tx: tx})
		var amb *tenancy.AmbiguousTenantError
		if !errors.As(err, &amb) || len(amb.Tenants) != 2 {
			t.Fatalf("err = %v, want *AmbiguousTenantError with 2 tenants", err)
		}
	})

	t.Run("no tenant stays ErrNoTenants", func(t *testing.T) {
		t.Parallel()
		tx := &fakeTx{rows: &fakeRows{}}
		_, err := tenancy.ResolveImplicitTenantAnyRole(context.Background(), fakeBeginner{tx: tx})
		if !errors.Is(err, tenancy.ErrNoTenants) {
			t.Fatalf("err = %v, want ErrNoTenants", err)
		}
	})

	boom := errors.New("boom")
	for name, db := range map[string]fakeBeginner{
		"begin fails":       {err: boom},
		"role read fails":   {tx: &fakeTx{roleErr: boom}},
		"role switch fails": {tx: &fakeTx{setErr: boom}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := tenancy.ResolveImplicitTenantAnyRole(context.Background(), db); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want boom", err)
			}
		})
	}
}
