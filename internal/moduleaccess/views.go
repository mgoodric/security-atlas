package moduleaccess

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/identity"
)

// Views answers, for the combined views (#2173: the dashboard's Upcoming
// list, the Calendar and its ICS feed, Activity, global search, the
// dashboard export, Metrics and Approvals), which modules' items a caller
// may see. An item is shown when the module that owns it is switched on
// for the tenant AND inside the caller's module access: the same two
// answers featureflag.ModuleGate and Gate give the module's own routes, so
// a combined view never lists what the module's page would refuse.
//
// A view names each item kind by a route the module registry places
// (featureflag.Resolve), as GET /v1/approvals names each type by its
// approve route, so no view keeps a second copy of which module owns what.
//
// Views filter only what is displayed. They never touch the evidence
// ledger, control evaluation, audit-binding output or saved snapshots
// (ADR 0025 decision 2).
type Views struct {
	access *Store
	flags  featureflag.Looker
}

// NewViews builds a Views over the module access store and the module
// switch store.
func NewViews(access *Store, flags featureflag.Looker) *Views {
	return &Views{access: access, flags: flags}
}

// Viewer is one caller's answer: Sees reports whether an item owned by a
// route's module may be shown to them.
type Viewer struct {
	r      *http.Request
	flags  featureflag.Looker
	access Access
	all    bool
}

// For resolves the caller of r. A nil Views (a unit-test server with no
// database) sees every module.
func (v *Views) For(r *http.Request) (Viewer, error) {
	if v == nil || v.access == nil || v.flags == nil {
		return Viewer{all: true}, nil
	}
	a, err := v.access.ForCaller(r)
	if err != nil {
		return Viewer{}, err
	}
	return Viewer{r: r, flags: v.flags, access: a}, nil
}

// ForUser resolves a person who is not the caller of r: the subscriber of
// an ICS feed, whose token is a calendar-only credential that carries no
// roles. It reads their module access and live roles when the feed is
// fetched, so a narrowing takes effect without re-subscribing. r must
// carry userID's tenant (tenancy.WithTenant).
//
// It fails closed: an empty userID (a feed minted before #2173 recorded
// its subscriber), or a person with no live role in the tenant (removed or
// deactivated), sees core modules only.
func (v *Views) ForUser(r *http.Request, tenantID uuid.UUID, userID string) (Viewer, error) {
	if v == nil || v.access == nil || v.flags == nil {
		return Viewer{all: true}, nil
	}
	none := Viewer{r: r, flags: v.flags, access: Access{Modules: []string{}}}
	if userID == "" {
		return none, nil
	}
	roles, err := v.access.roles.RolesFor(r.Context(), tenantID.String(), userID)
	if err != nil {
		return Viewer{}, fmt.Errorf("moduleaccess: subscriber roles: %w", err)
	}
	if len(roles) == 0 {
		return none, nil
	}
	for _, role := range roles {
		if role == authz.RoleAdmin || role == authz.RoleAuditor {
			return Viewer{r: r, flags: v.flags, access: Access{Exempt: true}}, nil
		}
	}
	modules, err := v.access.Get(r.Context(), tenantID, identity.RolesKey(userID))
	if err != nil {
		return Viewer{}, err
	}
	return Viewer{r: r, flags: v.flags, access: Access{Modules: modules}}, nil
}

// Sees reports whether an item owned by the module (and feature) that the
// registry places path under may be shown: the module and feature are
// switched on, and an optional module is inside the viewer's access. A
// path the registry cannot place is shown, as ModuleGate passes it on.
func (v Viewer) Sees(path string) (bool, error) {
	if v.all {
		return true, nil
	}
	o, err := featureflag.Resolve(path)
	if err != nil {
		return true, nil
	}
	if !o.Module.Core && !v.access.Has(o.Module.Key) {
		return false, nil
	}
	return featureflag.RouteOn(v.r, v.flags, path)
}

// Modules resolves the viewer's answer for every optional module at once,
// for a view that filters many rows inside a transaction: ModuleSet.Sees
// reads no flag and no access row, so it never asks the pool for a second
// connection while the transaction holds one. It answers what Sees
// answers: a module switched off or outside the viewer's access hides its
// routes, and a feature switched off hides the routes it owns, in a core
// module too (#2477).
func (v Viewer) Modules() (ModuleSet, error) {
	if v.all {
		return ModuleSet{}, nil
	}
	hidden := map[string]bool{}
	features := map[string]bool{}
	for _, m := range featureflag.Modules {
		if !m.Core {
			if !v.access.Has(m.Key) {
				hidden[m.Key] = true
				continue
			}
			on, err := featureflag.Enabled(v.r.Context(), v.flags, m.FlagKey)
			if err != nil {
				return ModuleSet{}, err
			}
			if !on {
				hidden[m.Key] = true
				continue
			}
		}
		for _, f := range m.Features {
			// A feature with no routes owns nothing Resolve can place.
			if len(f.Routes) == 0 {
				continue
			}
			on, err := featureflag.Enabled(v.r.Context(), v.flags, f.FlagKey)
			if err != nil {
				return ModuleSet{}, err
			}
			if !on {
				features[f.Key] = true
			}
		}
	}
	return ModuleSet{hidden: hidden, features: features}, nil
}

// ModuleSet is a Viewer's answer resolved for every optional module and
// every feature that owns routes.
type ModuleSet struct {
	hidden   map[string]bool
	features map[string]bool
}

// Sees reports whether an item owned by the module (and feature) the
// registry places path under may be shown. Paths the registry cannot
// place are shown; a core module's paths are shown unless a feature
// switched off owns them.
func (m ModuleSet) Sees(path string) bool {
	o, err := featureflag.Resolve(path)
	if err != nil {
		return true
	}
	if !o.Module.Core && m.hidden[o.Module.Key] {
		return false
	}
	return o.Feature == nil || !m.features[o.Feature.Key]
}

// Hidden returns the keys of routes whose items the viewer may not see.
// routes maps a view's item kind to a route its module owns.
func (v Viewer) Hidden(routes map[string]string) (map[string]bool, error) {
	out := map[string]bool{}
	for kind, path := range routes {
		ok, err := v.Sees(path)
		if err != nil {
			return nil, err
		}
		if !ok {
			out[kind] = true
		}
	}
	return out, nil
}
