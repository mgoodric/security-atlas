package featureflag

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Module is one module in the module registry (#2166). Every server route
// belongs to exactly one module.
//
// A core module is always on: it has no switch. An optional module is
// switched on or off per tenant by its module switch, an ordinary feature
// flag in Seed: the registry adds no second toggle store, only which
// routes, pages and features belong to the flag.
type Module struct {
	// Key names the module on the wire (GET /v1/me/modules, GET
	// /v1/admin/features).
	Key string
	// Name is the module's display name.
	Name string
	// Core marks a module that is always on. A core module has no
	// FlagKey.
	Core bool
	// FlagKey is an optional module's switch: a Seed key whose default is
	// the module's default.
	FlagKey string
	// Routes are the API route patterns the module owns. See Feature.Routes
	// for the pattern form.
	Routes []string
	// Pages are the web app paths the module owns, matched as a
	// segment-bounded prefix. The web app hides a nav entry and gates a
	// page from these, so it keeps no gating map of its own.
	Pages []string
	// Features are the switchable features inside the module. A feature is
	// on only when its module is on AND its own switch is on; its switch
	// keeps its stored value while the module is off.
	Features []Feature
}

// Feature is one switchable feature inside a module.
type Feature struct {
	// Key names the feature on the wire.
	Key string
	// Name is the feature's display name.
	Name string
	// FlagKey is the feature's switch, a Seed key.
	FlagKey string
	// Routes are the API route patterns the feature gates. A pattern owns
	// itself and every path below it, matched segment by segment: "*"
	// matches any one segment, and the last segment also owns its
	// "{id}:verb" form. So "/v1/questionnaires" owns
	// "/v1/questionnaires/{id}/answers", "/v1/controls/*/risks" owns
	// "/v1/controls/{id}/risks", and "/v1/questionnaires" does not own
	// "/v1/questionnaires-x".
	//
	// A feature with no Routes is gated through its module only: its own
	// switch is listed and stored but does not gate a route yet.
	Routes []string
	// Pages are the web app paths the feature owns.
	Pages []string
}

// Modules is the module registry: every module, core and optional. The
// order of the optional modules is the order GET /v1/me/modules reports
// them in.
//
// A nested route belongs to the module whose data it returns: action plans
// under a control path belong to Risk. The most specific pattern wins, so
// "/v1/controls/*/action-plans" (Risk) beats "/v1/controls" (Controls).
var Modules = []Module{
	// --- optional modules ---
	{
		Key:     "risk",
		Name:    "Risk",
		FlagKey: "risk.enabled",
		Routes: []string{
			"/v1/risks",
			"/v1/action-plans",
			"/v1/controls/*/action-plans",
			"/v1/controls/*/risks",
			"/v1/aggregation-rules",
			"/v1/org_units",
			"/v1/themes",
			"/v1/decisions",
		},
		Pages: []string{"/risks", "/action-plans"},
		Features: []Feature{
			{Key: "risk_themes", Name: "Risk themes", FlagKey: "risk.themes"},
			{Key: "risk_hierarchy", Name: "Risk hierarchy", FlagKey: "risk.hierarchy"},
			{Key: "decision_log", Name: "Decision log", FlagKey: "decisions.log"},
		},
	},
	{
		Key:     "third_party_risk",
		Name:    "Third-party risk",
		FlagKey: "vendor.enabled",
		Routes:  []string{"/v1/vendors", "/v1/admin/vendors"},
		Pages:   []string{"/vendors"},
		Features: []Feature{
			{
				Key:     "vendor_claims",
				Name:    "Vendor claims",
				FlagKey: "vendor.claims",
				Routes:  []string{"/v1/oscal/component-definitions", "/v1/oscal/component-claims"},
				Pages:   []string{"/oscal/component-definitions"},
			},
		},
	},
	{
		Key:     "policies",
		Name:    "Policies",
		FlagKey: "policy.enabled",
		Routes: []string{
			"/v1/policies",
			"/v1/controls/*/policies",
			"/v1/me/acknowledgments",
			"/v1/admin/policies",
		},
		Pages: []string{"/policies"},
		Features: []Feature{
			{Key: "policy_acknowledgments", Name: "Policy acknowledgments", FlagKey: "policy.acknowledgments"},
		},
	},
	{
		Key:     "questionnaires",
		Name:    "Questionnaires",
		FlagKey: "questionnaires.enabled",
		Routes:  []string{"/v1/questionnaires"},
		Pages:   []string{"/questionnaires"},
	},
	{
		Key:     "personnel_security",
		Name:    "Personnel security",
		FlagKey: "personnel.enabled",
		Routes:  []string{"/v1/personnel-security"},
		Pages:   []string{"/personnel-security"},
	},
	{
		Key:     "exceptions",
		Name:    "Exceptions",
		FlagKey: "exceptions.enabled",
		Routes:  []string{"/v1/exceptions", "/v1/admin/exceptions"},
		Pages:   []string{"/exceptions"},
	},
	{
		Key:     "board_reporting",
		Name:    "Board reporting",
		FlagKey: "board.reporting",
		Routes:  []string{"/v1/board-briefs", "/v1/board-packs", "/v1/board"},
		Pages:   []string{"/board-packs"},
	},

	// --- core modules ---
	{
		Key:  "controls",
		Name: "Controls",
		Core: true,
		Routes: []string{
			"/v1/controls",
		},
		Features: []Feature{
			{Key: "control_bundles", Name: "Control bundles", FlagKey: "controls.bundles"},
		},
	},
	{
		Key:  "evidence",
		Name: "Evidence",
		Core: true,
		Routes: []string{
			"/v1/evidence",
			"/v1/evidence-summary",
			"/v1/artifacts",
			"/v1/schemas",
			"/v1/admin/evidence",
		},
	},
	{
		Key:  "frameworks",
		Name: "Frameworks and the SCF catalog",
		Core: true,
		Routes: []string{
			"/v1/frameworks",
			"/v1/anchors",
			"/v1/requirements",
			"/v1/csf",
			"/v1/mapping-overlays",
			"/v1/oscal/imported-profiles",
			"/v1/admin/crosswalk-edges",
			"/v1/admin/framework-versions",
		},
	},
	{
		Key:  "scopes",
		Name: "Scopes and framework scopes",
		Core: true,
		Routes: []string{
			"/v1/scopes",
			"/v1/framework-scopes",
			"/v1/controls/*/effective-scope",
			"/v1/controls/*/applicability",
		},
	},
	{
		Key:  "audit_periods",
		Name: "Audit periods",
		Core: true,
		Routes: []string{
			"/v1/audit-periods",
			"/v1/audit-notes",
			"/v1/populations",
			"/v1/samples",
			"/v1/walkthroughs",
			"/v1/me/audit-period",
			"/v1/me/audit-periods",
			"/v1/admin/audit-periods",
			"/v1/admin/samples",
		},
		Features: []Feature{
			{Key: "audit_workflow", Name: "Audit sampling", FlagKey: "audit.workflow"},
			{
				Key:     "oscal_export",
				Name:    "OSCAL SSP and POA&M export",
				FlagKey: "audit.oscal_export",
				Routes:  []string{"/v1/audit-periods/*/oscal-export"},
			},
		},
	},
	{
		Key:  "dashboard",
		Name: "Dashboard",
		Core: true,
		Routes: []string{
			"/v1/dashboard",
			"/v1/activity",
			"/v1/calendar",
			"/v1/calendar.ics",
			"/v1/search",
			"/v1/metrics",
			"/v1/saved-views",
		},
	},
	{
		Key:  "incidents",
		Name: "Incidents",
		Core: true,
		Routes: []string{
			"/v1/incidents",
		},
	},
	{
		Key:  "settings_admin",
		Name: "Settings & admin",
		Core: true,
		Routes: []string{
			"/v1/admin",
			"/v1/me",
			"/v1/people",
			"/v1/tenants",
			"/v1/mcp",
			"/v1/install",
			"/v1/install-state",
			"/v1/version",
			"/v1/test",
			"/auth",
			"/oauth",
			"/scim",
			"/.well-known",
			"/health",
			"/ready",
			"/metrics",
		},
	},
}

// OptionalModules returns the optional modules, in registry order.
func OptionalModules() []Module {
	out := make([]Module, 0, len(Modules))
	for _, m := range Modules {
		if !m.Core {
			out = append(out, m)
		}
	}
	return out
}

// Owner is what owns a route: a module, and the feature inside it when a
// feature's pattern is the most specific match.
type Owner struct {
	Module  Module
	Feature *Feature
}

// ErrNoModule and ErrAmbiguousModule are Resolve's two failures. Both are
// registry bugs, and the every-route guard test fails on either.
var (
	ErrNoModule        = errors.New("featureflag: route belongs to no module")
	ErrAmbiguousModule = errors.New("featureflag: route matches more than one module equally")
)

// Resolve returns the module (and feature, if any) that owns an API path.
// The most specific matching pattern wins: the one with the most segments,
// then the most literal segments. A path no pattern owns returns
// ErrNoModule; two owners tied for most specific return
// ErrAmbiguousModule.
func Resolve(path string) (Owner, error) {
	segs := strings.Split(path, "/")
	var (
		best      Owner
		bestScore = -1
		tied      bool
	)
	consider := func(o Owner, pattern string) {
		score, ok := matchPattern(pattern, segs)
		switch {
		case !ok:
		case score > bestScore:
			best, bestScore, tied = o, score, false
		case score == bestScore && ownerID(o) != ownerID(best):
			tied = true
		}
	}
	for _, m := range Modules {
		for _, p := range m.Routes {
			consider(Owner{Module: m}, p)
		}
		for j := range m.Features {
			for _, p := range m.Features[j].Routes {
				consider(Owner{Module: m, Feature: &m.Features[j]}, p)
			}
		}
	}
	switch {
	case bestScore < 0:
		return Owner{}, ErrNoModule
	case tied:
		return Owner{}, ErrAmbiguousModule
	}
	return best, nil
}

func ownerID(o Owner) string {
	if o.Feature != nil {
		return o.Module.Key + "/" + o.Feature.Key
	}
	return o.Module.Key
}

// matchPattern reports whether pattern owns the path split into segs, and
// how specific the match is (segment count, then literal count).
func matchPattern(pattern string, segs []string) (int, bool) {
	ps := strings.Split(pattern, "/")
	if len(segs) < len(ps) {
		return 0, false
	}
	literals := 0
	for i, p := range ps {
		s := segs[i]
		if p == "*" {
			if s == "" {
				return 0, false
			}
			continue
		}
		last := i == len(ps)-1
		if s != p && (!last || !strings.HasPrefix(s, p+":")) {
			return 0, false
		}
		literals++
	}
	return len(ps)*1000 + literals, true
}

// ModuleForFlag returns the module a flag key switches, and the feature
// when the key is a feature's switch. ok is false for a key no module
// lists.
func ModuleForFlag(key string) (m Module, f *Feature, ok bool) {
	for i := range Modules {
		m := Modules[i]
		if m.FlagKey == key {
			return m, nil, true
		}
		for j := range m.Features {
			if m.Features[j].FlagKey == key {
				f := m.Features[j]
				return m, &f, true
			}
		}
	}
	return Module{}, nil, false
}

// ModuleGate is the module gate step of the authorization chain. It
// resolves the request path's owner from the registry and answers 404
// with the switched-off marker (X-Feature-Disabled: <flag key>, body
// {"error":"feature disabled"}) without reaching the handler when:
//
//   - the owning module is optional and switched off (the marker names the
//     module switch), or
//   - the owning feature's own switch is off (the marker names the feature
//     switch).
//
// A core route with no feature, or a module and feature that are on, falls
// through. The gate checks both the decoded path and, when it differs, the
// raw path chi routes on, and refuses if either is switched off, so an
// encoded "/" in an id cannot route past it.
//
// Mount it after tenancy (the flag read is tenant-scoped under RLS) and
// before the OPA role decision. A flag-store error falls open to the Seed
// default, as Enabled does for every switch (RLS is the security boundary,
// not the switch); Board reporting and Vendor claims default off, so they
// fall closed. An unknown FlagKey is a code bug and answers 500.
func ModuleGate(looker Looker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths := []string{r.URL.Path}
			if raw := r.URL.RawPath; raw != "" && raw != r.URL.Path {
				paths = append(paths, raw)
			}
			for _, p := range paths {
				o, err := Resolve(p)
				if err != nil {
					// A path the registry cannot place is no route chi
					// serves (the guard test holds every registered route
					// to a module); chi answers it.
					continue
				}
				off, err := switchedOffKey(r, looker, o)
				if err != nil {
					http.Error(w, "feature flag misconfigured: unknown key", http.StatusInternalServerError)
					return
				}
				if off != "" {
					writeFeatureDisabled(w, off)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// switchedOffKey returns the switch that turns the owner off, or "" when
// it is on. The module switch is read first, so a feature whose module is
// off names the module.
func switchedOffKey(r *http.Request, looker Looker, o Owner) (string, error) {
	keys := make([]string, 0, 2)
	if !o.Module.Core {
		keys = append(keys, o.Module.FlagKey)
	}
	if o.Feature != nil {
		keys = append(keys, o.Feature.FlagKey)
	}
	for _, k := range keys {
		on, err := Enabled(r.Context(), looker, k)
		if err != nil && errors.Is(err, ErrNotFound) {
			return "", err
		}
		if !on {
			return k, nil
		}
	}
	return "", nil
}

// writeFeatureDisabled writes the switched-off marker: 404, the
// X-Feature-Disabled header naming the flag, and {"error":"feature
// disabled"}.
func writeFeatureDisabled(w http.ResponseWriter, key string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Feature-Disabled", key)
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "feature disabled"})
}
