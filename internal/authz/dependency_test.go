// OE-432 (slice 356a G-1): unit coverage for the dependency-vs-policy
// error classification on Engine.Decide's DB-backed resolver hops.
//
// Exercised through the public surface: a resolver that fails with a
// connectivity-shaped error must surface as ErrDependencyUnavailable
// from Decide; a resolver that fails with a data/input-shaped error
// must NOT. The classification itself is internal (dependency.go); the
// contract callers rely on is errors.Is(err, ErrDependencyUnavailable).
package authz_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mgoodric/security-atlas/internal/authz"
)

// errResolver fails every RolesFor call with the configured error.
type errResolver struct{ err error }

func (r errResolver) RolesFor(_ context.Context, _, _ string) ([]authz.Role, error) {
	return nil, r.err
}

// errAttrsResolver fails every AttrsFor call with the configured error.
type errAttrsResolver struct{ err error }

func (r errAttrsResolver) AttrsFor(_ context.Context, _, _ string, _ []authz.Role) (map[string]interface{}, error) {
	return nil, r.err
}

func decideInput() authz.Input {
	return authz.Input{
		User: authz.UserInput{
			ID:    "user-432",
			Roles: []authz.Role{authz.RoleAdmin},
		},
		TenantID: "9b0c8b1e-0000-4000-8000-000000000001",
		Action:   "read",
		Resource: authz.ResourceInput{Type: "anchors"},
		Request:  authz.RequestInput{Method: "GET", Path: "/v1/anchors"},
	}
}

func TestDecide_ResolverErrorClassification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		err            error
		wantDependency bool
	}{
		{
			// The dominant shape during the slice 356a outage: every
			// fresh dial refused while Postgres was stopped.
			name:           "dial refused (net.OpError ECONNREFUSED)",
			err:            &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED},
			wantDependency: true,
		},
		{
			name:           "pooled conn reset mid-query (ECONNRESET)",
			err:            fmt.Errorf("query user_roles: %w", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}),
			wantDependency: true,
		},
		{
			name:           "driver already tore the conn down (pgconn.ErrConnClosed)",
			err:            fmt.Errorf("begin user_roles tx: %w", pgconn.ErrConnClosed),
			wantDependency: true,
		},
		{
			name:           "server dropped mid-protocol (unexpected EOF)",
			err:            fmt.Errorf("query user_roles: %w", io.ErrUnexpectedEOF),
			wantDependency: true,
		},
		{
			// Graceful `pg_ctl stop` sends 57P01 admin_shutdown before
			// closing; class 57 is operator intervention.
			name:           "SQLSTATE 57P01 admin_shutdown",
			err:            &pgconn.PgError{Severity: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"},
			wantDependency: true,
		},
		{
			name:           "SQLSTATE 08006 connection_failure",
			err:            &pgconn.PgError{Severity: "FATAL", Code: "08006", Message: "connection failure"},
			wantDependency: true,
		},
		{
			name:           "SQLSTATE 53300 too_many_connections",
			err:            &pgconn.PgError{Severity: "FATAL", Code: "53300", Message: "sorry, too many clients already"},
			wantDependency: true,
		},
		{
			name:           "pool-acquire deadline exceeded",
			err:            fmt.Errorf("begin user_roles tx: %w", context.DeadlineExceeded),
			wantDependency: true,
		},
		{
			// Data/query-shaped failures stay genuine 500s: a reachable
			// database rejecting a bad query is NOT an outage.
			name:           "SQLSTATE 42P01 undefined_table is not an outage",
			err:            &pgconn.PgError{Severity: "ERROR", Code: "42P01", Message: "relation does not exist"},
			wantDependency: false,
		},
		{
			name:           "plain resolver error is not an outage",
			err:            errors.New("user_roles row scan mismatch"),
			wantDependency: false,
		},
	}

	for _, tc := range cases {
		t.Run("roles resolver: "+tc.name, func(t *testing.T) {
			t.Parallel()
			e, err := authz.NewEngine(context.Background(), errResolver{err: tc.err})
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			_, decideErr := e.Decide(context.Background(), decideInput())
			if decideErr == nil {
				t.Fatalf("Decide returned nil error; resolver failed with %v", tc.err)
			}
			if got := errors.Is(decideErr, authz.ErrDependencyUnavailable); got != tc.wantDependency {
				t.Fatalf("errors.Is(err, ErrDependencyUnavailable) = %v, want %v (err=%v)", got, tc.wantDependency, decideErr)
			}
			// The original cause must stay in the chain for the
			// server-side log line — classification adds a marker, it
			// never swallows the root cause.
			if !errors.Is(decideErr, tc.err) && !errorsAsMatches(decideErr, tc.err) {
				t.Fatalf("Decide error chain lost the underlying cause: %v", decideErr)
			}
		})
	}
}

// TestDecide_AttrsResolverOutageClassified covers the second DB-backed
// hop: the slice-025 ABAC attrs resolver, consulted for auditor-role
// inputs, gets the same dependency classification as the roles hop.
func TestDecide_AttrsResolverOutageClassified(t *testing.T) {
	t.Parallel()
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	e, err := authz.NewEngine(context.Background(), authz.NoopRolesResolver{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	e.WithAttrsResolver(errAttrsResolver{err: dial})

	in := decideInput()
	in.User.Roles = []authz.Role{authz.RoleAuditor} // attrs resolver only runs for auditors

	_, decideErr := e.Decide(context.Background(), in)
	if decideErr == nil {
		t.Fatalf("Decide returned nil error; attrs resolver failed with %v", dial)
	}
	if !errors.Is(decideErr, authz.ErrDependencyUnavailable) {
		t.Fatalf("attrs-resolver dial failure not classified as ErrDependencyUnavailable: %v", decideErr)
	}
}

// errorsAsMatches reports whether want appears in got's chain by
// pointer-typed As matching — needed for *pgconn.PgError cases where
// errors.Is compares by identity but the table stores the same pointer,
// so Is normally suffices; this is belt-and-suspenders for wrapped
// pointer errors.
func errorsAsMatches(got, want error) bool {
	var pgErr *pgconn.PgError
	if errors.As(want, &pgErr) {
		var found *pgconn.PgError
		return errors.As(got, &found) && found.Code == pgErr.Code
	}
	return false
}
