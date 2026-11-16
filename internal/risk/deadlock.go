package risk

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// maxDeadlockAttempts bounds retryOnDeadlock: the first attempt plus two
// retries.
const maxDeadlockAttempts = 3

// isDeadlock reports whether err is, or wraps, a Postgres deadlock (40P01).
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// retryOnDeadlock runs attempt, and again when Postgres aborted it as a
// deadlock victim, up to maxDeadlockAttempts times (#2004).
//
// attempt must open, run and commit its own transaction, with every write
// inside it. A deadlock aborts that transaction whole, so nothing it did
// survives and the next attempt starts from a fresh one. The winning side
// has its locks by then, so the retry queues behind it instead of
// deadlocking again.
//
// The window it covers: a risk write's BeforeRiskWrite hook takes the
// hierarchy lock only when a rule matches its themes. A rule activated
// between that hook and the write's own evaluation, or a theme change that
// checkRuleLocks lets through on its last attempt, leaves the write taking
// risk_hierarchy:<tenant> after its row lock, the reverse of a concurrent
// link's order. See decisions D9 and D11 in
// docs/audit-log/1748-risk-hierarchy-links-decisions.md.
func retryOnDeadlock(ctx context.Context, attempt func() error) error {
	var err error
	for i := 0; i < maxDeadlockAttempts; i++ {
		err = attempt()
		if !isDeadlock(err) || ctx.Err() != nil {
			return err
		}
	}
	return err
}
