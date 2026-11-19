// Pure-Go unit tests for notifyErrorDetail (#2514): the notify_error value
// on the tenant_last_admin_deprovisioned audit row, which tenant admins can
// read, carries a SQLSTATE or a fixed text and never the raw error.
package scim

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestNotifyErrorDetail(t *testing.T) {
	t.Parallel()
	pgErr := &pgconn.PgError{Code: "22012", Message: "division by zero", Detail: "secret detail"}
	cases := map[string]struct {
		err  error
		want string
	}{
		"postgres error":         {pgErr, "SQLSTATE 22012"},
		"wrapped postgres error": {fmt.Errorf("scim: notify super-admins: %w", pgErr), "SQLSTATE 22012"},
		"network error": {
			errors.New("failed to connect to `host=10.0.0.5 user=atlas_app database=atlas`: dial tcp 10.0.0.5:5432: connect: connection refused"),
			notifyErrorOther,
		},
		"long error": {errors.New(strings.Repeat("x", 10_000)), notifyErrorOther},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := notifyErrorDetail(tc.err)
			if got != tc.want {
				t.Fatalf("notifyErrorDetail = %q, want %q", got, tc.want)
			}
			if len(got) > 200 {
				t.Fatalf("notifyErrorDetail is %d chars, want <= 200", len(got))
			}
		})
	}
}
