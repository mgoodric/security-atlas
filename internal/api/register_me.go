package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	auditnotesapi "github.com/mgoodric/security-atlas/internal/api/auditnotes"
	meapi "github.com/mgoodric/security-atlas/internal/api/me"
	tenantsapi "github.com/mgoodric/security-atlas/internal/api/tenants"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/audit/notes"
	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/userprefs"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/notify/email"
	"github.com/mgoodric/security-atlas/internal/notify/slack"
	"github.com/mgoodric/security-atlas/internal/notify/webhook"
)

// registerMe registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerMe(root *chi.Mux) {
	// Slice 025: auditor role + scoped read-only access.
	//
	//   POST /v1/audit-notes              auditor-only write (period assignment gated)
	//   GET  /v1/audit-notes              caller's own notes within a period
	//   GET  /v1/me/audit-period          AC-5 -- single active assignment
	//   GET  /v1/me/audit-periods         AC-6 -- all assignments
	//
	// Routes appended per the parallel-batch convention -- chi.Mux rejects
	// two Mounts at "/", so individual routes register onto the root. The
	// upstream OPA middleware (slice 035) is the primary gate; the handler
	// performs defense-in-depth on UserID / scope_type / body shape.
	// Slice 029: Audit Hub threaded comments + in-app notifications.
	// auditNotesH gets the notifications.Store so successful POSTs
	// dispatch notifications to the distinct prior-thread authors on
	// 'shared' notes. The notifications.Store is also wired into the
	// /v1/me/notifications endpoints below.
	notificationsStore := notifications.NewStore(s.dbPool)
	auditNotesH := auditnotesapi.New(notes.NewStore(s.dbPool), notificationsStore, nil)
	root.Post("/v1/audit-notes", auditNotesH.Create)
	root.Get("/v1/audit-notes", auditNotesH.List)
	// Thread retrieval is a literal sub-route declared BEFORE any
	// potential /v1/audit-notes/{id} so chi's declaration-order match
	// keeps it ahead of a bare-id route.
	root.Get("/v1/audit-notes/thread", auditNotesH.Thread)
	meAuditorStore := auditor.NewStore(s.dbPool)
	meH := meapi.New(meAuditorStore)
	root.Get("/v1/me/audit-period", meH.AuditPeriod)
	root.Get("/v1/me/audit-periods", meH.AuditPeriods)
	// Slice 029: /v1/me/notifications. Authenticated caller-scoped read
	// + mark-read surface. Routes append per the parallel-batch
	// convention (chi.Mux rejects two Mounts at "/", so individual
	// routes register onto the root). PATCH path params are resolved
	// via chi.URLParam in the handler.
	meNotificationsH := meapi.NewNotifications(notificationsStore)
	root.Get("/v1/me/notifications", meNotificationsH.List)
	root.Patch("/v1/me/notifications/{id}/read", meNotificationsH.MarkRead)
	// Slice 108: /v1/me + /v1/me/preferences + /v1/me/sessions. Each handler
	// gets its own dependency object (users store, userprefs store, sessions
	// store + dbPool for me_audit_log writes). Routes appended directly to the
	// root chi router per the parallel-batch convention. Static suffix routes
	// (/preferences, /sessions, /sessions/{id}) are declared after the bare
	// /v1/me but use distinct path shapes so there is no shadowing.
	usersStore := users.NewStore(s.dbPool)
	sessionsStore := sessions.NewStore(s.dbPool, 0)
	userprefsStore := userprefs.NewStore(s.dbPool)
	// Slice 130: share the same DBRolesResolver the slice-035 OPA engine
	// uses for `Input.UserRoles`. Building a fresh resolver here is cheap
	// (resolver is a pool wrapper, no state) and avoids threading the
	// authz.Engine's private resolver out through AttachAuthz. The shared
	// SELECT semantics + the shared `tenancy.ApplyTenant` posture are the
	// load-bearing properties; instance identity is not.
	meProfileH := meapi.NewProfile(usersStore, s.dbPool, authz.NewDBRolesResolver(s.dbPool))
	mePrefsH := meapi.NewPreferences(userprefsStore, s.dbPool)
	meSessionsH := meapi.NewSessions(sessionsStore, s.dbPool)
	root.Get("/v1/me", meProfileH.GetMe)
	root.Patch("/v1/me", meProfileH.PatchMe)
	root.Get("/v1/me/preferences", mePrefsH.GetPreferences)
	root.Patch("/v1/me/preferences", mePrefsH.PatchPreferences)
	// Slice 445: GET/PUT /v1/me/email-channel — the per-user master
	// opt-in toggle for the email delivery channel (AC-9). Default
	// opted-OUT (P0-445-7). The Channel is constructed with the SMTP
	// provider from env (inert when no SMTP host configured) + the
	// public base URL for the digest deep-link.
	emailCfg := email.ConfigFromEnv()
	emailCh := email.NewChannel(s.dbPool, email.NewSMTPProvider(emailCfg), emailCfg.BaseURL)
	// Slice 585: surface whether the OPERATOR has configured the SMTP target
	// (env present) so the settings toggle can render disabled + "not
	// configured by your administrator". Config.Enabled() is a presence check
	// only — the SMTP host/credentials never reach the wire (P0-585).
	meEmailH := meapi.NewEmailChannel(emailCh, emailCfg.Enabled())
	root.Get("/v1/me/email-channel", meEmailH.Get)
	root.Put("/v1/me/email-channel", meEmailH.Put)
	// Slice 543: GET/PUT /v1/me/slack-channel + /v1/me/webhook-channel —
	// per-user master opt-in toggles for the additional delivery channels.
	// Default opted-OUT (P0-543-3). Each channel's target + credentials are
	// OPERATOR-configured via env (inert when unset); the webhook URL is
	// SSRF-validated at construction (P0-543-2). These are SINKS only
	// (P0-543-4) — the toggle just records the per-user opt-in.
	slackCfg := slack.ConfigFromEnv()
	slackCh := slack.NewChannel(s.dbPool, slack.NewHTTPTransport(slackCfg), slackCfg.BaseURL)
	// Slice 585: slackCfg.Enabled() reports whether the operator-configured
	// Slack webhook URL env is present (presence only — the URL/token never
	// reaches the wire, P0-585 / P0-543-2). Drives the disabled toggle state.
	meSlackH := meapi.NewChannelOptIn("slack", slackCfg.Enabled(), slackCh.GetOptIn, slackCh.SetOptIn)
	root.Get("/v1/me/slack-channel", meSlackH.Get)
	root.Put("/v1/me/slack-channel", meSlackH.Put)
	webhookCfg := webhook.ConfigFromEnv()
	webhookTransport, webhookErr := webhook.NewHTTPTransport(webhookCfg, webhook.SSRFPolicy())
	if webhookErr != nil {
		// A misconfigured (internal-target) webhook URL fails fast and
		// visibly at startup rather than silently delivering to an internal
		// service (P0-543-2). The opt-in toggle still needs a channel, so
		// fall back to an inert transport and surface the config error.
		slog.Default().Error("webhook channel disabled: invalid target",
			slog.String("error", webhookErr.Error()))
		webhookTransport, _ = webhook.NewHTTPTransport(webhook.Config{}, webhook.SSRFPolicy())
	}
	webhookCh := webhook.NewChannel(s.dbPool, webhookTransport, webhookCfg.BaseURL)
	// Slice 585: the webhook is "configured" only when the env URL is present
	// AND it passed SSRF validation at construction. A present-but-invalid
	// (internal-target) URL fell back to an inert transport above, so it is
	// NOT usable — report configured=false so the toggle stays disabled.
	// Enabled() is a presence check only; the URL never reaches the wire
	// (P0-585 / P0-543-2).
	webhookConfigured := webhookCfg.Enabled() && webhookErr == nil
	meWebhookH := meapi.NewChannelOptIn("webhook", webhookConfigured, webhookCh.GetOptIn, webhookCh.SetOptIn)
	root.Get("/v1/me/webhook-channel", meWebhookH.Get)
	root.Put("/v1/me/webhook-channel", meWebhookH.Put)
	root.Get("/v1/me/sessions", meSessionsH.ListSessions)
	root.Delete("/v1/me/sessions", meSessionsH.RevokeOtherSessions)
	root.Delete("/v1/me/sessions/{id}", meSessionsH.RevokeSession)
	// Slice 192: GET /v1/me/tenants — multi-tenant directory.
	// Reads the caller's verified JWT claim
	// `atlas:available_tenants[]` and enriches with tenant names
	// from the BYPASSRLS authPool (PK-bounded query — P0-192-2).
	// When authPool is nil (test harness), the handler still
	// renders honest tenant IDs without name enrichment.
	meTenantsH := meapi.NewTenants(s.authPool)
	root.Get("/v1/me/tenants", meTenantsH.ListTenants)
	// Slice 144: PATCH /v1/tenants/{id} — rename a tenant.
	// Gated on per-tenant admin (slice-034 cred.IsAdmin OR
	// slice-187 JWT roles[CURRENT][admin]) OR super_admin
	// (slice-187 JWT atlas:super_admin claim). RLS on the
	// tenants table is the load-bearing second leg —
	// cross-tenant rename is impossible at the DB layer
	// regardless of the role gate.
	tenantsH := tenantsapi.New(s.dbPool)
	root.Patch("/v1/tenants/{id}", tenantsH.PatchTenant)
}
