package tenancy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestTenantFromContext_Missing(t *testing.T) {
	t.Parallel()

	_, err := tenancy.TenantFromContext(context.Background())
	if !errors.Is(err, tenancy.ErrNoTenant) {
		t.Fatalf("expected ErrNoTenant, got %v", err)
	}
}

func TestWithTenant_RoundTrip(t *testing.T) {
	t.Parallel()

	const id = "11111111-1111-1111-1111-111111111111"
	ctx, err := tenancy.WithTenant(context.Background(), id)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	got, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		t.Fatalf("TenantFromContext: %v", err)
	}
	if got != id {
		t.Fatalf("tenant mismatch: got %q, want %q", got, id)
	}
}

func TestWithTenant_RejectsInvalidUUID(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"", "not-a-uuid", "1234", "tenant-a"} {
		bad := bad
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			if _, err := tenancy.WithTenant(context.Background(), bad); err == nil {
				t.Fatalf("expected error for input %q, got nil", bad)
			}
		})
	}
}
