// handler.go — HTTP handlers for the slice-094 compliance calendar.
//
// Surface:
//
//	GET  /v1/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD&types=...
//	     JSON event list. Cookie-auth (slice 034). RLS-enforced.
//
//	GET  /v1/calendar.ics?token=<opaque>&from=...&to=...&types=...
//	     iCalendar 2.0 (RFC 5545) feed. URL-token auth. RLS-enforced.
//
//	POST /v1/calendar/subscription
//	     Mints (or reissues) the per-user ICS URL token. Cookie-auth.
//	     Returns {url, expires_at} ONCE; the plaintext token is not
//	     recoverable.
//
// Authorization: cookie-auth surfaces (GET /v1/calendar, POST
// /v1/calendar/subscription) admit any signed-in user — the calendar is
// cross-business by design (AC-9). The ICS surface (GET /v1/calendar.ics)
// admits only credentials whose AllowedKinds list contains
// "calendar.read.v1" — a leaked calendar URL token cannot be used as a
// general bearer for the rest of the platform API.
package calendar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Constants reflect the slice-094 acceptance criteria.
const (
	// defaultWindowDays is the AC-4 forward window when `from`/`to` are
	// omitted on the JSON endpoint.
	defaultWindowDays = 90

	// maxWindowDays is the AC-4 ceiling — one year. Bounds the result
	// set without a hard offset/limit.
	maxWindowDays = 366

	// truncateThreshold is the AC-5 ceiling on returned events. The
	// handler asks the store for threshold+1 rows; if the +1th row
	// arrived, the response is marked truncated.
	truncateThreshold = 500

	// validEventTypes is the AC-1 closed set. Filters outside this set
	// are rejected with 400. Slice 675 added `vendor`; #2130 added
	// `risk` (risk reviews), `action_plan` (action-plan due dates) and
	// `policy_ack` (policy acknowledgement expiries), making the calendar
	// the one list of what is due. #2215 added `offboarding` (open
	// offboarding checklists), which the query already emitted but the
	// filter could not name.
	validEventTypes = "audit,exception,policy,vendor,control,risk,action_plan,policy_ack,offboarding"

	// calendarScopeKind is the api_keys AllowedKinds entry used to
	// scope a credential to "may only fetch the ICS feed." See decision
	// D3 in the slice-094 decisions log.
	calendarScopeKind = credstore.KindCalendarRead

	// subscriptionTTL is the lifetime of a freshly-minted ICS URL
	// token. One year matches the typical "subscribe once, forget"
	// usage profile for a calendar subscription URL.
	subscriptionTTL = 365 * 24 * time.Hour

	// icsCacheMaxAgeSeconds is the AC-7 server-side cache hint (5 min).
	icsCacheMaxAgeSeconds = 300
)

// Handler bundles the slice-094 routes over the calendar Store + the
// credstore that mints/authenticates ICS URL tokens.
type Handler struct {
	store  *Store
	creds  *credstore.Store
	engine *authz.Engine
	views  *moduleaccess.Views
	nowFn  func() time.Time
	urlGen func(token string) string
}

// New constructs a Handler. now defaults to time.Now().UTC if nil. urlGen
// builds the public ICS URL from a freshly-minted opaque token; the
// default returns a relative path "/v1/calendar.ics?token=..." which
// is correct for the same-origin web shell. Callers (tests, self-host
// installs behind a custom base URL) can override.
func New(store *Store, creds *credstore.Store) *Handler {
	return &Handler{
		store:  store,
		creds:  creds,
		nowFn:  func() time.Time { return time.Now().UTC() },
		urlGen: func(token string) string { return "/v1/calendar.ics?token=" + token },
	}
}

// WithNow injects a fake clock — tests use this to make event-status
// computations deterministic.
func (h *Handler) WithNow(fn func() time.Time) *Handler {
	h.nowFn = fn
	return h
}

// WithAuthz wires the OPA engine that narrows the gated event types
// (gatedEventTypes) to the ones the caller may read (#2130). With no
// engine (unit-test servers) the JSON list returns every type, as the
// search handler does; the ICS feed leaves the gated types out.
func (h *Handler) WithAuthz(engine *authz.Engine) *Handler {
	h.engine = engine
	return h
}

// WithModules wires the module views (#2173): event types from a module
// that is switched off, or outside the viewer's module access, are left
// out of the list and the ICS feed. With none (unit-test servers) every
// module is shown.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// WithURLGenerator injects a custom URL builder for the ICS subscription
// response. Self-host installs override this to embed their public origin.
func (h *Handler) WithURLGenerator(fn func(token string) string) *Handler {
	h.urlGen = fn
	return h
}

// RegisterRoutes attaches the three calendar routes onto root. The slice
// follows the slice-011/020/021 chi-double-Mount-avoidance pattern
// (RegisterRoutes-on-root, not Mount("/")).
func (h *Handler) RegisterRoutes(r interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
}) {
	r.Get("/v1/calendar", h.ListEvents)
	r.Get("/v1/calendar.ics", h.ICS)
	r.Post("/v1/calendar/subscription", h.Subscribe)
}

// ----- wire shapes -----

// eventWire is the AC-2 unified envelope. `cadence` is populated only for
// event_type=control; the other three sources return null.
type eventWire struct {
	ID                string  `json:"id"`
	Type              string  `json:"type"`
	Title             string  `json:"title"`
	StartsAt          string  `json:"starts_at"`
	EndsAt            *string `json:"ends_at,omitempty"`
	RelatedEntityID   string  `json:"related_entity_id"`
	RelatedEntityKind string  `json:"related_entity_kind"`
	Summary           string  `json:"summary"`
	Status            string  `json:"status"`
	Cadence           *string `json:"cadence,omitempty"`
}

// listResponse is the AC-1/AC-5 response envelope.
type listResponse struct {
	Events    []eventWire `json:"events"`
	Count     int         `json:"count"`
	From      string      `json:"from"`
	To        string      `json:"to"`
	Truncated bool        `json:"truncated"`
	NextFrom  *string     `json:"next_from,omitempty"`
}

// subscribeResponse is the POST /v1/calendar/subscription return.
type subscribeResponse struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// ----- handlers -----

// ListEvents handles GET /v1/calendar (AC-1 / AC-2 / AC-4 / AC-5).
func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	ctx, ok := tenantContext(r)
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	from, to, err := parseWindow(r, h.nowFn())
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	typeFilter, err := normalizeTypeFilter(r.URL.Query().Get("types"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	typeFilter, anyLeft := h.readableTypes(r, typeFilter)
	viewer, err := h.views.For(r)
	if err != nil {
		httperr.WriteInternal(w, r, "calendar module access", err)
		return
	}
	if anyLeft {
		if typeFilter, anyLeft, err = VisibleTypes(viewer, typeFilter); err != nil {
			httperr.WriteInternal(w, r, "calendar module access", err)
			return
		}
	}
	var rows []dbx.ListCalendarEventsRow
	if anyLeft {
		rows, err = h.store.ListEvents(ctx, from, to, h.nowFn(), typeFilter, int32(truncateThreshold+1))
		if err != nil {
			httperr.WriteInternal(w, r, "calendar", err)
			return
		}
	}

	truncated := len(rows) > truncateThreshold
	if truncated {
		rows = rows[:truncateThreshold]
	}

	events := make([]eventWire, len(rows))
	for i, row := range rows {
		events[i] = rowToWire(row)
	}

	resp := listResponse{
		Events:    events,
		Count:     len(events),
		From:      from.UTC().Format(time.RFC3339),
		To:        to.UTC().Format(time.RFC3339),
		Truncated: truncated,
	}
	if truncated && len(events) > 0 {
		// next_from is the starts_at of the last row in the truncated
		// page — the caller re-issues with from=next_from to walk
		// forward. Decision D7 in the decisions log.
		nf := events[len(events)-1].StartsAt
		resp.NextFrom = &nf
	}

	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// Subscribe handles POST /v1/calendar/subscription (AC-8 mint path).
//
// Mints a per-user opaque token, stores it hashed in api_keys via
// credstore.Issue, scope-restricted to AllowedKinds=[calendarScopeKind].
// Returns the public URL ONCE. The credential's UserID is propagated
// from the calling cookie session so a future "rotate calendar URL"
// action can find and revoke the existing token.
//
// Idempotency: deliberately NOT idempotent — every POST mints a new
// token. The v1 contract is "the operator gets a fresh URL each time
// they click subscribe." If a user clicks twice and pastes the older
// URL into a calendar client, that older token still works until its
// TTL or until the user invokes a future "rotate" action. This trades
// a small UX wart for a much simpler implementation; the slice's P0
// anti-criteria do not forbid it.
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		httpresp.WriteError(w, http.StatusUnauthorized, "credential context missing")
		return
	}
	if cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}

	// #2173: the token belongs to the subscriber, so the feed shows them
	// only the modules they may see, re-read at every fetch.
	_, plaintext, err := h.creds.IssueOwned(
		cred.TenantID,
		[]string{calendarScopeKind},
		subscriptionTTL,
		credstore.MinterUserID(cred),
	)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("issue calendar token: %v", err))
		return
	}

	httpresp.WriteJSON(w, http.StatusCreated, subscribeResponse{
		URL:       h.urlGen(plaintext),
		ExpiresAt: h.nowFn().Add(subscriptionTTL).Format(time.RFC3339),
	})

}

// ICS handles GET /v1/calendar.ics (AC-6 / AC-7 / AC-8).
//
// Auth flow: the URL `?token=<opaque>` is the bearer. The handler
// authenticates the token via credstore.Authenticate, REJECTS any
// credential whose AllowedKinds does not contain calendarScopeKind
// (defense against a leaked general bearer being repurposed as a
// calendar URL, and vice versa), and only then resolves the tenant.
// The tenant for ICS requests is sourced FROM the credential, NOT from
// an upstream tenancy middleware — calendar clients (Google / Apple /
// Outlook) do not carry the session cookie that the middleware needs.
func (h *Handler) ICS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "calendar token required")
		return
	}
	cred, err := h.creds.Authenticate(token)
	if err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "invalid calendar token")
		return
	}
	if !hasCalendarScope(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "token not scoped for calendar access")
		return
	}

	// Apply the tenant GUC by injecting it into the request context.
	// The tenant for ICS is sourced from the credential — the upstream
	// cookie-auth middleware is bypassed because calendar clients
	// can't carry cookies.
	ctx, terr := tenancy.WithTenant(r.Context(), cred.TenantID)
	if terr != nil {
		httperr.WriteInternal(w, r, "calendar", terr)
		return
	}

	from, to, err := parseWindow(r.WithContext(ctx), h.nowFn())
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	typeFilter, err := normalizeTypeFilter(r.URL.Query().Get("types"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// #2331: the feed shows the gated types its subscriber may read now,
	// asked of OPA with their live user_roles (ADR 0027). A token that
	// names no subscriber (minted before #2173) leaves them all out, and
	// so does a failed role lookup: the feed still serves the rest.
	subscriber := credstore.MinterUserID(cred)
	typeFilter, anyLeft, aerr := ReadableTypesFor(ctx, h.engine, cred.TenantID, subscriber, typeFilter)
	if aerr != nil {
		slog.WarnContext(ctx, "calendar ics: subscriber authz failed; gated types left out",
			slog.String("credential_id", cred.ID), slog.Any("err", aerr))
	}
	// #2173: the feed shows the modules its subscriber may see now, not
	// when they subscribed.
	if anyLeft {
		tenantID, perr := uuid.Parse(cred.TenantID)
		if perr != nil {
			httperr.WriteInternal(w, r, "calendar", perr)
			return
		}
		viewer, verr := h.views.ForUser(r.WithContext(ctx), tenantID, subscriber)
		if verr != nil {
			httperr.WriteInternal(w, r, "calendar module access", verr)
			return
		}
		if typeFilter, anyLeft, err = VisibleTypes(viewer, typeFilter); err != nil {
			httperr.WriteInternal(w, r, "calendar module access", err)
			return
		}
	}
	var rows []dbx.ListCalendarEventsRow
	if anyLeft {
		rows, err = h.store.ListEvents(ctx, from, to, h.nowFn(), typeFilter, int32(truncateThreshold+1))
		if err != nil {
			httperr.WriteInternal(w, r, "calendar", err)
			return
		}
	}
	if len(rows) > truncateThreshold {
		rows = rows[:truncateThreshold]
	}

	body := renderICS(rows, calendarNameFor(cred), h.nowFn())

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", icsCacheMaxAgeSeconds))
	w.Header().Set("Content-Disposition", `attachment; filename="compliance-calendar.ics"`)
	_, _ = w.Write([]byte(body))
}

// ----- helpers -----

func tenantContext(r *http.Request) (context.Context, bool) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		return nil, false
	}
	return r.Context(), true
}

func hasCalendarScope(c credstore.Credential) bool {
	for _, k := range c.Kinds {
		if k == calendarScopeKind {
			return true
		}
	}
	return false
}

// calendarNameFor builds the AC-7 X-WR-CALNAME header value. We do NOT
// have a tenants table lookup in this slice (avoid a write-path
// coupling), so the human-facing label is "Compliance calendar
// (<tenant-id-short>)" — operators can rename the calendar inside
// Google/Apple Calendar after subscribing. A future slice can replace
// this with a real tenant display-name lookup.
func calendarNameFor(c credstore.Credential) string {
	if c.TenantID == "" {
		return "Compliance calendar"
	}
	short := c.TenantID
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("Compliance calendar (%s)", short)
}

// parseWindow extracts the AC-4 from/to query params and applies the
// defaults. Both must be YYYY-MM-DD; missing values default to
// `now` and `now + 90d`; the spread must be <= maxWindowDays.
func parseWindow(r *http.Request, now time.Time) (time.Time, time.Time, error) {
	q := r.URL.Query()
	fromStr := q.Get("from")
	toStr := q.Get("to")

	from := now
	if fromStr != "" {
		t, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid `from` (want YYYY-MM-DD): %w", err)
		}
		from = t.UTC()
	}

	to := now.Add(defaultWindowDays * 24 * time.Hour)
	if toStr != "" {
		t, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid `to` (want YYYY-MM-DD): %w", err)
		}
		to = t.UTC().Add(24 * time.Hour) // exclusive upper bound
	}

	if !to.After(from) {
		return time.Time{}, time.Time{}, errors.New("`to` must be strictly after `from`")
	}
	if to.Sub(from) > maxWindowDays*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("window exceeds %d days", maxWindowDays)
	}
	return from, to, nil
}

// normalizeTypeFilter validates a comma-separated event-type filter
// against the AC-1 closed vocabulary. An empty filter means "all four
// types." An unknown value returns 400.
func normalizeTypeFilter(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	parts := strings.Split(raw, ",")
	allowed := map[string]struct{}{
		"audit":       {},
		"exception":   {},
		"policy":      {},
		"vendor":      {},
		"control":     {},
		"risk":        {},
		"action_plan": {},
		"policy_ack":  {},
		"offboarding": {},
	}
	kept := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := allowed[p]; !ok {
			return "", fmt.Errorf("unknown event type %q (valid: %s)", p, validEventTypes)
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		kept = append(kept, p)
	}
	return strings.Join(kept, ","), nil
}

// gatedEventTypes maps each calendar event type that needs more than
// calendar access to the OPA resource type whose read it needs (#2130).
// Roles admitted to `calendar` (auditor, control_owner, viewer) are not
// all admitted to risks or action plans, so those events are shown only
// to a caller who could read the records themselves, as /v1/search does
// per type. Offboarding events (#2215) carry the departing person's work
// email, so they need a personnel-security read, which only the GRC
// engineer and admin hold.
//
// Vendor and exception events (#2232) name a vendor or the control an
// exception waives, so they need a vendors or exceptions read: the auditor
// reads exceptions but not vendors, and the control owner reads neither.
// The ICS feed asks OPA about its subscriber instead (ReadableTypesFor,
// #2331), and drops every gated type for a token that names none.
var gatedEventTypes = map[string]string{
	"risk":        "risks",
	"action_plan": "action-plans",
	"offboarding": "personnel-security",
	"vendor":      "vendors",
	"exception":   "exceptions",
}

// readableTypes narrows a normalized type filter to the types the caller
// may read; see ReadableTypes.
func (h *Handler) readableTypes(r *http.Request, typeFilter string) (string, bool) {
	return ReadableTypes(r, h.engine, typeFilter)
}

// ReadableTypes narrows a normalized type filter to the event types the
// caller of r may read. Gated types (gatedEventTypes) the caller's OPA
// decision denies are dropped. It returns the filter to query with and
// whether any type is left; an empty filter means every type, so a
// narrowed one is spelled out. With no engine every type stays (unit-test
// servers). The dashboard export's upcoming panel calls it too (#2234),
// so the export and the calendar show a caller the same events.
func ReadableTypes(r *http.Request, engine *authz.Engine, typeFilter string) (string, bool) {
	if engine == nil {
		return typeFilter, true
	}
	denied := map[string]string{}
	for eventType, resource := range gatedEventTypes {
		in := authz.BuildInput(r, nil)
		in.Resource.Type = resource
		in.Resource.ID = ""
		in.Action = "read"
		d, err := engine.Decide(r.Context(), in)
		if err != nil || !d.Allow {
			denied[eventType] = resource
		}
	}
	return withoutTypes(typeFilter, denied)
}

// ReadableTypesFor narrows a normalized type filter to the event types
// the person userID (a bare users(id) uuid) may read in tenantID, for a
// caller that is not that person: the ICS feed, whose calendar-only token
// acts for its subscriber on these reads alone (#2331, ADR 0027). OPA
// decides as them with no role but the live user_roles rows the engine
// resolves in Decide, so a demoted subscriber's feed narrows at the next
// fetch and a deactivated or removed one reads no gated type.
//
// It fails closed: with no engine or no userID every gated type is left
// out, and so it is when a decision errors, which is also returned for
// the caller to log. ctx must carry tenantID (tenancy.WithTenant) for the
// role lookup.
func ReadableTypesFor(ctx context.Context, engine *authz.Engine, tenantID, userID, typeFilter string) (string, bool, error) {
	if engine == nil || userID == "" {
		filter, left := withoutTypes(typeFilter, gatedEventTypes)
		return filter, left, nil
	}
	denied := map[string]string{}
	for eventType, resource := range gatedEventTypes {
		d, err := engine.Decide(ctx, authz.Input{
			User: authz.UserInput{
				ID:    userID,
				Attrs: map[string]interface{}{"is_machine_actor": false, "is_super_admin": false},
			},
			TenantID: tenantID,
			Action:   "read",
			Resource: authz.ResourceInput{Type: resource, Attrs: map[string]interface{}{}},
			Request:  authz.RequestInput{Method: http.MethodGet, Path: "/v1/calendar.ics"},
		})
		if err != nil {
			filter, left := withoutTypes(typeFilter, gatedEventTypes)
			return filter, left, err
		}
		if !d.Allow {
			denied[eventType] = resource
		}
	}
	filter, left := withoutTypes(typeFilter, denied)
	return filter, left, nil
}

// withoutTypes removes drop's keys from a normalized type filter ("" is
// every type). It returns the remaining filter and whether any type is
// left.
func withoutTypes(typeFilter string, drop map[string]string) (string, bool) {
	if len(drop) == 0 {
		return typeFilter, true
	}
	src := typeFilter
	if src == "" {
		src = validEventTypes
	}
	kept := make([]string, 0, 8)
	for _, t := range strings.Split(src, ",") {
		if _, gone := drop[t]; !gone {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, ","), len(kept) > 0
}

// eventTypeRoutes names, for each event type, a route of the module that
// owns its records, so the module registry (featureflag.Modules) decides
// which module an event belongs to (#2173). Audit and control events are
// core and always shown.
var eventTypeRoutes = map[string]string{
	"audit":       "/v1/audit-periods",
	"exception":   "/v1/exceptions",
	"policy":      "/v1/policies",
	"vendor":      "/v1/vendors",
	"control":     "/v1/controls",
	"risk":        "/v1/risks",
	"action_plan": "/v1/action-plans",
	"policy_ack":  "/v1/me/acknowledgments",
	"offboarding": "/v1/personnel-security",
}

// VisibleTypes narrows a normalized type filter to the event types whose
// module the viewer may see: switched on, and inside their module access
// (#2173). It returns the filter to query with and whether any type is
// left. The dashboard export's upcoming panel calls it too, so the export
// and the calendar agree.
func VisibleTypes(v moduleaccess.Viewer, typeFilter string) (string, bool, error) {
	hidden, err := v.Hidden(eventTypeRoutes)
	if err != nil {
		return "", false, err
	}
	drop := make(map[string]string, len(hidden))
	for t := range hidden {
		drop[t] = ""
	}
	filter, left := withoutTypes(typeFilter, drop)
	return filter, left, nil
}

// rowToWire maps a sqlc-generated row into the AC-2 wire shape.
func rowToWire(r dbx.ListCalendarEventsRow) eventWire {
	ev := eventWire{
		ID:                r.EventID,
		Type:              r.EventType,
		Title:             r.Title,
		StartsAt:          r.StartsAt.Time.UTC().Format(time.RFC3339),
		RelatedEntityID:   r.RelatedEntityID,
		RelatedEntityKind: r.RelatedEntityKind,
		Summary:           r.Summary,
		Status:            r.Status,
	}
	if r.EndsAt.Valid {
		s := r.EndsAt.Time.UTC().Format(time.RFC3339)
		ev.EndsAt = &s
	}
	if r.Cadence != nil {
		c := *r.Cadence
		ev.Cadence = &c
	}
	return ev
}
