package moduleaccess

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// #2175: work that runs with no request behind it (the reminder and
// notification jobs, the notification digest) asks the same two
// questions the module's routes ask: is the module switched on for the
// tenant, and can the recipient work in it. There is no credential, so
// the switch is read for the tenant the job names and the recipient by
// UserHas, from their live roles and stored access.
//
// A job that cannot read a recipient's access sends nothing to them: the
// caller treats the error as a skip, never as "send to everyone".

// SwitchedOn reports whether key is switched on for tenantID. key is a
// module key or a feature key: a feature is on when its module and its
// own switch are both on. A core module, or a key the registry does not
// list, answers true. A nil Views answers true.
//
// The switch read follows featureflag.Enabled: a flag-store error falls
// back to the switch's seed default, as it does for the module's routes.
// Background senders use SwitchedOnStrict instead.
func (v *Views) SwitchedOn(ctx context.Context, tenantID uuid.UUID, key string) (bool, error) {
	return v.switchedOn(ctx, tenantID, key, featureflag.Enabled)
}

// SwitchedOnStrict is SwitchedOn for work with no request behind it (#2548):
// a flag-store error comes back as an error (errors.Is
// featureflag.ErrStoreUnavailable) instead of the seed default. The caller
// skips the tenant for this sweep and logs; the next sweep retries.
func (v *Views) SwitchedOnStrict(ctx context.Context, tenantID uuid.UUID, key string) (bool, error) {
	return v.switchedOn(ctx, tenantID, key, featureflag.EnabledStrict)
}

func (v *Views) switchedOn(ctx context.Context, tenantID uuid.UUID, key string,
	enabled func(context.Context, featureflag.Looker, string) (bool, error),
) (bool, error) {
	if v == nil || v.flags == nil {
		return true, nil
	}
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return false, fmt.Errorf("moduleaccess: switch tenant: %w", err)
	}
	m, f, ok := lookup(key)
	if !ok {
		return true, nil
	}
	keys := make([]string, 0, 2)
	if !m.Core {
		keys = append(keys, m.FlagKey)
	}
	if f != nil {
		keys = append(keys, f.FlagKey)
	}
	for _, k := range keys {
		on, err := enabled(tctx, v.flags, k)
		if err != nil {
			return false, err
		}
		if !on {
			return false, nil
		}
	}
	return true, nil
}

// Reaches reports whether a background job may notify userID about work
// in key (a module or feature key): key is switched on for the tenant and
// userID can work in its module (UserHas).
//
// Only background senders ask it, so the switch is read strictly
// (SwitchedOnStrict): a flag-store error is an error, which the sender
// treats as "skip this tenant", never as the seed default (#2548).
func (v *Views) Reaches(ctx context.Context, tenantID uuid.UUID, userID, key string) (bool, error) {
	on, err := v.SwitchedOnStrict(ctx, tenantID, key)
	if err != nil || !on {
		return false, err
	}
	m, _, ok := lookup(key)
	if !ok {
		return true, nil
	}
	tctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return false, fmt.Errorf("moduleaccess: recipient tenant: %w", err)
	}
	return v.UserHas(tctx, tenantID, userID, m.Key)
}

// lookup finds key in the registry as a module key or a feature key,
// returning the module and, for a feature, the feature.
func lookup(key string) (featureflag.Module, *featureflag.Feature, bool) {
	for _, m := range featureflag.Modules {
		if m.Key == key {
			return m, nil, true
		}
		for i := range m.Features {
			if m.Features[i].Key == key {
				return m, &m.Features[i], true
			}
		}
	}
	return featureflag.Module{}, nil, false
}
