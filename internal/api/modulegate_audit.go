package api

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// gatedAttemptWindow is how long one decision_audit_log row stands for
// every repeat of the same attempt (tenant, actor, switch). A client
// polling a switched-off module writes one row per window, not one per
// request (#2339).
const gatedAttemptWindow = 5 * time.Minute

// gatedAttemptCap bounds the dedupe map. Past it, expired entries are
// pruned, and if that frees nothing the map is reset: a reset can only
// write an extra row, never skip one.
const gatedAttemptCap = 10000

// gatedAttemptAuditor writes a decision_audit_log row for a request the
// module gate refused as switched off (#2339). ModuleGate runs before the
// OPA step, which audits every decision it makes, so without this a
// request to a switched-off module left no trace. The row is a deny with
// reason "module_disabled", resource_type "module" and resource_id the
// module key; policy_hits names the switch that is off.
type gatedAttemptAuditor struct {
	audit *authz.AuditWriter
	now   func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time
}

func newGatedAttemptAuditor(audit *authz.AuditWriter) *gatedAttemptAuditor {
	return &gatedAttemptAuditor{audit: audit, now: time.Now, seen: map[string]time.Time{}}
}

// Record is a featureflag.WithAttemptRecorder recorder. The 404 is already
// written, so a failed write is logged and the response stands.
func (a *gatedAttemptAuditor) Record(r *http.Request, at featureflag.GatedAttempt) {
	in := authz.BuildInput(r, nil)
	if in.TenantID == "" {
		// No credential: requireCredential answers these before the gate.
		return
	}
	if !a.first(in.TenantID + "\x00" + in.User.ID + "\x00" + at.FlagKey) {
		return
	}
	_, err := a.audit.Write(r.Context(), authz.AuditRecord{
		TenantID:      in.TenantID,
		UserID:        in.User.ID,
		UserRoles:     in.User.Roles,
		Action:        in.Action,
		ResourceType:  "module",
		ResourceID:    at.Module,
		Result:        "deny",
		Reason:        "module_disabled",
		PolicyHits:    []string{"module_gate:" + at.FlagKey},
		RequestPath:   r.URL.Path,
		RequestMethod: r.Method,
	})
	if err != nil {
		slog.Error("module gate: audit write failed",
			slog.String("module", at.Module),
			slog.String("flag_key", at.FlagKey),
			slog.String("tenant_id", in.TenantID),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
	}
}

// RecordNoAccess is a moduleaccess.WithDenyRecorder recorder (#2466): a
// request the access gate refused as outside the caller's module access.
// It writes the same row shape as Record, with reason "module_no_access"
// and policy_hits naming the module, deduped per tenant, actor and module.
// The 403 is already written, so a failed write is logged and stands.
func (a *gatedAttemptAuditor) RecordNoAccess(r *http.Request, m featureflag.Module) {
	in := authz.BuildInput(r, nil)
	if in.TenantID == "" {
		return
	}
	if !a.first(in.TenantID + "\x00" + in.User.ID + "\x00access:" + m.Key) {
		return
	}
	_, err := a.audit.Write(r.Context(), authz.AuditRecord{
		TenantID:      in.TenantID,
		UserID:        in.User.ID,
		UserRoles:     in.User.Roles,
		Action:        in.Action,
		ResourceType:  "module",
		ResourceID:    m.Key,
		Result:        "deny",
		Reason:        "module_no_access",
		PolicyHits:    []string{"module_access:" + m.Key},
		RequestPath:   r.URL.Path,
		RequestMethod: r.Method,
	})
	if err != nil {
		slog.Error("module access: audit write failed",
			slog.String("module", m.Key),
			slog.String("tenant_id", in.TenantID),
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()),
		)
	}
}

// first reports whether key has no row inside the current window, and
// starts a window for it when so.
func (a *gatedAttemptAuditor) first(key string) bool {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if at, ok := a.seen[key]; ok && now.Sub(at) < gatedAttemptWindow {
		return false
	}
	if len(a.seen) >= gatedAttemptCap {
		for k, at := range a.seen {
			if now.Sub(at) >= gatedAttemptWindow {
				delete(a.seen, k)
			}
		}
		if len(a.seen) >= gatedAttemptCap {
			a.seen = map[string]time.Time{}
		}
	}
	a.seen[key] = now
	return true
}
