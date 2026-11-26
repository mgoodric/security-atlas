package superadmin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/superadmin"
)

func TestConfirm(t *testing.T) {
	t.Parallel()
	admin := uuid.New()
	set := superadmin.CheckerFunc(func(_ context.Context, id uuid.UUID) (bool, error) {
		return id == admin, nil
	})
	failing := superadmin.CheckerFunc(func(context.Context, uuid.UUID) (bool, error) {
		return true, errors.New("db down")
	})

	cases := []struct {
		name    string
		checker superadmin.Checker
		subject string
		want    bool
	}{
		{"prefixed subject in table", set, "user:" + admin.String(), true},
		{"bare subject in table", set, admin.String(), true},
		{"subject not in table", set, "user:" + uuid.NewString(), false},
		{"non-UUID subject", set, "key_abc123", false},
		{"nil checker fails closed", nil, admin.String(), false},
		{"query error fails closed", failing, admin.String(), false},
		{"pool-less DB checker fails closed", superadmin.NewDBChecker(nil), admin.String(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := superadmin.Confirm(context.Background(), tc.checker, tc.subject); got != tc.want {
				t.Errorf("Confirm = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDBChecker_NoPool(t *testing.T) {
	t.Parallel()
	_, err := superadmin.NewDBChecker(nil).IsSuperAdmin(context.Background(), uuid.New())
	if !errors.Is(err, superadmin.ErrNoPool) {
		t.Errorf("err = %v, want ErrNoPool", err)
	}
}
