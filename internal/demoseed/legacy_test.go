package demoseed

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// #2153 review N2: a serialization failure surfaces as ErrConcurrentWrite,
// which tells the operator nothing was deleted; other errors pass through.
func TestConcurrentWriteErr(t *testing.T) {
	t.Parallel()
	serial := fmt.Errorf("commit: %w", &pgconn.PgError{Code: "40001"})
	if got := concurrentWriteErr(serial); !errors.Is(got, ErrConcurrentWrite) {
		t.Errorf("40001 maps to %v, want ErrConcurrentWrite", got)
	}
	other := fmt.Errorf("exec: %w", &pgconn.PgError{Code: "23503"})
	if got := concurrentWriteErr(other); got != other {
		t.Errorf("23503 maps to %v, want it unchanged", got)
	}
	plain := errors.New("boom")
	if got := concurrentWriteErr(plain); got != plain {
		t.Errorf("plain error maps to %v, want it unchanged", got)
	}
}
