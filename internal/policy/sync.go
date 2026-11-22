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
	// version and body; nothing was written. Drift recorded by an earlier
	// run is cleared.
	SyncStatusUnchanged = "unchanged"
	// SyncStatusDrift: the page's body changed without a version bump.
	// No version was written; the drift is recorded on the policy
	// (#2851).
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
// policy instead (#2851). Drift clears when a later run's body matches
// again or arrives with a new version.
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
// invalid are pages the caller refused (a missing or invalid property);
// they are reported as skipped and written nowhere. Unlike Import, valid
// pages are written even when another page is skipped. Runs for one source
// are serialised, so an overlapping run cannot create a page twice.
// Everything runs in one transaction.
func (s *Store) Sync(ctx context.Context, src SyncSource, pages []SyncPage, invalid []SyncResult, allowEmpty bool, keyID string) (SyncReport, error) {
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
		if len(pages) == 0 && len(invalid) == 0 && synced > 0 && !allowEmpty {
			return ErrSyncEmptyRefused
		}
		// Every page the run carries is present, valid or not: a page
		// with a bad property is still labelled.
		present := []string{}
		for _, r := range invalid {
			present = append(present, r.PageID)
		}
		for _, p := range pages {
			present = append(present, p.PageID)
		}
		if err := q.ClearSyncedPolicyMissing(ctx, dbx.ClearSyncedPolicyMissingParams{
			TenantID: pgUUID(tenantID), SyncSite: &src.Site, SyncSpace: &src.Space, PresentPageIds: present,
		}); err != nil {
			return fmt.Errorf("sync: clear missing: %w", err)
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
			case !bodyDrifted(cur.BodyMd, p.In.BodyMd):
				// Same content as the row holds, up to whitespace
				// (#2910); the row and its recorded hash stay as they are.
				if cur.SyncDriftDetectedAt.Valid {
					if cur, err = q.ClearSyncedPolicyDrift(ctx, dbx.ClearSyncedPolicyDriftParams{
						TenantID: pgUUID(tenantID), ID: cur.ID,
					}); err != nil {
						return fmt.Errorf("sync: clear drift on page %q: %w", p.PageID, err)
					}
				}
				existing := policyFromRow(cur)
				res.Status, res.Policy = SyncStatusUnchanged, &existing
			default:
				// The body changed and the version did not: what staff
				// acknowledged is not rewritten under them. The drift is
				// recorded on the policy and nothing else is written.
				// No row back means this drift is already recorded.
				drifted, err := q.SetSyncedPolicyDrift(ctx, dbx.SetSyncedPolicyDriftParams{
					TenantID: pgUUID(tenantID), ID: cur.ID, SyncDriftBodySha256: &hash,
				})
				switch {
				case errors.Is(err, pgx.ErrNoRows):
					drifted = cur
				case err != nil:
					return fmt.Errorf("sync: record drift on page %q: %w", p.PageID, err)
				}
				existing := policyFromRow(drifted)
				res.Status, res.Policy = SyncStatusDrift, &existing
				res.Reason = fmt.Sprintf("the page body changed without a version bump; version %q is unchanged. Bump the page's version to publish the change, or revert the edit", cur.Version)
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
		if taken.SyncDriftDetectedAt != nil {
			res.Reason += ". The page body differs from the imported text, so it is reported as drift; bump the page's version to publish it"
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
	row, err := q.TakeOverImportedPolicy(ctx, dbx.TakeOverImportedPolicyParams{
		TenantID:            pgUUID(tenantID),
		ID:                  imported.ID,
		SourceUrl:           stringPtr(p.In.SourceURL),
		SyncSite:            &src.Site,
		SyncSpace:           &src.Space,
		SyncPageID:          &p.PageID,
		SyncedBodySha256:    &rowHash,
		SyncedBy:            &keyID,
		SyncDriftBodySha256: drift,
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
// Only the comparison uses it; every recorded hash is of the body as sent.
func normalizeDriftBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(body, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimRightFunc(l, unicode.IsSpace)
		if l == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// bodyDrifted reports whether a page body differs in content from the
// body a synced row holds, ignoring what normalizeDriftBody removes.
func bodyDrifted(stored, page string) bool {
	return normalizeDriftBody(stored) != normalizeDriftBody(page)
}
