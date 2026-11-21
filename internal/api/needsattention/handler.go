package needsattention

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/approvals"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/policyacks"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/duedate"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DueSoonDays is band 4's window, the calendar's and "Coming up"'s.
const DueSoonDays = calendar.DueSoonDays

// Paging bounds: the dashboard asks for 10, the full page for more.
const (
	DefaultLimit = 10
	MaxLimit     = 100
)

// calendarTypes are the calendar event types the due-based bands read.
// Audit periods, policy acknowledgement lapses (one row per policy and day
// for many people) and offboarding checklists are not review work an owner
// does, so they stay on the calendar only.
const calendarTypes = "exception,policy,vendor,control,risk,action_plan"

// DefaultWarnRows is the row count past which one source read logs a
// warning. The reads are uncapped so totals stay exact; past about 10,000
// rows a source is where the #2555 D1 measurement says to revisit that
// (#2698). cmd/atlas reads ATLAS_NEEDS_ATTENTION_WARN_ROWS.
const DefaultWarnRows = 10_000

// WarnInterval is how long one (tenant, source) pair stays quiet after a
// large-read warning. The home page polls GET /v1/needs-attention, so an
// unthrottled warning would repeat on every poll (#2783).
const WarnInterval = 15 * time.Minute

// Handler serves GET /v1/needs-attention.
type Handler struct {
	pool      *pgxpool.Pool
	calendar  *calendar.Store
	approvals *approvals.Handler
	engine    *authz.Engine
	views     *moduleaccess.Views
	nowFn     func() time.Time
	warnRows  int
	logger    *slog.Logger

	// warnMu guards warned: the last warning time per (tenant, source).
	warnMu sync.Mutex
	warned map[warnKey]time.Time
}

type warnKey struct {
	tenant uuid.UUID
	source string
}

// New builds a Handler on the app pool. approvals lists band 2, cal the
// due-based bands.
func New(pool *pgxpool.Pool, cal *calendar.Store, approvalsH *approvals.Handler) *Handler {
	return &Handler{
		pool: pool, calendar: cal, approvals: approvalsH,
		nowFn:    func() time.Time { return time.Now().UTC() },
		warnRows: DefaultWarnRows,
		warned:   map[warnKey]time.Time{},
	}
}

// WithWarnRows sets the row count past which one source read logs a
// warning. n <= 0 keeps DefaultWarnRows.
func (h *Handler) WithWarnRows(n int) *Handler {
	if n > 0 {
		h.warnRows = n
	}
	return h
}

// WithLogger sets the logger the large-read warning goes to; nil, the
// default, is slog.Default() at the time of the warning.
func (h *Handler) WithLogger(l *slog.Logger) *Handler {
	h.logger = l
	return h
}

// warnLarge logs a source read that returned more than warnRows rows:
// the tenant has reached the size where the uncapped reads should be
// revisited (#2555 D1, #2698). One (tenant, source) pair warns at most
// once per WarnInterval (#2783).
func (h *Handler) warnLarge(ctx context.Context, tenantID uuid.UUID, source string, bands []string, rows int) {
	if rows <= h.warnRows || !h.warnDue(tenantID, source) {
		return
	}
	l := h.logger
	if l == nil {
		l = slog.Default()
	}
	l.WarnContext(ctx, "needs attention: band read passed the row threshold",
		slog.String("tenant_id", tenantID.String()),
		slog.String("source", source),
		slog.String("band", strings.Join(bands, ",")),
		slog.Int("rows", rows),
		slog.Int("threshold", h.warnRows))
}

// warnDue reports whether the (tenant, source) pair may warn now and, if
// so, records the warning. Pairs past WarnInterval are dropped as it goes,
// so the map holds only pairs that warned within the interval.
func (h *Handler) warnDue(tenantID uuid.UUID, source string) bool {
	now := h.nowFn()
	k := warnKey{tenant: tenantID, source: source}
	h.warnMu.Lock()
	defer h.warnMu.Unlock()
	if last, ok := h.warned[k]; ok && now.Sub(last) < WarnInterval {
		return false
	}
	for key, at := range h.warned {
		if now.Sub(at) >= WarnInterval {
			delete(h.warned, key)
		}
	}
	h.warned[k] = now
	return true
}

// WithAuthz wires the OPA engine that narrows item types to the ones the
// caller may read, as the calendar does (calendar.ReadableTypes).
func (h *Handler) WithAuthz(e *authz.Engine) *Handler {
	h.engine = e
	return h
}

// WithModules wires the module views: items of a module the caller cannot
// see are neither listed nor counted.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// WithNow injects a clock for tests.
func (h *Handler) WithNow(fn func() time.Time) *Handler {
	h.nowFn = fn
	return h
}

// List handles GET /v1/needs-attention?for=me|all&type=&limit=&offset=.
//
// `type` is a comma-separated list of item types (ItemTypes); the page,
// total and band counts then cover those types only, while type_counts
// still counts every type in the view (#2135).
//
// With `for` omitted the view is "me", unless the caller owns nothing and
// may see everyone, in which case it is "all" (#2072 user story 4). The
// response's `for` says which view was served.
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
	q := r.URL.Query()
	view := q.Get("for")
	if view != "" && view != ForMe && view != ForAll {
		httpresp.WriteError(w, http.StatusBadRequest, "for must be me or all")
		return
	}
	everyone := authz.IsTenantApprover(cred)
	if view == ForAll && !everyone {
		httpresp.WriteError(w, http.StatusForbidden, "for=all is limited to admins and GRC leads")
		return
	}
	limit, offset, err := parsePage(q.Get("limit"), q.Get("offset"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	types, err := parseTypes(q["type"])
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.pool == nil || h.calendar == nil || h.approvals == nil {
		httpresp.WriteError(w, http.StatusServiceUnavailable, "needs attention unavailable: no database")
		return
	}

	items, err := h.collect(r, cred, tenantID)
	if err != nil {
		moduleaccess.WriteError(w, r, "needs attention", err)
		return
	}
	mine := mineFor(cred)
	var ranked []Item
	switch view {
	case ForAll:
		ranked = Rank(items, ForAll, mine)
	case ForMe:
		ranked = Rank(items, ForMe, mine)
	default:
		view = ForMe
		ranked = Rank(items, ForMe, mine)
		if len(ranked) == 0 && everyone {
			view = ForAll
			ranked = Rank(items, ForAll, mine)
		}
	}
	resp := Page(FilterTypes(ranked, types), view, limit, offset)
	resp.TypeCounts = CountTypes(ranked)
	httpresp.WriteJSON(w, http.StatusOK, resp)
}

// parseTypes reads `?type=`: repeated, comma-separated, or both. Every
// value must be one of ItemTypes.
func parseTypes(values []string) ([]string, error) {
	var out []string
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if !slices.Contains(ItemTypes, t) {
				return nil, fmt.Errorf("type must be one of %s", strings.Join(ItemTypes, "|"))
			}
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

func parsePage(rawLimit, rawOffset string) (int, int, error) {
	limit, offset := DefaultLimit, 0
	if rawLimit != "" {
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n < 1 || n > MaxLimit {
			return 0, 0, fmt.Errorf("limit must be between 1 and %d", MaxLimit)
		}
		limit = n
	}
	if rawOffset != "" {
		n, err := strconv.Atoi(rawOffset)
		if err != nil || n < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = n
	}
	return limit, offset, nil
}

// mineFor reports whether an item is the caller's: they are its owner,
// they hold its owner role as a live role (authz.AckRoleLabels, the labels
// a policy's role-targeted reads match; a JWT role claim never counts), or
// it waits for their approval. A credential with no person behind it owns
// nothing.
func mineFor(cred credstore.Credential) func(Item) bool {
	user := credstore.MinterUserID(cred)
	roles := authz.AckRoleLabels(cred)
	return func(it Item) bool {
		switch {
		case it.Band == BandApproval:
			return true
		case it.Owner == nil:
			return false
		case it.Owner.UserID != "":
			return user != "" && it.Owner.UserID == user
		default:
			return it.Owner.Role != "" && slices.Contains(roles, it.Owner.Role)
		}
	}
}

// itemTypeRoutes names each item type by a route of the module that owns
// it, for the module views. Approval items are filtered by the approvals
// handler itself.
var itemTypeRoutes = map[string]string{
	"control":     "/v1/controls",
	"exception":   "/v1/exceptions",
	"policy":      "/v1/policies",
	"vendor":      "/v1/vendors",
	"risk":        "/v1/risks",
	"action_plan": "/v1/action-plans",
}

// visibleTypes is the set of item types the caller may read (OPA, as the
// calendar asks it) and whose module they may see. A module-access error
// fails the request rather than showing everything.
func (h *Handler) visibleTypes(r *http.Request) (map[string]bool, error) {
	filter, left := calendar.ReadableTypes(r, h.engine, calendarTypes)
	out := map[string]bool{}
	if !left {
		return out, nil
	}
	if filter == "" {
		filter = calendarTypes
	}
	viewer, err := h.views.For(r)
	if err != nil {
		return nil, fmt.Errorf("module access: %w", err)
	}
	if filter, left, err = calendar.VisibleTypes(viewer, filter); err != nil {
		return nil, fmt.Errorf("module access: %w", err)
	}
	if !left {
		return out, nil
	}
	if filter == "" {
		filter = calendarTypes
	}
	for _, t := range strings.Split(filter, ",") {
		if _, known := itemTypeRoutes[t]; known {
			out[t] = true
		}
	}
	return out, nil
}

// collect reads every band's items, before the view filter.
func (h *Handler) collect(r *http.Request, cred credstore.Credential, tenantID uuid.UUID) ([]Item, error) {
	ctx := r.Context()
	now := h.nowFn()
	visible, err := h.visibleTypes(r)
	if err != nil {
		return nil, err
	}

	var items []Item
	// Band 2, approvals.
	pending, err := h.approvals.Pending(r, cred, tenantID)
	if err != nil {
		return nil, err
	}
	caller := credstore.MinterUserID(cred)
	for _, p := range pending {
		items = append(items, approvalItem(p, caller))
	}

	// Bands 3 and 4, from the calendar.
	if filter := typesCSV(visible); filter != "" {
		end := duedate.UTCDay(now).AddDate(0, 0, DueSoonDays+1)
		rows, err := h.calendar.ListEvents(ctx, time.Unix(0, 0).UTC(), end, now, filter, math.MaxInt32)
		if err != nil {
			return nil, fmt.Errorf("calendar: %w", err)
		}
		h.warnLarge(ctx, tenantID, "calendar", []string{BandOverdue, BandDueSoon}, len(rows))
		items = append(items, DueItems(rows, now)...)
	}

	// Bands 3 and 4, the caller's own policy acknowledgements: the Policies
	// module and its acknowledgements feature both on (#2175).
	if visible["policy"] {
		on, err := h.views.SwitchedOnStrict(ctx, tenantID, "policy_acknowledgments")
		if err != nil {
			return nil, fmt.Errorf("module access: %w", err)
		}
		if on {
			ack, err := h.ackItems(r, cred)
			if err != nil {
				return nil, err
			}
			items = append(items, ack...)
		}
	}

	// Bands 1 and 5, and every item's owner.
	err = h.inTx(ctx, tenantID, func(tx pgx.Tx) error {
		q := dbx.New(tx)
		tid := pgtype.UUID{Bytes: tenantID, Valid: true}
		if visible["control"] {
			ctl, n, err := controlItems(ctx, tx, q, tid)
			if err != nil {
				return err
			}
			h.warnLarge(ctx, tenantID, "controls", []string{BandFailing, BandOwnershipGap}, n)
			items = append(items, ctl...)
		}
		if visible["risk"] {
			rows, err := q.ListRisks(ctx, tid)
			if err != nil {
				return fmt.Errorf("list risks: %w", err)
			}
			h.warnLarge(ctx, tenantID, "risks", []string{BandOwnershipGap}, len(rows))
			items = append(items, UnownedRisks(rows)...)
		}
		if err := resolveOwners(ctx, tx, tenantID, items); err != nil {
			return err
		}
		return nameOwners(ctx, tx, tenantID, items)
	})
	return items, err
}

// ackItems lists the policies waiting for the caller's acknowledgement:
// published, aimed at a role the caller holds live or at everyone (the
// targeting POST /v1/policies/{id}/acknowledge applies, #2580, #2728), with
// no fresh ack of the current version. A credential with no person behind
// it acknowledges nothing.
func (h *Handler) ackItems(r *http.Request, cred credstore.Credential) ([]Item, error) {
	caller := policyacks.CallerFromCred(cred)
	if caller.UserID == "" {
		return nil, nil
	}
	pending, err := policy.NewAckStore(h.pool).WithClock(h.nowFn).PendingForUser(r.Context(), caller)
	if err != nil {
		return nil, fmt.Errorf("pending acknowledgements: %w", err)
	}
	return AckItems(pending, caller.UserID, h.nowFn()), nil
}

// AckItems turns the caller's pending acknowledgements into items they
// own, banded and dated by policy.Pending.Due, the rule
// GET /v1/me/acknowledgments reports (#2736): a lapsed ack is overdue from
// the day it lapsed; a policy never acknowledged is due soon until
// policy.AckGraceDays after its effective date, and overdue after.
func AckItems(pending []policy.Pending, caller string, now time.Time) []Item {
	out := make([]Item, 0, len(pending))
	for _, p := range pending {
		id := p.PolicyID.String()
		it := Item{
			Type: TypePolicyAck, ID: id, Title: p.Title,
			Link: links["policy"] + id, Action: "Acknowledge",
			Owner: &Owner{UserID: caller},
		}
		due, state := p.Due(now)
		it.Band, it.DueAt = BandDueSoon, due
		if state == policy.AckOverdue {
			it.Band = BandOverdue
		}
		out = append(out, it)
	}
	return out
}

func typesCSV(visible map[string]bool) string {
	var out []string
	for _, t := range strings.Split(calendarTypes, ",") {
		if visible[t] {
			out = append(out, t)
		}
	}
	return strings.Join(out, ",")
}

func (h *Handler) inTx(ctx context.Context, tenantID uuid.UUID, fn func(pgx.Tx) error) error {
	ctx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// approvalItem is one approvals row as a band-2 item, linked where
// GET /v1/approvals' page links it. The caller is the one who must act.
func approvalItem(p approvals.Item, caller string) Item {
	it := Item{
		Band: BandApproval, Type: p.Type, ID: p.ID, Title: p.Title,
		Action: "Approve", Since: p.RequestedAt, Owner: &Owner{UserID: caller},
	}
	switch p.Type {
	case approvals.TypeException:
		it.Link = "/exceptions/" + p.ID
	case approvals.TypeRiskAcceptance:
		it.Link = "/risks/" + p.ID + "/acceptance/approve"
	case approvals.TypePolicy:
		it.Link = "/policies/" + p.ID
	case approvals.TypeFrameworkScope:
		it.Link = "/framework-scopes"
		if p.FrameworkVersionID != "" {
			it.Link += "/" + p.FrameworkVersionID
		}
	}
	return it
}

// links and dueActions are per item type.
var links = map[string]string{
	"control":     "/controls/",
	"exception":   "/exceptions/",
	"policy":      "/policies/",
	"vendor":      "/vendors/",
	"risk":        "/risks/",
	"action_plan": "/action-plans/",
}

var dueActions = map[string]string{
	"control":     "Attest",
	"exception":   "Review exception",
	"policy":      "Review policy",
	"vendor":      "Review vendor",
	"risk":        "Review risk",
	"action_plan": "Update plan",
}

// DueItems turns calendar rows into band-3 and band-4 items. A row is
// overdue exactly when the calendar marks it overdue; it is due soon when
// the calendar marks it due soon and it falls within DueSoonDays (a policy
// review's own due-soon window is longer). An exception's calendar status
// is its lifecycle state, so its expiry date is rated with duedate.Status,
// the calendar's rule for the other dated types. Vendor renewals are
// commercial dates, not reviews, and are left out.
func DueItems(rows []dbx.ListCalendarEventsRow, now time.Time) []Item {
	var out []Item
	for _, row := range rows {
		if !row.StartsAt.Valid || strings.HasPrefix(row.EventID, "renewal-") {
			continue
		}
		due := row.StartsAt.Time.UTC()
		status := row.Status
		if row.EventType == "exception" {
			status = duedate.Status(due, now, DueSoonDays)
		}
		var band string
		switch {
		case status == duedate.Overdue:
			band = BandOverdue
		case status == duedate.DueSoon && duedate.Status(due, now, DueSoonDays) != duedate.Upcoming:
			band = BandDueSoon
		default:
			continue
		}
		out = append(out, Item{
			Band: band, Type: row.EventType, ID: row.RelatedEntityID, Title: row.Title,
			Link: links[row.EventType] + row.RelatedEntityID, Action: dueActions[row.EventType],
			DueAt: &due,
		})
	}
	return out
}

// controlItems reads the in-scope controls through the controls list's
// own query (active, not superseded, worst state per control) and returns
// the failing ones (band 1) and the unowned ones (band 5), with the
// number of rows read.
func controlItems(ctx context.Context, tx pgx.Tx, q *dbx.Queries, tid pgtype.UUID) ([]Item, int, error) {
	rows, err := q.ListActiveControlsWithState(ctx, dbx.ListActiveControlsWithStateParams{
		TenantID: tid, SortKey: "default", RowLimit: math.MaxInt32,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list controls: %w", err)
	}
	var unowned []uuid.UUID
	for _, c := range rows {
		if c.LifecycleState == dbx.ControlLifecycleStateActive && !c.OwnerUserID.Valid {
			unowned = append(unowned, c.ID.Bytes)
		}
	}
	created, err := createdAt(ctx, tx, tid.Bytes, unowned)
	if err != nil {
		return nil, 0, err
	}
	return ControlItems(rows, created), len(rows), nil
}

// ControlItems maps the controls list's rows to band-1 and band-5 items.
// created holds the unowned controls' creation times.
func ControlItems(rows []dbx.ListActiveControlsWithStateRow, created map[uuid.UUID]time.Time) []Item {
	var out []Item
	for _, c := range rows {
		if c.LifecycleState != dbx.ControlLifecycleStateActive {
			continue
		}
		id := uuid.UUID(c.ID.Bytes).String()
		title := c.Title
		if c.ScfID != nil && *c.ScfID != "" {
			title = *c.ScfID + " — " + c.Title
		}
		var owner *Owner
		if c.OwnerUserID.Valid {
			owner = &Owner{UserID: uuid.UUID(c.OwnerUserID.Bytes).String()}
		}
		if c.StateResult != nil && *c.StateResult == dbx.EvidenceResultFail {
			out = append(out, Item{
				Band: BandFailing, Type: "control", ID: id, Title: title,
				Link: links["control"] + id, Action: "Investigate",
				Since: timePtr(c.StateEvaluatedAt), Owner: owner,
			})
		}
		if owner == nil {
			var since *time.Time
			if t, ok := created[c.ID.Bytes]; ok {
				since = &t
			}
			out = append(out, Item{
				Band: BandOwnershipGap, Type: "control", ID: id, Title: title,
				Link: links["control"] + id, Action: "Assign owner", Since: since,
			})
		}
	}
	return out
}

// UnownedRisks lists the open risks with no owner user as band-5 items.
func UnownedRisks(rows []dbx.Risk) []Item {
	var out []Item
	for _, r := range rows {
		if r.Status != dbx.RiskStatusOpen || r.OwnerUserID.Valid {
			continue
		}
		id := uuid.UUID(r.ID.Bytes).String()
		out = append(out, Item{
			Band: BandOwnershipGap, Type: "risk", ID: id, Title: r.Title,
			Link: links["risk"] + id, Action: "Assign owner", Since: timePtr(r.CreatedAt),
		})
	}
	return out
}

func createdAt(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]time.Time, error) {
	out := map[uuid.UUID]time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id, created_at FROM controls WHERE tenant_id = $1 AND id = ANY($2)`, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("control created_at: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id uuid.UUID
			at time.Time
		)
		if err := rows.Scan(&id, &at); err != nil {
			return nil, fmt.Errorf("scan control created_at: %w", err)
		}
		out[id] = at.UTC()
	}
	return out, rows.Err()
}

// ownerQueries reads each due-based item type's owner by record id: a
// person for every type but a policy, which is owned by a role. An
// exception's owner is its control's assigned owner, who renews or lets
// the waiver lapse. control_owner_assignments' primary key is
// (tenant_id, control_id), so a control, and an exception through its
// control, has at most one owner row (#2555).
var ownerQueries = map[string]string{
	"control": `SELECT control_id::text, owner_user_id::text FROM control_owner_assignments
	             WHERE tenant_id = $1 AND control_id = ANY($2::uuid[])`,
	"exception": `SELECT e.id::text, coa.owner_user_id::text FROM exceptions e
	              JOIN control_owner_assignments coa ON coa.tenant_id = e.tenant_id AND coa.control_id = e.control_id
	             WHERE e.tenant_id = $1 AND e.id = ANY($2::uuid[])`,
	"risk": `SELECT id::text, owner_user_id::text FROM risks
	          WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND owner_user_id IS NOT NULL`,
	"vendor": `SELECT id::text, owner_user_id::text FROM vendors
	            WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND owner_user_id IS NOT NULL`,
	"action_plan": `SELECT id::text, owner_id::text FROM action_plans
	                 WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND owner_id IS NOT NULL`,
	"policy": `SELECT id::text, owner_role FROM policies
	            WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND owner_role <> ''`,
}

// resolveOwners fills Owner on the due-based items.
func resolveOwners(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, items []Item) error {
	ids := map[string][]string{}
	for _, it := range items {
		if it.Band == BandOverdue || it.Band == BandDueSoon {
			ids[it.Type] = append(ids[it.Type], it.ID)
		}
	}
	found := map[string]map[string]string{}
	for typ, list := range ids {
		sql, ok := ownerQueries[typ]
		if !ok {
			continue
		}
		m, err := pairs(ctx, tx, sql, tenantID, list)
		if err != nil {
			return fmt.Errorf("%s owners: %w", typ, err)
		}
		found[typ] = m
	}
	for i := range items {
		it := &items[i]
		if it.Band != BandOverdue && it.Band != BandDueSoon {
			continue
		}
		v, ok := found[it.Type][it.ID]
		switch {
		case !ok:
		case it.Type == "policy":
			it.Owner = &Owner{Role: v}
		default:
			it.Owner = &Owner{UserID: v}
		}
	}
	return nil
}

// nameOwners fills each person-owner's display name.
func nameOwners(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, items []Item) error {
	var ids []string
	for _, it := range items {
		if it.Owner != nil && it.Owner.UserID != "" {
			ids = append(ids, it.Owner.UserID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	names, err := pairs(ctx, tx,
		`SELECT id::text, display_name FROM users WHERE tenant_id = $1 AND id = ANY($2::uuid[])`, tenantID, ids)
	if err != nil {
		return fmt.Errorf("owner names: %w", err)
	}
	for i := range items {
		if o := items[i].Owner; o != nil && o.UserID != "" {
			o.Name = names[o.UserID]
		}
	}
	return nil
}

// pairs runs a two-text-column lookup keyed by the first column. ids that
// are not UUIDs (a credential that names no user) are skipped.
func pairs(ctx context.Context, tx pgx.Tx, sql string, tenantID uuid.UUID, ids []string) (map[string]string, error) {
	var valid []uuid.UUID
	for _, s := range ids {
		if u, err := uuid.Parse(s); err == nil {
			valid = append(valid, u)
		}
	}
	out := map[string]string{}
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, sql, tenantID, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}
