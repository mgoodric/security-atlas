package api

import (
	"github.com/go-chi/chi/v5"

	calendarapi "github.com/mgoodric/security-atlas/internal/api/calendar"
)

// registerCalendar registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerCalendar(root *chi.Mux) {
	// Slice 094: compliance calendar. Read-only aggregation across four
	// existing source tables (audit_periods + exceptions + policies +
	// controls + control_evaluations) plus an iCalendar (RFC 5545) export.
	// Tenant isolation is enforced at the DB layer via RLS (slice 033).
	// ICS auth is a per-user opaque URL token, hashed in api_keys and
	// scope-restricted to AllowedKinds=[calendar.read.v1] — a leaked
	// calendar URL token cannot be used as a general bearer. Routes
	// appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). See docs/audit-log/094-compliance-calendar-decisions.md
	// decisions D1-D8 for the design calls the implementing agent made.
	calendarH := calendarapi.New(
		calendarapi.NewStore(s.dbPool),
		s.credStore,
	)
	calendarH.RegisterRoutes(root)
}
