package crosswalkremap

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The database's errors map to the store's typed errors, which the HTTP
// layer turns into statuses.
func TestMapDBErr(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		in   error
		want error
	}{
		{"not found", &pgconn.PgError{Code: "P0002", Message: "edge-remap: not found"}, ErrNotFound},
		{"decided", &pgconn.PgError{Code: "23514", Message: "edge-remap: decided (status approved)"}, ErrDecided},
		{"anchor required", &pgconn.PgError{Code: "23514", Message: "edge-remap: anchor required (class split)"}, ErrAnchorRequired},
		{"anchor not in release", &pgconn.PgError{Code: "23514", Message: "edge-remap: anchor not in release (X in B)"}, ErrAnchorNotInRelease},
		{"edge changed", &pgconn.PgError{Code: "23514", Message: "edge-remap: edge changed"}, ErrEdgeChanged},
		{"edge exists", &pgconn.PgError{Code: "23505", Message: "edge-remap: edge exists (r already maps to X)"}, ErrEdgeExists},
		// Two proposals approved onto the same pair at once: the loser's
		// insert hits fw_to_scf_edges' unique constraint directly.
		{"unique race", &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "fw_to_scf_edges_framework_requirement_id_scf_anchor_id_key"`}, ErrEdgeExists},
		{"decider", &pgconn.PgError{Code: "23514", Message: "edge-remap: decider required"}, ErrDeciderRequired},
		{"tenant context", &pgconn.PgError{Code: "42501", Message: "edge-remap: refused under a tenant context"}, ErrTenantContext},
	} {
		if got := mapDBErr(c.in); !errors.Is(got, c.want) {
			t.Errorf("%s: mapDBErr = %v; want %v", c.name, got, c.want)
		}
	}
	other := mapDBErr(&pgconn.PgError{Code: "23503", Message: "fk"})
	for _, typed := range []error{ErrNotFound, ErrDecided, ErrEdgeExists, ErrEdgeChanged, ErrTenantContext} {
		if errors.Is(other, typed) {
			t.Errorf("an unrelated database error mapped to %v", typed)
		}
	}
}

func TestLockErr(t *testing.T) {
	t.Parallel()
	if got := lockErr(&pgconn.PgError{Code: "55P03", Message: "could not obtain lock"}); !errors.Is(got, ErrBulkRunning) {
		t.Errorf("55P03 = %v; want ErrBulkRunning", got)
	}
	if got := lockErr(&pgconn.PgError{Code: "42501", Message: "edge-remap: refused under a tenant context"}); !errors.Is(got, ErrTenantContext) {
		t.Errorf("tenant guard = %v; want ErrTenantContext", got)
	}
	if got := lockErr(errors.New("boom")); errors.Is(got, ErrBulkRunning) || errors.Is(got, ErrTenantContext) {
		t.Errorf("other error mapped to a typed error: %v", got)
	}
}

func TestCheckInput(t *testing.T) {
	t.Parallel()
	if err := checkInput(" ", ""); !errors.Is(err, ErrDeciderRequired) {
		t.Errorf("blank decider = %v", err)
	}
	long := make([]rune, maxReason+1)
	for i := range long {
		long[i] = 'x'
	}
	if err := checkInput("u", string(long)); !errors.Is(err, ErrReasonTooLong) {
		t.Errorf("long reason = %v", err)
	}
	if err := checkInput("u", "fine"); err != nil {
		t.Errorf("valid input = %v", err)
	}
}
