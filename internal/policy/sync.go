package policy

import (
	"context"
	"errors"
	"fmt"

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
	// SyncStatusSkipped: the page was not synced; Reason says why.
	SyncStatusSkipped = "skipped"
)

// auditActionSynced is the policy_audit_log action for a synced write.
const auditActionSynced = "synced"

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
// order given (skipped pages first, as the caller passes them).
type SyncReport struct {
	Results []SyncResult
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
// invalid are pages the caller refused (a missing or invalid property);
// they are reported as skipped and written nowhere. Unlike Import, valid
// pages are written even when another page is skipped. Runs for one source
// are serialised, so an overlapping run cannot create a page twice.
// Everything runs in one transaction.
func (s *Store) Sync(ctx context.Context, src SyncSource, pages []SyncPage, invalid []SyncResult, keyID string) (SyncReport, error) {
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
		results := append([]SyncResult{}, invalid...)
		seen := map[string]bool{}
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
				written, err := syncCreate(ctx, q, tenantID, src, p, hash, keyID)
				if err != nil {
					return err
				}
				res.Status, res.Policy = SyncStatusCreated, &written
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
			case cur.SyncedBodySha256 != nil && *cur.SyncedBodySha256 == hash:
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
		report.Results = results
		return nil
	})
	if err != nil {
		return SyncReport{}, err
	}
	return report, nil
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
