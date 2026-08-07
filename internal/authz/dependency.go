// Dependency-outage classification for Engine.Decide (OE-432, slice 356a
// gap G-1/G-2).
//
// Engine.Decide has exactly two DB-backed hops — the RolesResolver and
// the AttrsResolver — and one in-memory hop (the embedded OPA eval).
// When Postgres is down, the resolver hop is the FIRST thing to fail on
// every authenticated request, and before OE-432 the middleware
// reported that as a generic "authorization engine error" 500. The
// chaos run of record (docs/audit-log/356a-pg-primary-outage-chaos-
// decisions.md, D1) measured 120/120 such 500s with zero log lines.
//
// This file draws the dependency-vs-policy line: a resolver error whose
// chain is connectivity-shaped is wrapped with ErrDependencyUnavailable
// so the authzmw middleware can return 503 + retry_after instead of a
// 500 that points operators at OPA. Errors from the OPA eval itself are
// never classified as dependency failures — the engine is embedded and
// has no network dependency.
package authz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDependencyUnavailable marks a Decide failure caused by an
// unreachable backing dependency (the Postgres pool behind the roles /
// attrs resolvers) rather than by the policy engine itself. Callers
// match it with errors.Is; the authzmw middleware maps it to
// 503 {"error":"database_unavailable","retry_after":5} per the slice
// 335 Experiment 3 design.
var ErrDependencyUnavailable = errors.New("authz dependency unavailable")

// wrapIfDependencyUnavailable returns err wrapped with
// ErrDependencyUnavailable when its chain is connectivity-shaped, and
// err unchanged otherwise. Applied to the resolver hops in Decide only.
func wrapIfDependencyUnavailable(err error) error {
	if isDependencyUnavailable(err) {
		return fmt.Errorf("%w: %w", ErrDependencyUnavailable, err)
	}
	return err
}

// isDependencyUnavailable reports whether the error chain indicates the
// backing store is unreachable (as opposed to a query/data/input
// error, which stays a genuine 500). The cases cover the shapes a
// stopped or unreachable Postgres actually produces through pgx v5:
//
//   - *pgconn.ConnectError — a fresh dial failed (connection refused /
//     DNS failure / dial timeout). The dominant shape during the slice
//     356a outage window.
//   - pgconn.ErrConnClosed — an operation on a connection the driver
//     already tore down.
//   - net.Error / connection-level syscall errors / unexpected EOF — a
//     pooled connection died mid-conversation when the server went away.
//   - PgError SQLSTATE class 08 (connection exception), class 57
//     (operator intervention: 57P01 admin_shutdown is what a graceful
//     `pg_ctl stop` sends), and 53300 (too_many_connections) — the
//     server itself declining connections.
//   - context.DeadlineExceeded — pool-acquire or dial timed out while
//     the dependency was unresponsive.
func isDependencyUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	if errors.Is(err, pgconn.ErrConnClosed) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.SQLState()
		if strings.HasPrefix(code, "08") || strings.HasPrefix(code, "57") || code == "53300" {
			return true
		}
	}
	return false
}
