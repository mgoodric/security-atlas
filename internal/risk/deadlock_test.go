package risk

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func deadlockErr() error {
	return fmt.Errorf("link child: %w", &pgconn.PgError{Code: "40P01"})
}

func TestRetryOnDeadlock(t *testing.T) {
	t.Parallel()
	other := errors.New("boom")
	cases := []struct {
		name      string
		results   []error
		wantCalls int
		wantErr   func(error) bool
	}{
		{"success first try", []error{nil}, 1, func(err error) bool { return err == nil }},
		{"deadlock then success", []error{deadlockErr(), nil}, 2, func(err error) bool { return err == nil }},
		{"other error not retried", []error{other}, 1, func(err error) bool { return errors.Is(err, other) }},
		{"bounded", []error{deadlockErr(), deadlockErr(), deadlockErr(), nil}, maxDeadlockAttempts, isDeadlock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			err := retryOnDeadlock(context.Background(), func() error {
				err := tc.results[calls]
				calls++
				return err
			})
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if !tc.wantErr(err) {
				t.Fatalf("unexpected err %v", err)
			}
		})
	}
}

func TestRetryOnDeadlock_StopsWhenContextDone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := retryOnDeadlock(ctx, func() error {
		calls++
		return deadlockErr()
	})
	if calls != 1 || !isDeadlock(err) {
		t.Fatalf("calls = %d, err = %v; want one call returning the deadlock", calls, err)
	}
}
