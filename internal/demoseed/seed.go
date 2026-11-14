package demoseed

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ErrMultiTenantNotEnabled — Seed refused to create a demo tenant because
// one would make the install multi-tenant without the operator's opt-in
// (#1765). errors.Is-matchable.
var ErrMultiTenantNotEnabled = errors.New("demoseed: creating a demo tenant needs the multi-tenant opt-in")

// ErrNoImplicitTenant — a slug-less Seed or Teardown could not tell which
// tenant is meant: there is none, or there are several. errors.Is-matchable.
var ErrNoImplicitTenant = errors.New("demoseed: no single tenant to seed in place")

// SeedInput is what `atlas-cli demo seed` passes to Seed.
type SeedInput struct {
	// Slug names a separate demo tenant to create (or re-run against).
	// Empty means the install's only tenant, seeded in place.
	Slug string

	// AllowMultiTenant is the deployment's ATLAS_ALLOW_MULTI_TENANT
	// opt-in. Without it, Seed never creates a tenant beside an
	// existing one.
	AllowMultiTenant bool

	ActorUserID   uuid.UUID
	ActorTenantID uuid.UUID
}

// WithTenantResolver overrides how a slug-less Seed or Teardown picks the
// tenant. Test-only.
func (s *Seeder) WithTenantResolver(fn func(ctx context.Context) (tenancy.TenantRef, error)) *Seeder {
	s.resolveTenant = fn
	return s
}

// Seed is the CLI's entry point (#1765). Seeding never turns an install
// multi-tenant on its own:
//
//   - Without a slug it seeds the install's only tenant in place
//     (ApplyInPlace), which refuses if that tenant holds real data.
//   - With a slug it creates a separate demo tenant (Apply), but only when
//     no other tenant exists or AllowMultiTenant is set. A slug that already
//     names a tenant goes to Apply, which re-runs idempotently or refuses.
func (s *Seeder) Seed(ctx context.Context, in SeedInput) (Result, error) {
	if in.Slug == "" {
		ref, err := s.implicitTenant(ctx, "demo seed")
		if err != nil {
			return Result{}, err
		}
		return s.ApplyInPlace(ctx, ref.ID, in.ActorUserID, in.ActorTenantID)
	}
	if err := validateSlug(in.Slug); err != nil {
		return Result{}, err
	}
	existing, err := s.lookupTenantBySlug(ctx, in.Slug)
	if err != nil {
		return Result{}, fmt.Errorf("demoseed: lookup tenant: %w", err)
	}
	if existing == nil && !in.AllowMultiTenant {
		var others int
		if err := s.authPool.QueryRow(ctx,
			`SELECT count(*) FROM tenants WHERE id <> $1`, uuid.Nil,
		).Scan(&others); err != nil {
			return Result{}, fmt.Errorf("demoseed: count tenants: %w", err)
		}
		if others > 0 {
			return Result{}, fmt.Errorf(
				"refusing to create demo tenant %q: it would make this install multi-tenant. "+
					"Omit --tenant-slug to load the demo into your organisation (allowed while it holds only the stock bootstrap seed), "+
					"or opt in to multi-tenant by setting ATLAS_ALLOW_MULTI_TENANT=true (one-way: an install cannot go back to one tenant): %w",
				in.Slug, ErrMultiTenantNotEnabled,
			)
		}
	}
	return s.Apply(ctx, ApplyInput{
		Slug:          in.Slug,
		ActorUserID:   in.ActorUserID,
		ActorTenantID: in.ActorTenantID,
	})
}

// TeardownImplicit is Teardown for the install's only tenant (`atlas-cli
// demo teardown` with no --tenant-slug). It removes exactly the rows an
// in-place seed recorded.
func (s *Seeder) TeardownImplicit(ctx context.Context, actorUserID uuid.UUID, actorTenantID uuid.UUID) (tenancy.TenantRef, error) {
	ref, err := s.implicitTenant(ctx, "demo teardown")
	if err != nil {
		return tenancy.TenantRef{}, err
	}
	return ref, s.TeardownTenant(ctx, ref.ID, actorUserID, actorTenantID)
}

// implicitTenant resolves the install's only tenant, turning the resolver's
// "none" and "several" answers into operator-facing ErrNoImplicitTenant
// errors.
func (s *Seeder) implicitTenant(ctx context.Context, verb string) (tenancy.TenantRef, error) {
	ref, err := s.resolveTenant(ctx)
	var amb *tenancy.AmbiguousTenantError
	switch {
	case err == nil:
		return ref, nil
	case errors.Is(err, tenancy.ErrNoTenants):
		return tenancy.TenantRef{}, fmt.Errorf(
			"%s: no tenant exists yet; run the install bootstrap first, or pass --tenant-slug for a separate demo tenant: %w",
			verb, ErrNoImplicitTenant)
	case errors.As(err, &amb):
		return tenancy.TenantRef{}, fmt.Errorf(
			"%s: this is a multi-tenant install, so demo data lives in its own tenant; pass --tenant-slug (%v): %w",
			verb, err, ErrNoImplicitTenant)
	default:
		return tenancy.TenantRef{}, fmt.Errorf("%s: resolve tenant: %w", verb, err)
	}
}
