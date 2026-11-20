package grouprole

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsIDPConfigFKViolation pins #2731: only a 23503 on the IdP config FK
// maps to ErrUnknownIDPConfig; any other FK failure on the insert is not
// blamed on the config.
func TestIsIDPConfigFKViolation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"idp config fk", &pgconn.PgError{Code: "23503", ConstraintName: idpConfigFKConstraint}, true},
		{"wrapped idp config fk", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23503", ConstraintName: idpConfigFKConstraint}), true},
		{"other fk", &pgconn.PgError{Code: "23503", ConstraintName: "oidc_idp_group_mappings_created_by_fkey"}, false},
		{"fk without constraint name", &pgconn.PgError{Code: "23503"}, false},
		{"unique violation on the same name", &pgconn.PgError{Code: "23505", ConstraintName: idpConfigFKConstraint}, false},
		{"non-pg error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isIDPConfigFKViolation(tc.err); got != tc.want {
				t.Fatalf("isIDPConfigFKViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
