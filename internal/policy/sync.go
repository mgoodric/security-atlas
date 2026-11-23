package policy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Sync outcomes, one per page, reported by Store.Sync.
const (
	// SyncStatusCreated: the page had no synced policy and one was
	// published.
	SyncStatusCreated = "created"
	// SyncStatusNewVersion: the page's declared version changed. The
	// next version was published, chained to the page's previous synced
	// row and superseding it (#2851).
	SyncStatusNewVersion = "new_version"
	// SyncStatusUnchanged: the page's synced policy already has this
	// version, body and properties; nothing was written. Drift recorded
	// by an earlier run is cleared.
	SyncStatusUnchanged = "unchanged"
	// SyncStatusDrift: the page's body (#2851), its properties (#2877),
	// or both changed without a version bump. No version was written and
	// nothing was applied in place; the drift is recorded on the policy,
	// body and property drift separately.
	SyncStatusDrift = "drift"
	// SyncStatusTakenOver: the page's first sync found a published,
	// imported policy with its title, and took it over instead of starting
	// a second history (#2854). With the same version the imported row
	// became the page's synced row in place; with another version the
	// synced version was published as its next version, superseding it.
	SyncStatusTakenOver = "taken_over"
	// SyncStatusSkipped: the page was not synced; Reason says why.
	SyncStatusSkipped = "skipped"
	// SyncStatusMissing: a page with a published synced policy is absent
	// from the run, so it lost its label or was deleted (#2853). Nothing
	// is written to the policy beyond recording when it was first found
	// missing; it stays published and acknowledgements continue.
	SyncStatusMissing = "missing"
	// SyncStatusUnreadable: the run carried the page as unreadable
	// (#3000): it is still labelled, but the connector could not read its
	// body. The page is present, so it is not missing. Nothing about its
	// policy changes beyond recording the state and the pusher's reason;
	// a page with no synced policy yet gets none.
	SyncStatusUnreadable = "unreadable"
)

// ErrSyncEmptyRefused is returned by Sync for a push with no pages for a
// source that has published synced policies, unless the caller allows
// it (#2902): a connector bug that sends nothing would otherwise mark
// every policy of the source missing.
var ErrSyncEmptyRefused = errors.New("sync: an empty push for a source with published synced policies; set allow_empty to confirm the source is now empty")

// syncMissingReason is the report's reason on a missing result.
const syncMissingReason = "the page is no longer in this source's labelled set: it lost its label or was deleted. The published policy stays in force; restore the label to clear this"

// auditActionSynced is the policy_audit_log action for a synced write.
const auditActionSynced = "synced"

// auditActionSyncTakeover is the policy_audit_log action written on an
// imported policy a page took over (#2854).
const auditActionSyncTakeover = "sync_takeover"

// SyncSource names where a sync run's pages come from: one Confluence site
// and space. A page is identified by its id within its source.
type SyncSource struct {
	Site  string
	Space string
}

// SyncPage is one valid page of a sync run. In carries the page's title,
// properties, Markdown body and URL, validated with the import's rules
// (importfile.FromPage). In.LinkedControlRefs is ignored: control links
// are kept in security-atlas, not synced.
type SyncPage struct {
	PageID string
	In     ImportInput
}

// SyncUnreadablePage is one page a sync run carries as unreadable
// (#3000): the pusher could not read its body, so it sends the page's id
// and a reason instead. Reason is already trimmed and bounded by the
// caller.
type SyncUnreadablePage struct {
	PageID string
	Title  string
	Reason string
}

// syncUnreadableNewReason is appended to an unreadable result's reason
// for a page that has no synced policy yet.
const syncUnreadableNewReason = "no policy exists for this page yet, so none was created; it syncs once its body can be read"

// SyncResult is the outcome for one page.
type SyncResult struct {
	PageID string
	Title  string
	Status string
	Reason string
	Policy *Policy // the page's synced policy, unless Status is skipped
}

// SyncReport is the outcome of one Sync call, one result per page in the
// order given (skipped pages first, as the caller passes them), then one
// missing result per previously synced page absent from the run, by page
// id.
type SyncReport struct {
	Results []SyncResult
	// Warnings are run-level notes, such as a run that leaves more than
	// half of the source's policies missing (#2902).
	Warnings []string
}

// Sync applies one full-set push from a policy-sync key (#2850). Every
// page with no synced policy in this source becomes a published policy
// with source_attribution 'synced', the page's approver and approval date
// as its historical approver, the page URL as its source URL and the
// body's sha256, plus one 'synced' policy_audit_log row naming the key.
// A page whose synced policy has the same version and body is unchanged.
// A page whose declared version changed becomes the next version of its
// policy, superseding the previous synced row; a page whose body changed
// without a version bump writes no version and records drift on the
// policy instead (#2851), and so does a page whose properties changed
// without one, naming the properties (#2877). Each drift clears when a
// later run's body or properties match again, or the page arrives with a
// new version.
//
// A page's first sync takes over a published, imported policy with the
// page's title (#2854), so an adopter who imported before syncing keeps
// one history per policy: the same version is taken over in place,
// another version is published as its next version. A policy authored in
// security-atlas is never taken over; the page starts a new policy and the
// result's Reason says so. Each takeover writes a 'sync_takeover' audit
// row on the imported policy naming its previous attribution and the key.
//
// The push is the source's full set, so a page with a published synced
// policy that the run does not carry is missing (#2853): the row records
// when it was first found missing, the report lists it, and the policy
// is otherwise left as it is. A page the run carries, even one skipped,
// is present, and any missing state on it is cleared. Only rows of this
// source in the caller's tenant are considered.
//
// A push that carries no page, valid or invalid, for a source that has
// published synced policies is refused with ErrSyncEmptyRefused and
// writes nothing, unless allowEmpty says the source is now empty
// (#2902). A run that leaves more than half of the source's published
// synced policies missing carries a warning in the report.
//
// unreadable are pages the run carries without a body because the pusher
// could not read it (#3000). Each is present, so its missing state is
// cleared, and its published synced row records the reason and when the
// page was first carried unreadable; its body, hash, properties, drift
// and version are left alone. A page with no synced policy yet is
// reported and gets none. Every row of the source whose page the run
// does not carry unreadable loses any unreadable state. A page id carried
// both readable and unreadable, or unreadable twice, is synced readable
// (or once) and the extra entry skipped.
//
// invalid are pages the caller refused (a missing or invalid property);
// they are reported as skipped and written nowhere. Unlike Import, valid
// pages are written even when another page is skipped. Runs for one source
// are serialised, so an overlapping run cannot create a page twice.
// Everything runs in one transaction.
func (s *Store) Sync(ctx context.Context, src SyncSource, pages []SyncPage, unreadable []SyncUnreadablePage, invalid []SyncResult, allowEmpty bool, keyID string) (SyncReport, error) {
	if keyID == "" {
		return SyncReport{}, ErrCreatedByRequired
	}
	report := SyncReport{}
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		if err := q.LockPolicySyncSource(ctx, dbx.LockPolicySyncSourceParams{
			TenantID:  tenantID.String(),
			SyncSite:  src.Site,
			SyncSpace: src.Space,
		}); err != nil {
			return fmt.Errorf("sync: lock source: %w", err)
		}
		synced, err := q.CountPublishedSyncedPoliciesInSource(ctx, dbx.CountPublishedSyncedPoliciesInSourceParams{
			TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space,
		})
		if err != nil {
			return fmt.Errorf("sync: count source policies: %w", err)
		}
		if len(pages) == 0 && len(unreadable) == 0 && len(invalid) == 0 && synced > 0 && !allowEmpty {
			return ErrSyncEmptyRefused
		}
		// Every page the run carries is present, valid or not: a page
		// with a bad property is still labelled.
		present := []string{}
		for _, r := range invalid {
			present = append(present, r.PageID)
		}
		readable := map[string]bool{}
		for _, p := range pages {
			present = append(present, p.PageID)
			readable[p.PageID] = true
		}
		// The unreadable pages that apply: not also carried readable, and
		// each once. Every other row of the source loses its unreadable
		// state before the readable pages are synced.
		var unreadableIDs []string
		for _, u := range unreadable {
			present = append(present, u.PageID)
			if !readable[u.PageID] && !slices.Contains(unreadableIDs, u.PageID) {
				unreadableIDs = append(unreadableIDs, u.PageID)
			}
		}
		if err := q.ClearSyncedPolicyMissing(ctx, dbx.ClearSyncedPolicyMissingParams{
			TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space, PresentPageIds: present,
		}); err != nil {
			return fmt.Errorf("sync: clear missing: %w", err)
		}
		if err := q.ClearSyncedPoliciesUnreadable(ctx, dbx.ClearSyncedPoliciesUnreadableParams{
			TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space, UnreadablePageIds: append([]string{}, unreadableIDs...),
		}); err != nil {
			return fmt.Errorf("sync: clear unreadable: %w", err)
		}
		results := append([]SyncResult{}, invalid...)
		seen := map[string]bool{}
		// The page ids of the run by title: a takeover needs the title to
		// name one page (#2854).
		byTitle := map[string][]string{}
		for _, p := range pages {
			if !slices.Contains(byTitle[p.In.Title], p.PageID) {
				byTitle[p.In.Title] = append(byTitle[p.In.Title], p.PageID)
			}
		}
		for _, p := range pages {
			res := SyncResult{PageID: p.PageID, Title: p.In.Title}
			if err := validateSyncPage(p); err != nil {
				res.Status, res.Reason = SyncStatusSkipped, err.Error()
				results = append(results, res)
				continue
			}
			if seen[p.PageID] {
				res.Status, res.Reason = SyncStatusSkipped, fmt.Sprintf("page %q appears more than once in this push", p.PageID)
				results = append(results, res)
				continue
			}
			seen[p.PageID] = true
			hash := bodySHA256(p.In.BodyMd)
			cur, err := q.GetSyncedPolicyByPage(ctx, dbx.GetSyncedPolicyByPageParams{
				TenantID:   pgUUID(tenantID),
				SyncSite:   &src.Site,
				SyncSpace:  &src.Space,
				SyncPageID: &p.PageID,
			})
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if res, err = syncFirst(ctx, q, tenantID, src, p, hash, keyID, byTitle[p.In.Title]); err != nil {
					return err
				}
			case err != nil:
				return fmt.Errorf("sync: look up page %q: %w", p.PageID, err)
			case cur.Version != p.In.Version:
				// The chain is linear: a row has at most one successor. An
				// in-tool draft of the next version already holding that
				// slot skips this page instead of failing the run.
				lock, err := q.LockSyncedPolicyForSuccessor(ctx, dbx.LockSyncedPolicyForSuccessorParams{
					TenantID: pgUUID(tenantID), ID: cur.ID,
				})
				if err != nil {
					return fmt.Errorf("sync: lock page %q: %w", p.PageID, err)
				}
				if lock.SuccessorID.Valid {
					existing := policyFromRow(cur)
					status := ""
					if lock.SuccessorStatus != nil {
						status = *lock.SuccessorStatus
					}
					res.Status, res.Policy = SyncStatusSkipped, &existing
					res.Reason = fmt.Sprintf("version %q already has a next version in security-atlas (policy %s, %s); discard it before version %q can sync",
						cur.Version, uuid.UUID(lock.SuccessorID.Bytes), status, p.In.Version)
					break
				}
				written, err := syncNewVersion(ctx, q, tenantID, src, p, cur, hash, keyID)
				if err != nil {
					return err
				}
				res.Status, res.Policy = SyncStatusNewVersion, &written
			default:
				// Same version: the body and properties are compared with
				// the synced row, and what differs is recorded as drift.
				if res, err = syncSameVersion(ctx, q, tenantID, p, cur, hash); err != nil {
					return err
				}
			}
			results = append(results, res)
		}
		for _, u := range unreadable {
			res := SyncResult{PageID: u.PageID, Title: u.Title}
			if seen[u.PageID] || readable[u.PageID] {
				res.Status, res.Reason = SyncStatusSkipped, fmt.Sprintf("page %q appears more than once in this push", u.PageID)
				results = append(results, res)
				continue
			}
			seen[u.PageID] = true
			res, err := syncUnreadable(ctx, q, tenantID, src, u)
			if err != nil {
				return err
			}
			results = append(results, res)
		}
		missing, err := syncMarkMissing(ctx, q, tenantID, src, present)
		if err != nil {
			return err
		}
		report.Results = append(results, missing...)
		if int64(len(missing))*2 > synced {
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"%d of this source's %d published synced policies are missing from this push. If those pages were not deleted or unlabelled, check the connector",
				len(missing), synced))
		}
		return nil
	})
	if err != nil {
		return SyncReport{}, err
	}
	return report, nil
}

// syncUnreadable records one unreadable page (#3000) on its published
// synced row: the pusher's reason, and when the page was first carried
// unreadable. Nothing else is written. A page with no synced policy is
// reported and gets none: there is no body to publish.
func syncUnreadable(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, u SyncUnreadablePage) (SyncResult, error) {
	res := SyncResult{PageID: u.PageID, Title: u.Title, Status: SyncStatusUnreadable, Reason: u.Reason}
	cur, err := q.GetSyncedPolicyByPage(ctx, dbx.GetSyncedPolicyByPageParams{
		TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space, SyncPageID: &u.PageID,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		res.Reason = u.Reason + "; " + syncUnreadableNewReason
		return res, nil
	case err != nil:
		return res, fmt.Errorf("sync: look up page %q: %w", u.PageID, err)
	}
	row := cur
	// No row back means this reason is already recorded.
	marked, err := q.SetSyncedPolicyUnreadable(ctx, dbx.SetSyncedPolicyUnreadableParams{
		TenantID: pgUUID(tenantID), ID: cur.ID, SyncUnreadableReason: &u.Reason,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return res, fmt.Errorf("sync: record unreadable page %q: %w", u.PageID, err)
	default:
		row = marked
	}
	pol := policyFromRow(row)
	res.Policy = &pol
	if res.Title == "" {
		res.Title = row.Title
	}
	return res, nil
}

// syncSameVersion handles a page whose synced row already has its
// declared version. The body and the synced properties are compared with
// the row: body drift (#2851) and property drift (#2877) are each
// recorded when they differ and cleared when they match. The body
// comparison ignores whitespace (#2910); the hashes recorded are of the
// body as sent. Nothing else is written, since what staff acknowledged is
// not rewritten under them.
func syncSameVersion(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, p SyncPage, cur dbx.Policy, hash string) (SyncResult, error) {
	res := SyncResult{PageID: p.PageID, Title: p.In.Title}
	row := cur
	bodyDrift := bodyDrifted(cur.BodyMd, p.In.BodyMd)
	switch {
	case bodyDrift:
		// No row back means this drift is already recorded.
		drifted, err := q.SetSyncedPolicyDrift(ctx, dbx.SetSyncedPolicyDriftParams{
			TenantID: pgUUID(tenantID), ID: cur.ID, SyncDriftBodySha256: &hash,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return res, fmt.Errorf("sync: record drift on page %q: %w", p.PageID, err)
		default:
			row = drifted
		}
	case cur.SyncDriftDetectedAt.Valid:
		cleared, err := q.ClearSyncedPolicyDrift(ctx, dbx.ClearSyncedPolicyDriftParams{
			TenantID: pgUUID(tenantID), ID: cur.ID,
		})
		if err != nil {
			return res, fmt.Errorf("sync: clear drift on page %q: %w", p.PageID, err)
		}
		row = cleared
	}
	// Nil keys clear the property drift; no row back means the row
	// already holds these keys (or none).
	keys := syncPropertyDrift(cur, p.In)
	updated, err := q.SetSyncedPolicyPropertyDrift(ctx, dbx.SetSyncedPolicyPropertyDriftParams{
		TenantID: pgUUID(tenantID), ID: cur.ID, SyncPropertyDriftKeys: keys,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return res, fmt.Errorf("sync: record property drift on page %q: %w", p.PageID, err)
	default:
		row = updated
	}
	pol := policyFromRow(row)
	res.Policy = &pol
	if !bodyDrift && len(keys) == 0 {
		res.Status = SyncStatusUnchanged
		return res, nil
	}
	res.Status = SyncStatusDrift
	res.Reason = fmt.Sprintf("the page %s changed without a version bump; version %q is unchanged. Bump the page's version to publish the change, or revert the edit",
		syncDriftWhat(bodyDrift, keys), cur.Version)
	return res, nil
}

// syncDriftWhat names what drifted on a page, for a report reason.
func syncDriftWhat(body bool, keys []string) string {
	props := ""
	if len(keys) > 0 {
		noun := "property"
		if len(keys) > 1 {
			noun = "properties"
		}
		props = noun + " " + strings.Join(keys, ", ")
	}
	switch {
	case body && props != "":
		return "body and " + props
	case body:
		return "body"
	default:
		return props
	}
}

// Page property names compared by syncPropertyDrift, as the sync request
// names them. version is not among them: a version change is a bump.
// source_url is not either: it is the page's own address, not a property
// of the policy its staff acknowledged.
const (
	syncPropTitle         = "title"
	syncPropApprovedBy    = "approved_by_name"
	syncPropApprovedAt    = "approved_at"
	syncPropEffectiveDate = "effective_date"
	syncPropNextReviewAt  = "next_review_at"
	syncPropOwnerRole     = "owner_role"
	syncPropApproverRole  = "approver_role"
	syncPropAckRoles      = "acknowledgment_required_roles"
)

// syncPropertyDrift returns the sorted names of the page properties in
// that differ from row's, or nil when none do (#2877). The comparison is
// on the values a synced insert would store, so a page synced unchanged
// never reports drift: dates by calendar day, the acknowledgement
// audience as a set.
func syncPropertyDrift(row dbx.Policy, in ImportInput) []string {
	var keys []string
	differ := func(key string, same bool) {
		if !same {
			keys = append(keys, key)
		}
	}
	day := func(d pgtype.Date) string {
		if !d.Valid {
			return ""
		}
		return d.Time.Format("2006-01-02")
	}
	approver := ""
	if row.HistoricalApproverName != nil {
		approver = *row.HistoricalApproverName
	}
	nextReview := false
	switch {
	case !row.NextReviewAt.Valid && in.NextReviewAt == nil:
		nextReview = true
	case row.NextReviewAt.Valid && in.NextReviewAt != nil:
		nextReview = row.NextReviewAt.Time.Equal(*in.NextReviewAt)
	}
	differ(syncPropTitle, row.Title == in.Title)
	differ(syncPropApprovedBy, approver == in.HistoricalApproverName)
	differ(syncPropApprovedAt, day(row.HistoricalApprovedAt) == day(pgDate(in.HistoricalApprovedAt)))
	differ(syncPropEffectiveDate, day(row.EffectiveDate) == day(pgDate(in.EffectiveDate)))
	differ(syncPropNextReviewAt, nextReview)
	differ(syncPropOwnerRole, row.OwnerRole == in.OwnerRole)
	differ(syncPropApproverRole, row.ApproverRole == in.ApproverRole)
	differ(syncPropAckRoles, sameRoleSet(row.AcknowledgmentRequiredRoles, in.AcknowledgmentRequiredRoles))
	slices.Sort(keys)
	return keys
}

func sameRoleSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// syncMarkMissing records every published synced row of src whose page
// is not in present as missing, keeping the first-sighting time of a row
// already marked, and returns one missing result per such row.
func syncMarkMissing(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, present []string) ([]SyncResult, error) {
	if err := q.MarkSyncedPoliciesMissing(ctx, dbx.MarkSyncedPoliciesMissingParams{
		TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space, PresentPageIds: present,
	}); err != nil {
		return nil, fmt.Errorf("sync: mark missing: %w", err)
	}
	rows, err := q.ListSyncedPoliciesMissingFromSource(ctx, dbx.ListSyncedPoliciesMissingFromSourceParams{
		TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space,
	})
	if err != nil {
		return nil, fmt.Errorf("sync: list missing: %w", err)
	}
	out := make([]SyncResult, 0, len(rows))
	for _, row := range rows {
		pol := policyFromRow(row)
		pageID := ""
		if row.SyncPageID != nil {
			pageID = *row.SyncPageID
		}
		out = append(out, SyncResult{
			PageID: pageID, Title: row.Title, Status: SyncStatusMissing,
			Reason: syncMissingReason, Policy: &pol,
		})
	}
	return out, nil
}

// syncFirst handles a page with no synced policy in its source: it takes
// over the tenant's published imported policy with the page's title, if
// there is exactly one, or else publishes the page as a new policy
// (#2854). A title that matches several imported policies, or that another
// page in the run shares while it matches one, skips the page instead.
//
// sameTitle is every page id in the run with p's title, p's own included.
func syncFirst(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, p SyncPage, hash, keyID string, sameTitle []string) (SyncResult, error) {
	res := SyncResult{PageID: p.PageID, Title: p.In.Title}
	candidates, err := q.ListImportedPoliciesForTakeover(ctx, dbx.ListImportedPoliciesForTakeoverParams{
		TenantID: pgUUID(tenantID), Title: p.In.Title,
	})
	if err != nil {
		return res, fmt.Errorf("sync: look up imported policies for page %q: %w", p.PageID, err)
	}
	// An audit tool refuses rather than chooses: with more than one
	// candidate policy, or more than one page that would claim the one,
	// the page is skipped and written nowhere. A later run takes it over
	// once the title is unambiguous.
	switch {
	case len(candidates) > 1:
		ids := make([]string, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, fmt.Sprintf("%s (version %q)", uuid.UUID(c.ID.Bytes), c.Version))
		}
		res.Status = SyncStatusSkipped
		res.Reason = fmt.Sprintf("ambiguous title: %d published imported policies are titled %q: %s. Retitle all but one, or retitle this page, before it can sync",
			len(candidates), p.In.Title, strings.Join(ids, ", "))
		return res, nil
	case len(candidates) == 1 && len(sameTitle) > 1:
		res.Status = SyncStatusSkipped
		res.Reason = fmt.Sprintf("ambiguous title: pages %s in this push are all titled %q, which matches imported policy %s. Give each page a distinct title before one can take it over",
			strings.Join(sameTitle, ", "), p.In.Title, uuid.UUID(candidates[0].ID.Bytes))
		return res, nil
	}
	switch {
	case len(candidates) == 0:
	case candidates[0].Version == p.In.Version:
		imported := candidates[0]
		taken, err := syncTakeOverInPlace(ctx, q, tenantID, src, p, imported, hash, keyID)
		if err != nil {
			return res, err
		}
		res.Status, res.Policy = SyncStatusTakenOver, &taken
		res.Reason = fmt.Sprintf("took over imported policy %s (version %q) in place; it is now managed by this page", uuid.UUID(imported.ID.Bytes), imported.Version)
		body, keys := taken.SyncDriftDetectedAt != nil, taken.SyncPropertyDriftKeys
		if body || len(keys) > 0 {
			res.Reason += fmt.Sprintf(". The page %s differs from the imported policy, so it is reported as drift; bump the page's version to publish it",
				syncDriftWhat(body, keys))
		}
		return res, nil
	default:
		imported := candidates[0]
		// The chain is linear, as for a synced version bump: an in-tool
		// draft of the next version already holding the imported row's
		// successor slot skips this page.
		lock, err := q.LockSyncedPolicyForSuccessor(ctx, dbx.LockSyncedPolicyForSuccessorParams{
			TenantID: pgUUID(tenantID), ID: imported.ID,
		})
		if err != nil {
			return res, fmt.Errorf("sync: lock imported policy for page %q: %w", p.PageID, err)
		}
		if lock.SuccessorID.Valid {
			existing := policyFromRow(imported)
			status := ""
			if lock.SuccessorStatus != nil {
				status = *lock.SuccessorStatus
			}
			res.Status, res.Policy = SyncStatusSkipped, &existing
			res.Reason = fmt.Sprintf("imported policy %s (version %q) already has a next version in security-atlas (policy %s, %s); discard it before version %q can take it over",
				uuid.UUID(imported.ID.Bytes), imported.Version, uuid.UUID(lock.SuccessorID.Bytes), status, p.In.Version)
			return res, nil
		}
		written, err := syncTakeOverAsNextVersion(ctx, q, tenantID, src, p, imported, hash, keyID)
		if err != nil {
			return res, err
		}
		res.Status, res.Policy = SyncStatusTakenOver, &written
		res.Reason = fmt.Sprintf("took over imported policy %s (version %q); version %q is its next version and supersedes it", uuid.UUID(imported.ID.Bytes), imported.Version, p.In.Version)
		return res, nil
	}

	written, err := syncCreate(ctx, q, tenantID, src, p, hash, keyID)
	if err != nil {
		return res, err
	}
	res.Status, res.Policy = SyncStatusCreated, &written
	authored, err := q.GetAuthoredPolicyByTitle(ctx, dbx.GetAuthoredPolicyByTitleParams{
		TenantID: pgUUID(tenantID), Title: p.In.Title,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return res, fmt.Errorf("sync: look up authored policy for page %q: %w", p.PageID, err)
	default:
		res.Reason = fmt.Sprintf("policy %s (%s) has this title but was authored in security-atlas, so it was not taken over; this page started a new policy",
			uuid.UUID(authored.ID.Bytes), authored.Status)
	}
	return res, nil
}

// syncTakeOverInPlace makes the imported row the page's synced row: same
// id, version and body, so acknowledgements and control links stay on
// it. A page body that differs from the imported text is recorded as
// drift, since what staff acknowledged is not rewritten.
func syncTakeOverInPlace(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, p SyncPage, imported dbx.Policy, hash, keyID string) (Policy, error) {
	rowHash := bodySHA256(imported.BodyMd)
	var drift *string
	if bodyDrifted(imported.BodyMd, p.In.BodyMd) {
		drift = &hash
	}
	// The imported row keeps its properties too; page properties that
	// differ are property drift (#2877).
	propDrift := syncPropertyDrift(imported, p.In)
	row, err := q.TakeOverImportedPolicy(ctx, dbx.TakeOverImportedPolicyParams{
		TenantID:              pgUUID(tenantID),
		ID:                    imported.ID,
		SourceUrl:             stringPtr(p.In.SourceURL),
		SyncSite:              &src.Site,
		SyncSpace:             &src.Space,
		SyncPageID:            &p.PageID,
		SyncedBodySha256:      &rowHash,
		SyncedBy:              &keyID,
		SyncDriftBodySha256:   drift,
		SyncPropertyDriftKeys: propDrift,
	})
	if err != nil {
		return Policy{}, fmt.Errorf("sync: take over imported policy for page %q: %w", p.PageID, err)
	}
	if err := syncAuditTakeover(ctx, q, tenantID, p, row, imported.SourceAttribution, keyID, takeoverAudit{bodyHash: &rowHash, driftHash: drift}); err != nil {
		return Policy{}, err
	}
	return policyFromRow(row), nil
}

// syncTakeOverAsNextVersion supersedes the imported row and publishes the
// page's version as its next version, carrying its control links, as a
// synced version bump does.
func syncTakeOverAsNextVersion(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, p SyncPage, imported dbx.Policy, hash, keyID string) (Policy, error) {
	superseded, err := q.SupersedeImportedPolicy(ctx, dbx.SupersedeImportedPolicyParams{
		TenantID: pgUUID(tenantID), ID: imported.ID,
	})
	if err != nil {
		return Policy{}, fmt.Errorf("sync: supersede imported policy for page %q: %w", p.PageID, err)
	}
	links := imported.LinkedControlIds
	if links == nil {
		links = []pgtype.UUID{}
	}
	written, err := syncInsert(ctx, q, tenantID, src, p, hash, keyID, imported.ID, links)
	if err != nil {
		return Policy{}, err
	}
	if err := syncAuditTakeover(ctx, q, tenantID, p, superseded, imported.SourceAttribution, keyID, takeoverAudit{
		previousState: imported.Status, newState: superseded.Status,
	}); err != nil {
		return Policy{}, err
	}
	return written, nil
}

// syncAuditTakeover writes the 'sync_takeover' row on the taken-over
// policy: the key as actor, the page id as source file, the page URL, and
// the attribution the policy had before.
func syncAuditTakeover(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, p SyncPage, row dbx.Policy, prevAttribution, keyID string, a takeoverAudit) error {
	if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
		TenantID:                  pgUUID(tenantID),
		PolicyID:                  row.ID,
		Action:                    auditActionSyncTakeover,
		Actor:                     keyID,
		SourceFile:                p.PageID,
		HistoricalApproverName:    row.HistoricalApproverName,
		HistoricalApprovedAt:      row.HistoricalApprovedAt,
		SourceUrl:                 stringPtr(p.In.SourceURL),
		PolicyTitle:               &row.Title,
		PolicyVersion:             &row.Version,
		BodySha256:                a.bodyHash,
		PreviousState:             stringPtr(a.previousState),
		NewState:                  stringPtr(a.newState),
		PreviousSourceAttribution: &prevAttribution,
		SyncDriftBodySha256:       a.driftHash,
	}); err != nil {
		return fmt.Errorf("sync: takeover audit log: %w", err)
	}
	return nil
}

// takeoverAudit is what differs between the two takeover shapes' audit
// rows (#2910). In place: the row's own body hash, and the page's hash
// when the page drifted from it. Chained: the imported row's status before
// and after it was superseded.
type takeoverAudit struct {
	bodyHash, driftHash     *string
	previousState, newState string
}

func syncCreate(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, p SyncPage, hash, keyID string) (Policy, error) {
	return syncInsert(ctx, q, tenantID, src, p, hash, keyID, pgtype.UUID{}, []pgtype.UUID{})
}

// syncNewVersion publishes the page's new declared version as the next
// row of its chain (#2851): cur, the page's published synced row, is
// superseded first (one published synced row per page), then the new row
// is inserted with cur as its predecessor and cur's control links.
// Acknowledgements belong to a row, so staff are asked to acknowledge the
// new version afresh.
func syncNewVersion(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, p SyncPage, cur dbx.Policy, hash, keyID string) (Policy, error) {
	if _, err := q.SupersedeSyncedPolicy(ctx, dbx.SupersedeSyncedPolicyParams{
		TenantID: pgUUID(tenantID), ID: cur.ID,
	}); err != nil {
		return Policy{}, fmt.Errorf("sync: supersede page %q: %w", p.PageID, err)
	}
	links := cur.LinkedControlIds
	if links == nil {
		links = []pgtype.UUID{}
	}
	return syncInsert(ctx, q, tenantID, src, p, hash, keyID, cur.ID, links)
}

func syncInsert(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, src SyncSource, p SyncPage, hash, keyID string, predecessor pgtype.UUID, links []pgtype.UUID) (Policy, error) {
	in := p.In
	ackRoles := in.AcknowledgmentRequiredRoles
	if ackRoles == nil {
		ackRoles = []string{}
	}
	nextReview := pgtype.Timestamptz{}
	if in.NextReviewAt != nil {
		nextReview = pgtype.Timestamptz{Time: in.NextReviewAt.UTC(), Valid: true}
	}
	row, err := q.InsertSyncedPolicy(ctx, dbx.InsertSyncedPolicyParams{
		ID:                          pgUUID(uuid.New()),
		TenantID:                    pgUUID(tenantID),
		PredecessorID:               predecessor,
		LinkedControlIds:            links,
		Title:                       in.Title,
		Version:                     in.Version,
		BodyMd:                      in.BodyMd,
		OwnerRole:                   in.OwnerRole,
		ApproverRole:                in.ApproverRole,
		AcknowledgmentRequiredRoles: ackRoles,
		EffectiveDate:               pgDate(in.EffectiveDate),
		NextReviewAt:                nextReview,
		SyncedBy:                    keyID,
		HistoricalApproverName:      stringPtr(in.HistoricalApproverName),
		HistoricalApprovedAt:        pgDate(in.HistoricalApprovedAt),
		SourceUrl:                   stringPtr(in.SourceURL),
		SyncSite:                    &src.Site,
		SyncSpace:                   &src.Space,
		SyncPageID:                  &p.PageID,
		SyncedBodySha256:            &hash,
	})
	if err != nil {
		return Policy{}, mapCreateError(err)
	}
	if _, err := q.InsertPolicyAuditLog(ctx, dbx.InsertPolicyAuditLogParams{
		TenantID:               pgUUID(tenantID),
		PolicyID:               row.ID,
		Action:                 auditActionSynced,
		Actor:                  keyID,
		SourceFile:             p.PageID,
		HistoricalApproverName: stringPtr(in.HistoricalApproverName),
		HistoricalApprovedAt:   pgDate(in.HistoricalApprovedAt),
		SourceUrl:              stringPtr(in.SourceURL),
		PolicyTitle:            &row.Title,
		PolicyVersion:          &row.Version,
		BodySha256:             &hash,
	}); err != nil {
		return Policy{}, fmt.Errorf("sync: audit log: %w", err)
	}
	return policyFromRow(row), nil
}

// validateSyncPage re-checks what the handler guarantees, as validateImport
// does for Import, plus what a synced row needs beyond an imported one.
func validateSyncPage(p SyncPage) error {
	switch {
	case p.PageID == "":
		return errors.New("policy: page id is required")
	case len(p.PageID) > MaxImportFieldLen:
		return fmt.Errorf("policy: page id is longer than %d characters", MaxImportFieldLen)
	case p.In.SourceURL == "":
		return errors.New("policy: page url is required")
	}
	return validateImport(p.In)
}

// normalizeDriftBody is the form a body is compared in for sync drift
// (#2910): line endings as \n, trailing whitespace stripped from every
// line, runs of blank lines collapsed to one, and trailing newlines
// trimmed. The connector's Confluence-to-Markdown conversion differs from
// an export in exactly these ways, and none of them changes the content.
// Two Markdown cases are left alone because they do change what renders
// (#2984): lines inside a fenced code block keep their whitespace and
// their blank lines, and a trailing run of two or more spaces that
// CommonMark renders as a hard break is kept, as exactly two spaces.
// Only the comparison uses it; every recorded hash is of the body as sent.
func normalizeDriftBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	var fence string // the opening marker while inside a fence
	for i, l := range lines {
		if fence != "" {
			out = append(out, l)
			if isClosingFence(l, fence) {
				fence = ""
			}
			continue
		}
		t := strings.TrimRightFunc(l, unicode.IsSpace)
		if m := openingFence(t); m != "" {
			fence = m
			out = append(out, t)
			continue
		}
		if t == "" {
			if len(out) > 0 && out[len(out)-1] == "" {
				continue
			}
			out = append(out, t)
			continue
		}
		if isHardBreak(l, t, lines[i+1:]) {
			t += "  "
		}
		out = append(out, t)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// fenceIndent strips the up to three leading spaces a fence marker or an
// ATX heading may carry. A fourth space makes the line an indented code
// block instead, reported as false.
func fenceIndent(l string) (string, bool) {
	t := strings.TrimLeft(l, " ")
	return t, len(l)-len(t) <= 3
}

// openingFence returns the marker (three or more backticks or tildes) a
// line opens a fenced code block with, or "" when it opens none.
func openingFence(l string) string {
	t, ok := fenceIndent(l)
	if !ok || len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return ""
	}
	n := len(t) - len(strings.TrimLeft(t, t[:1]))
	if n < 3 || (t[0] == '`' && strings.Contains(t[n:], "`")) {
		return ""
	}
	return t[:n]
}

// isClosingFence reports whether a line closes the fence opened with
// marker: the same character, at least as many of it, and nothing after
// but whitespace.
func isClosingFence(l, marker string) bool {
	t, ok := fenceIndent(l)
	if !ok {
		return false
	}
	rest := strings.TrimLeft(t, marker[:1])
	return len(t)-len(rest) >= len(marker) && strings.TrimRightFunc(rest, unicode.IsSpace) == ""
}

// isHardBreak reports whether line l, trimmed to t, ends in a CommonMark
// hard break: two or more spaces and nothing else after the content, on a
// line that is not an ATX heading, followed by a line that continues the
// same block. A break before a blank line, before a line that starts a new
// block, or at the end of the body renders nothing.
func isHardBreak(l, t string, rest []string) bool {
	tail := l[len(t):]
	if len(tail) < 2 || strings.Trim(tail, " ") != "" || isATXHeading(t) {
		return false
	}
	return len(rest) > 0 && strings.TrimSpace(rest[0]) != "" && !startsBlock(rest[0])
}

// isATXHeading reports whether a line is a `#` to `######` heading.
func isATXHeading(l string) bool {
	h, ok := fenceIndent(l)
	if !ok || !strings.HasPrefix(h, "#") {
		return false
	}
	n := len(h) - len(strings.TrimLeft(h, "#"))
	return n <= 6 && (n == len(h) || h[n] == ' ' || h[n] == '\t')
}

// startsBlock reports whether a line begins a block that ends the one
// before it: a heading, a blockquote, a list item, a fence, or a `-`/`=`
// line (a setext underline or a thematic break). Any of these closes the
// paragraph, so trailing spaces on the line before render no break.
func startsBlock(l string) bool {
	t, ok := fenceIndent(l)
	if !ok || t == "" {
		return false
	}
	switch t[0] {
	case '#':
		return isATXHeading(t)
	case '>', '=':
		return true
	case '-', '*', '+':
		return len(t) == 1 || t[1] == ' ' || t[1] == '\t' ||
			(t[0] != '+' && strings.Trim(t, string(t[0])+" \t") == "")
	case '`', '~':
		return openingFence(t) != ""
	}
	n := len(t) - len(strings.TrimLeft(t, "0123456789"))
	return n >= 1 && n <= 9 && n < len(t) && (t[n] == '.' || t[n] == ')') &&
		(n+1 == len(t) || t[n+1] == ' ' || t[n+1] == '\t')
}

// bodyDrifted reports whether a page body differs in content from the
// body a synced row holds, ignoring what normalizeDriftBody removes.
func bodyDrifted(stored, page string) bool {
	return normalizeDriftBody(stored) != normalizeDriftBody(page)
}
