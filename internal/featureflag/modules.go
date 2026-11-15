package featureflag

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Module is one optional module in the module registry (#2166). An
// optional module is switched on or off per tenant by its module switch,
// which is an ordinary feature flag in Seed: the registry adds no second
// toggle store, only which routes and pages belong to the flag.
//
// A route or page that belongs to no module here is core: always on.
type Module struct {
	// Key names the module on the wire (GET /v1/me/modules).
	Key string
	// Name is the module's display name.
	Name string
	// FlagKey is the module switch: a Seed key whose default is the
	// module's default.
	FlagKey string
	// Routes are the API path prefixes the module owns. A prefix owns
	// itself and every path below it ("/v1/questionnaires" owns
	// "/v1/questionnaires/{id}/answers/{qid}"), matched on a segment
	// boundary so "/v1/questionnaires" does not own "/v1/questionnaires-x".
	Routes []string
	// Pages are the web app paths the module owns, matched the same way.
	// The web app hides a nav entry and gates a page from these, so it
	// keeps no gating map of its own.
	Pages []string
}

// Modules is the module registry. The order is the order GET
// /v1/me/modules reports them in.
//
// #2170 is the tracer: Questionnaires is the first optional module, and
// the two modules the web app gated by hand before it (Board reporting,
// and Vendor claims under `oscal.export`) move in unchanged. The
// remaining optional modules join in later tickets of #2166.
var Modules = []Module{
	{
		Key:     "questionnaires",
		Name:    "Questionnaires",
		FlagKey: "questionnaires.enabled",
		Routes:  []string{"/v1/questionnaires"},
		Pages:   []string{"/questionnaires"},
	},
	{
		Key:     "board_reporting",
		Name:    "Board reporting",
		FlagKey: "board.reporting",
		Routes:  []string{"/v1/board-briefs", "/v1/board-packs", "/v1/board"},
		Pages:   []string{"/board-packs"},
	},
	{
		Key:     "vendor_claims",
		Name:    "Vendor claims",
		FlagKey: "oscal.export",
		Routes:  []string{"/v1/oscal/component-definitions", "/v1/oscal/component-claims"},
		Pages:   []string{"/oscal/component-definitions"},
	},
}

// ModuleForPath returns the optional module that owns an API path, and
// false when the path is core.
func ModuleForPath(path string) (Module, bool) {
	for _, m := range Modules {
		for _, prefix := range m.Routes {
			if ownsPath(prefix, path) {
				return m, true
			}
		}
	}
	return Module{}, false
}

// ownsPath reports whether prefix owns path: equal, or path continues
// past prefix at a "/" or ":" (the {id}:verb route shape).
func ownsPath(prefix, path string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	next := path[len(prefix)]
	return next == '/' || next == ':'
}

// ModuleGate is the module gate step of the authorization chain. It
// resolves the request path's module from the registry and, when that
// module is switched off for the caller's tenant, answers 404 with the
// switched-off marker (X-Feature-Disabled: <flag key>, body
// {"error":"feature disabled"}) without reaching the handler. A core path,
// or a module that is on, falls through.
//
// It replaces the per-route Gate wrapping for every module in the
// registry. Mount it after tenancy (the flag read is tenant-scoped under
// RLS) and before the OPA role decision. Like Gate, a DB error falls open
// to the Seed default (Enabled); an unknown FlagKey is a code bug and
// answers 500.
func ModuleGate(looker Looker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m, ok := ModuleForPath(r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			on, err := Enabled(r.Context(), looker, m.FlagKey)
			if err != nil && errors.Is(err, ErrNotFound) {
				http.Error(w, "feature flag misconfigured: unknown key", http.StatusInternalServerError)
				return
			}
			if !on {
				writeFeatureDisabled(w, m.FlagKey)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeFeatureDisabled writes the switched-off marker: 404, the
// X-Feature-Disabled header naming the flag, and {"error":"feature
// disabled"}. Shared by Gate and ModuleGate so the shape cannot drift.
func writeFeatureDisabled(w http.ResponseWriter, key string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Feature-Disabled", key)
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "feature disabled"})
}
