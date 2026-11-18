// Package approvals serves GET /v1/approvals (#2119): what is waiting for
// the caller's approval across the four records that need a second person
// — requested exceptions, proposed risk acceptances, policies under review
// and framework scopes in review.
//
// The list is exactly what each type's approve handler would accept from
// the caller. It applies the same gates in the same order:
//
//  1. the type's module is on (featureflag.RouteOn on its approve route,
//     the check ModuleGate makes);
//  2. authz.IsTenantApprover(cred), the handler's role gate;
//  3. the record is in the state the approve move leaves from (filtered
//     in SQL, #2457, and checked again per row);
//  4. the type's own approver check (exception.CheckApprover,
//     risk.CheckAcceptanceApprover, policy.CheckApprover,
//     frameworkscope.CheckApprover), called per row with the tenant's
//     Allow self-approval setting. Those are the functions the stores call
//     inside the approve transaction, so the two cannot disagree.
//
// A row the caller requested is listed only when the organisation allows
// self-approval, and is then marked self_approval. A machine credential
// approves nothing (only people approve, #2101), so it gets an empty list,
// as does any caller who is not an approver.
//
// Every read runs on the app pool under the caller's RLS tenant
// (invariant #6), so no count includes another tenant's rows.
package approvals

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/approval"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// The four types, in the order the page groups them.
const (
	TypeException      = "exception"
	TypeRiskAcceptance = "risk_acceptance"
	TypePolicy         = "policy"
	TypeFrameworkScope = "framework_scope"
)

// Types lists the four types in page order.
var Types = []string{TypeException, TypeRiskAcceptance, TypePolicy, TypeFrameworkScope}

// approveRoutes is each type's approve route, as ModuleGate sees it. The
// id segment is a placeholder: the module registry matches by prefix.
var approveRoutes = map[string]string{
	TypeException:      "/v1/exceptions/_/approve",
	TypeRiskAcceptance: "/v1/risks/_/acceptance/approve",
	TypePolicy:         "/v1/policies/_/approve",
	TypeFrameworkScope: "/v1/framework-scopes/_/approve",
}

// MaxPerType caps the items listed per type, oldest first. Counts are
// never capped: a type past the cap reports its full count and
// truncated=true.
const MaxPerType = 100

// Item is one record waiting for the caller's approval.
type Item struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Title string `json:"title"`
	// RequestedBy is who asked for the approval: the exception's
	// requester, the acceptance's proposer, the policy's or scope's
	// submitter.
	RequestedBy string `json:"requested_by"`
	// RequestedAt is when they asked; nil when the record does not say.
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	// SelfApproval marks the caller's own request, listed because the
	// organisation allows self-approval. Approving it flags the record
	// self_approved.
	SelfApproval bool `json:"self_approval"`
	// FrameworkVersionID is set on a framework scope: its page is keyed
	// by framework version.
	FrameworkVersionID string `json:"framework_version_id,omitempty"`
}

// Response is the GET /v1/approvals shape.
type Response struct {
	Items     []Item         `json:"items"`
	Counts    map[string]int `json:"counts"`
	Total     int            `json:"total"`
	Truncated bool           `json:"truncated"`
}

func emptyResponse() Response {
	counts := make(map[string]int, len(Types))
	for _, t := range Types {
		counts[t] = 0
	}
	return Response{Items: []Item{}, Counts: counts}
}

// Handler serves GET /v1/approvals.
type Handler struct {
	pool  *pgxpool.Pool
	flags featureflag.Looker
	views *moduleaccess.Views
}

// New builds a Handler on the app pool. flags is the module switch store
// ModuleGate reads.
func New(pool *pgxpool.Pool, flags featureflag.Looker) *Handler {
	return &Handler{pool: pool, flags: flags}
}

// WithModules wires the module views (#2173): a type whose module is
// outside the caller's module access is not listed or counted, as its
// approve route would answer 403. With none only the switch is checked.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// List handles GET /v1/approvals.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || cred.TenantID == "" {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	tenantID, err := uuid.Parse(cred.TenantID)
	if err != nil {
		httpresp.WriteError(w, http.StatusInternalServerError, "invalid tenant in credential")
		return
	}
	items, err := h.Pending(r, cred, tenantID)
	if errors.Is(err, ErrNoDatabase) {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "approvals unavailable: no database")
		return
	}
	if err != nil {
		httperr.WriteInternal(w, r, "list approvals", err)
		return
	}
	httpresp.WriteJSON(w, http.StatusOK, Build(items))
}

// ErrNoDatabase is Pending's answer to an approver on a server with no
// database.
var ErrNoDatabase = errors.New("approvals: no database")

// Pending returns every record waiting for cred's approval, unordered and
// uncapped, under the gates the package comment lists. A caller who is not
// an approver, or has no person behind it, gets none. GET
// /v1/needs-attention (#2134) lists the same rows as its approvals band.
func (h *Handler) Pending(r *http.Request, cred credstore.Credential, tenantID uuid.UUID) ([]Item, error) {
	approver := approval.Approver{UserID: credstore.MinterUserID(cred), CredentialID: cred.ID}
	// Gates 2 and "only people approve": nothing to read.
	if !authz.IsTenantApprover(cred) || approver.UserID == "" {
		return nil, nil
	}
	if h.pool == nil {
		return nil, ErrNoDatabase
	}
	viewer, err := h.views.For(r)
	if err != nil {
		return nil, fmt.Errorf("approvals module access: %w", err)
	}
	on := map[string]bool{}
	for _, t := range Types {
		if on[t], err = featureflag.RouteOn(r, h.flags, approveRoutes[t]); err != nil {
			return nil, fmt.Errorf("approvals module check: %w", err)
		}
		if !on[t] {
			continue
		}
		if on[t], err = viewer.Sees(approveRoutes[t]); err != nil {
			return nil, fmt.Errorf("approvals module access: %w", err)
		}
	}
	return h.pending(r.Context(), tenantID, approver, on)
}

// Build turns the full pending list into the response: counts per type
// over every item, and at most MaxPerType items per type, oldest first.
func Build(items []Item) Response {
	out := emptyResponse()
	byType := map[string][]Item{}
	for _, it := range items {
		byType[it.Type] = append(byType[it.Type], it)
	}
	for _, t := range Types {
		list := byType[t]
		sort.SliceStable(list, func(i, j int) bool { return older(list[i], list[j]) })
		out.Counts[t] = len(list)
		out.Total += len(list)
		if len(list) > MaxPerType {
			list = list[:MaxPerType]
			out.Truncated = true
		}
		out.Items = append(out.Items, list...)
	}
	return out
}

// older orders by requested_at ascending, undated last, then by id.
func older(a, b Item) bool {
	switch {
	case a.RequestedAt != nil && b.RequestedAt != nil && !a.RequestedAt.Equal(*b.RequestedAt):
		return a.RequestedAt.Before(*b.RequestedAt)
	case (a.RequestedAt == nil) != (b.RequestedAt == nil):
		return a.RequestedAt != nil
	}
	return a.ID < b.ID
}

func (h *Handler) pending(ctx context.Context, tenantID uuid.UUID, a approval.Approver, on map[string]bool) ([]Item, error) {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return nil, err
	}
	q := dbx.New(tx)
	tid := pgtype.UUID{Bytes: tenantID, Valid: true}
	allowSelf, err := approval.AllowSelf(ctx, q, tenantID)
	if err != nil {
		return nil, err
	}

	var items []Item
	if on[TypeException] {
		rows, err := q.ListRequestedExceptions(ctx, tid)
		if err != nil {
			return nil, fmt.Errorf("list exceptions: %w", err)
		}
		titles, err := controlTitles(ctx, tx, tenantID, rows)
		if err != nil {
			return nil, err
		}
		items = append(items, PendingExceptions(rows, titles, a, allowSelf)...)
	}
	if on[TypeRiskAcceptance] {
		rows, err := q.ListRisksWithProposedAcceptance(ctx, tid)
		if err != nil {
			return nil, fmt.Errorf("list risks: %w", err)
		}
		items = append(items, PendingRiskAcceptances(rows, a, allowSelf)...)
	}
	if on[TypePolicy] {
		rows, err := q.ListPoliciesUnderReview(ctx, tid)
		if err != nil {
			return nil, fmt.Errorf("list policies: %w", err)
		}
		items = append(items, PendingPolicies(rows, a, allowSelf)...)
	}
	if on[TypeFrameworkScope] {
		rows, err := q.ListFrameworkScopesInReview(ctx, tid)
		if err != nil {
			return nil, fmt.Errorf("list framework scopes: %w", err)
		}
		items = append(items, PendingFrameworkScopes(rows, a, allowSelf)...)
	}
	return items, tx.Commit(ctx)
}

// controlTitles names the controls the requested exceptions are on.
func controlTitles(ctx context.Context, tx dbx.DBTX, tenantID uuid.UUID, rows []dbx.Exception) (map[uuid.UUID]string, error) {
	var ids []uuid.UUID
	for _, e := range rows {
		if e.Status == exception.StateRequested && e.ControlID.Valid {
			ids = append(ids, e.ControlID.Bytes)
		}
	}
	titles := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return titles, nil
	}
	rs, err := tx.Query(ctx, `SELECT id, title FROM controls WHERE tenant_id = $1 AND id = ANY($2)`, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("control titles: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var (
			id    uuid.UUID
			title string
		)
		if err := rs.Scan(&id, &title); err != nil {
			return nil, fmt.Errorf("scan control title: %w", err)
		}
		titles[id] = title
	}
	return titles, rs.Err()
}

// admitted runs a type's approver check and reports whether the row is
// listed. Any refusal (not a person, the requester without the setting,
// a requester that cannot be compared) leaves it out, as the approve
// handler would refuse it.
func admitted(self bool, err error) (listed, selfApproval bool) {
	if err != nil {
		return false, false
	}
	return true, self
}

// PendingExceptions lists the requested exceptions a may approve.
func PendingExceptions(rows []dbx.Exception, controlTitles map[uuid.UUID]string, a approval.Approver, allowSelf bool) []Item {
	var out []Item
	for _, e := range rows {
		if e.Status != exception.StateRequested {
			continue
		}
		ok, self := admitted(exception.CheckApprover(e.RequestedBy, a, allowSelf))
		if !ok {
			continue
		}
		title := controlTitles[e.ControlID.Bytes]
		if title == "" {
			title = "Exception"
		}
		out = append(out, Item{
			Type: TypeException, ID: uuidString(e.ID), Title: title,
			RequestedBy: e.RequestedBy, RequestedAt: timePtr(e.RequestedAt), SelfApproval: self,
		})
	}
	return out
}

// PendingRiskAcceptances lists the open risks with a proposed acceptance
// a may approve. A closed risk's acceptance cannot be approved until the
// risk is reopened, so it is not listed.
func PendingRiskAcceptances(rows []dbx.Risk, a approval.Approver, allowSelf bool) []Item {
	var out []Item
	for _, r := range rows {
		if r.Status != dbx.RiskStatusOpen || r.AcceptanceStatus == nil || *r.AcceptanceStatus != dbx.RiskAcceptanceStatusProposed {
			continue
		}
		ok, self := admitted(risk.CheckAcceptanceApprover(r.AcceptanceProposedBy, a, allowSelf))
		if !ok {
			continue
		}
		out = append(out, Item{
			Type: TypeRiskAcceptance, ID: uuidString(r.ID), Title: r.Title,
			RequestedBy: r.AcceptanceProposedBy, RequestedAt: timePtr(r.AcceptanceProposedAt), SelfApproval: self,
		})
	}
	return out
}

// PendingPolicies lists the policies under review a may approve.
func PendingPolicies(rows []dbx.Policy, a approval.Approver, allowSelf bool) []Item {
	var out []Item
	for _, p := range rows {
		if p.Status != policy.StateUnderReview {
			continue
		}
		ok, self := admitted(policy.CheckApprover(p, a, allowSelf))
		if !ok {
			continue
		}
		out = append(out, Item{
			Type: TypePolicy, ID: uuidString(p.ID), Title: p.Title + " v" + p.Version,
			RequestedBy: deref(p.SubmittedBy), RequestedAt: timePtr(p.SubmittedAt), SelfApproval: self,
		})
	}
	return out
}

// PendingFrameworkScopes lists the framework scopes in review a may
// approve. A scope records no submission time, so its last update stands
// in for requested_at.
func PendingFrameworkScopes(rows []dbx.FrameworkScope, a approval.Approver, allowSelf bool) []Item {
	var out []Item
	for _, s := range rows {
		if s.State != frameworkscope.StateReview {
			continue
		}
		ok, self := admitted(frameworkscope.CheckApprover(s, frameworkscope.ApproveRequest{
			ID:             s.ID.Bytes,
			ApproverUserID: a.UserID,
			Actor:          a.CredentialID,
		}, allowSelf))
		if !ok {
			continue
		}
		out = append(out, Item{
			Type: TypeFrameworkScope, ID: uuidString(s.ID), Title: s.Name,
			RequestedBy: deref(s.SubmittedBy), RequestedAt: timePtr(s.UpdatedAt), SelfApproval: self,
			FrameworkVersionID: uuidString(s.FrameworkVersionID),
		})
	}
	return out
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
