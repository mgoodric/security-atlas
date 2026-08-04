// Package authzmw provides the chi HTTP middleware that enforces
// authorization decisions on every API request via the embedded OPA
// engine (internal/authz). This is constitutional invariant 6's
// application-layer twin: tenancymw sets the tenant GUC for RLS;
// authzmw evaluates the role + ABAC predicates for what the caller
// can actually DO within that tenant.
//
// Attach order in internal/api/httpserver.go (slice 035):
//
//	root.Use(corsMiddleware)                            // CORS
//	root.Use(httpAuthMiddlewareWithExemptions(...))     // bearer auth (slice 034)
//	root.Use(tenancymw.Middleware)                      // tenant GUC (slice 033)
//	root.Use(authzmw.Middleware(engine, audit, exempt)) // authz (slice 035)
//
// Exempt prefixes (matches the bearer-auth exempt set + a health probe):
//
//	/auth/    - login / callback / logout (user has no bearer yet)
//	/health   - liveness probe
//
// Anti-criteria honored (P0):
//
//   - NO endpoint without explicit Decide call: every non-exempt path
//     reaches authz.Decide. The matrix integration test enumerates
//     every chi route + every role + asserts a Decide call occurred
//     before the handler.
//   - NO admin emergency-bypass: the middleware has no
//     "skip-on-admin" branch. Admin allow comes from admin.rego, not
//     from middleware code.
//   - NO decision skips the audit log: the middleware calls
//     audit.Write on both allow and deny. On allow, audit failure
//     fails the request with 500.
package authzmw

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/requestidmw"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// Middleware returns the chi-compatible middleware that calls
// engine.Decide on every non-exempt request and writes the result to
// the decision_audit_log via audit. exempt is the list of path
// prefixes that should bypass authz entirely (mirrors the bearer-auth
// exempt set). When engine or audit is nil, the middleware logs at
// startup and is a no-op for that request -- production callers
// always wire both.
func Middleware(engine *authz.Engine, audit *authz.AuditWriter, exempt ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Exempt prefix check.
			for _, p := range exempt {
				if p != "" && strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Without an engine (test mode where authz is disabled), pass
			// through. Production servers always wire one.
			if engine == nil {
				next.ServeHTTP(w, r)
				return
			}

			// Build the canonical input. If no credential is in
			// context, the input arrives with empty roles -- default-deny
			// fires.
			in := authz.BuildInput(r, nil)

			decision, err := engine.Decide(r.Context(), in)
			if err != nil {
				// OE-432 (slice 356a G-1/G-2): distinguish a dependency
				// outage from a genuine policy-engine failure, and never
				// drop err on the floor unlogged.
				//
				//   - DB unreachable → 503 database_unavailable with a
				//     retry_after hint (slice 335 Experiment 3 shape),
				//     logged here with request context.
				//   - anything else → generic 500 via httperr, which
				//     logs the full error keyed by request_id (slice
				//     367 CWE-209 discipline).
				if errors.Is(err, authz.ErrDependencyUnavailable) {
					writeDatabaseUnavailable(w, r, in, err)
					return
				}
				httperr.WriteInternal(w, r, "authz decide", err)
				return
			}

			rec := authz.AuditRecord{
				TenantID:      in.TenantID,
				UserID:        in.User.ID,
				UserRoles:     in.User.Roles,
				Action:        in.Action,
				ResourceType:  in.Resource.Type,
				ResourceID:    in.Resource.ID,
				Reason:        decision.Reason,
				PolicyHits:    decision.PolicyHits,
				RequestPath:   r.URL.Path,
				RequestMethod: r.Method,
			}
			if decision.Allow {
				rec.Result = "allow"
			} else {
				rec.Result = "deny"
			}

			// On deny: write 403 first, then audit. Audit failure
			// logs but does not change the response (the user is
			// already denied).
			if !decision.Allow {
				httpresp.WriteError(w, http.StatusForbidden, "forbidden")
				_, _ = audit.Write(r.Context(), rec)
				return
			}

			// On allow: audit FIRST, fail the request if audit fails
			// (anti-criterion P0: never proceed with an unaudited
			// allow). Then call next.
			if audit != nil {
				if _, err := audit.Write(r.Context(), rec); err != nil {
					httpresp.WriteError(w, http.StatusInternalServerError, "audit log write failed")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// dbUnavailableRetryAfterSeconds is the client back-off hint carried in
// both the Retry-After header and the JSON retry_after field of the 503
// dependency-outage response. 5s per the slice 335 Experiment 3 design
// (the measured recovery after Postgres restart was ~1s, so 5s is a
// comfortable client-side backoff).
const dbUnavailableRetryAfterSeconds = 5

// writeDatabaseUnavailable emits the dependency-outage response for an
// authz decision that failed because the database was unreachable
// (authz.ErrDependencyUnavailable): HTTP 503 with the structured body
//
//	{"error":"database_unavailable","retry_after":5,"request_id":"<id>"}
//
// plus a Retry-After header, and logs the full underlying error at
// error level with request context (request_id, method, path, tenant,
// user). The body carries no driver text, SQLSTATE, or internal detail
// — that stays in the server-side log, keyed by request_id, matching
// the slice 367 error-shape discipline.
//
// Not routed through httperr.WriteStatus because that helper
// deliberately genericises every 5xx body to "internal error"; this
// response's whole point is to name the unavailable dependency and
// carry a retry hint (slice 356a gap G-1).
func writeDatabaseUnavailable(w http.ResponseWriter, r *http.Request, in authz.Input, err error) {
	id := requestidmw.RequestIDFromContext(r.Context())
	if id == "" {
		// Same fallback as httperr: mint an ID when the middleware is
		// exercised without requestidmw in the chain (direct handler
		// tests) so the client always gets a correlatable ID.
		id = uuid.NewString()
	}

	slog.Error("authz decide failed: database dependency unavailable",
		slog.String("request_id", id),
		slog.String("op", "authz decide"),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.String("tenant_id", in.TenantID),
		slog.String("user_id", in.User.ID),
		slog.Int("status", http.StatusServiceUnavailable),
		slog.String("error", err.Error()),
	)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(requestidmw.HeaderName, id)
	w.Header().Set("Retry-After", strconv.Itoa(dbUnavailableRetryAfterSeconds))
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":       "database_unavailable",
		"retry_after": dbUnavailableRetryAfterSeconds,
		"request_id":  id,
	})
}

// IsCredentialPresent is exported for the matrix integration test to
// assert that a credential is established on the context before the
// middleware runs.
func IsCredentialPresent(r *http.Request) bool {
	_, ok := authctx.CredentialFromContext(r.Context())
	return ok
}
