package api

import (
	"encoding/json"
	"net/http"
)

// VersionFields is the JSON contract for GET /v1/version. The four fields
// match what `web/lib/version.ts` reads and what `cmd/atlas-cli version`
// prints. Reordering or renaming fields is a breaking change to the BFF
// proxy and the VersionFooter component — bump the route version, do not
// mutate the shape in place.
type VersionFields struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
}

// VersionHandler serves GET /v1/version.
//
// Slice 072 anti-criterion P0-A1 — the endpoint is INTENTIONALLY public:
// no bearer auth, no authz, no tenancy GUC. It returns metadata about
// the binary, NOT tenant data. The auth-bypass is documented here and
// wired via the bearer-auth + authzmw exemption lists in
// internal/api/httpserver.go (same precedent as /health, slice 037).
// Adding auth to a metadata endpoint would silently re-bias the surface
// toward only authed users and defeat the "what am I running?" purpose
// from before sign-in.
//
// Slice 072 anti-criterion P0-A5 — over-fetching is the failure mode
// here, not stale data. The handler sets Cache-Control: public,
// max-age=300 so the browser caches for 5 minutes; the frontend's
// TanStack Query staleTime (24h) caches even longer in-memory. Version
// doesn't change between binary restarts.
type VersionHandler struct {
	fieldsFn func() VersionFields
}

// NewVersionHandler constructs the handler. fieldsFn is the callback
// cmd/atlas wires in at startup that returns the build-time-injected
// version/commit/date/go-version tuple (see cmd/atlas/version.go).
// Panics on nil fieldsFn — refusing to construct is better than
// surfacing the nil pointer on the first request.
func NewVersionHandler(fieldsFn func() VersionFields) *VersionHandler {
	if fieldsFn == nil {
		panic("api: NewVersionHandler called with nil fieldsFn")
	}
	return &VersionHandler{fieldsFn: fieldsFn}
}

// ServeHTTP renders the four fields as JSON. No request fields are read
// (no auth, no tenant, no path params), so the handler is fully
// deterministic and trivially cacheable.
func (h *VersionHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	fields := h.fieldsFn()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	// json.Marshal on a four-field struct cannot fail (no maps with
	// non-string keys, no unsupported types, no recursive references).
	// Best-effort error logging through the response body would only
	// confuse callers — drop on the floor.
	_ = json.NewEncoder(w).Encode(fields)
}
