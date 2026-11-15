package control

// Reviewer decisions on SCF remap proposals (#1776).
//
// A proposal (#1775) names a control left on an older SCF release's anchor
// and the successor anchors the current release offers. A person resolves
// it:
//
//   - approve re-anchors the control on a successor as a superseding
//     control revision, through the same upload path as any manifest
//     change (never an in-place edit); final.
//   - reject leaves the control on its legacy anchor; the proposal can be
//     approved or kept legacy later.
//   - keep legacy leaves the control on its legacy anchor; final.
//
// Each decision sets the proposal's status and appends a row to
// scf_remap_decisions, the anchor history, naming who decided.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Remap proposal statuses. open and rebound are written by the import
// (#1775); the rest by a reviewer's decision.
const (
	RemapStatusOpen       = "open"
	RemapStatusRebound    = "rebound"
	RemapStatusApproved   = "approved"
	RemapStatusRejected   = "rejected"
	RemapStatusKeptLegacy = "kept_legacy"
)

// RemapStatuses lists every proposal status.
var RemapStatuses = []string{RemapStatusOpen, RemapStatusRebound, RemapStatusApproved, RemapStatusRejected, RemapStatusKeptLegacy}

// remapDecidable are the statuses a reviewer may still decide: open, and
// rejected, which can be revisited.
var remapDecidable = []string{RemapStatusOpen, RemapStatusRejected}

// remapClassUnchanged is the one class bulk-approve applies to.
const remapClassUnchanged = "unchanged"

// maxRemapReason bounds a decision's reason, matching the column CHECK.
const maxRemapReason = 2000

var (
	// ErrRemapProposalNotFound: no proposal with that id in the caller's
	// tenant.
	ErrRemapProposalNotFound = errors.New("control remap: proposal not found")
	// ErrRemapProposalDecided: the proposal is approved, kept legacy,
	// rebound, or (for a reject) already rejected.
	ErrRemapProposalDecided = errors.New("control remap: proposal already decided")
	// ErrRemapAnchorRequired: the proposal has no single successor (split,
	// no_successor), so approval must name the anchor.
	ErrRemapAnchorRequired = errors.New("control remap: choose the successor anchor (scf_id) to approve this proposal")
	// ErrRemapAnchorNotInRelease: the chosen code is not in the proposal's
	// target release.
	ErrRemapAnchorNotInRelease = errors.New("control remap: the chosen anchor is not in the target SCF release")
	// ErrRemapControlChanged: the control was re-versioned or re-anchored
	// since the proposal was made, so the proposal no longer describes it.
	ErrRemapControlChanged = errors.New("control remap: the control changed since the proposal was made")
	// ErrRemapBulkRunning: another bulk-approve, or a decision on one of
	// its proposals, holds the proposals' row locks.
	ErrRemapBulkRunning = errors.New("control remap: a bulk approval is already running")
	// ErrRemapDeciderRequired: no deciding person was named.
	ErrRemapDeciderRequired = errors.New("control remap: a deciding person is required")
	// ErrRemapReasonTooLong: the reason exceeds maxRemapReason characters.
	ErrRemapReasonTooLong = fmt.Errorf("control remap: reason exceeds %d characters", maxRemapReason)
)

// RemapDecision is one decision on a proposal, as recorded in the anchor
// history. The New* fields are set on an approval only.
type RemapDecision struct {
	ProposalID      string `json:"proposal_id"`
	Decision        string `json:"decision"`
	Bulk            bool   `json:"bulk"`
	BundleID        string `json:"bundle_id"`
	LegacyControlID string `json:"legacy_control_id"`
	LegacyAnchorID  string `json:"legacy_anchor_id"`
	LegacySCFID     string `json:"legacy_scf_id"`
	LegacyRelease   string `json:"legacy_release"`
	NewControlID    string `json:"new_control_id,omitempty"`
	NewVersion      int32  `json:"new_version,omitempty"`
	NewAnchorID     string `json:"new_anchor_id,omitempty"`
	NewSCFID        string `json:"new_scf_id,omitempty"`
	NewRelease      string `json:"new_release,omitempty"`
	Reason          string `json:"reason,omitempty"`
	DecidedBy       string `json:"decided_by"`
	DecidedAt       string `json:"decided_at"`
}

// RemapApproveInput approves one proposal. SCFID is the successor code in
// the target release; empty takes the proposal's single successor.
type RemapApproveInput struct {
	ProposalID string
	SCFID      string
	Reason     string
	DecidedBy  string
}

// RemapCloseInput rejects or keeps legacy one proposal.
type RemapCloseInput struct {
	ProposalID string
	Reason     string
	DecidedBy  string
}

// RemapBulkSkip is an unchanged proposal bulk-approve left open, with why.
type RemapBulkSkip struct {
	ProposalID string `json:"proposal_id"`
	BundleID   string `json:"bundle_id"`
	Reason     string `json:"reason"`
}

// RemapBulkResult reports a bulk-approve of unchanged proposals.
type RemapBulkResult struct {
	ApprovedCount int             `json:"approved_count"`
	Approved      []RemapDecision `json:"approved"`
	Skipped       []RemapBulkSkip `json:"skipped"`
}

// remapProposalRow is a locked proposal with the fields a decision needs.
type remapProposalRow struct {
	ID             uuid.UUID
	ControlID      uuid.UUID
	BundleID       string
	LegacyAnchorID uuid.UUID
	LegacySCFID    string
	LegacyRelease  string
	TargetRelease  string
	Class          string
	Status         string
	Successors     []string
}

const remapProposalColumns = `id, control_id, bundle_id, legacy_anchor_id, legacy_scf_id,
	legacy_release, target_release, class, status, successor_scf_ids`

func scanRemapProposal(row pgx.Row) (remapProposalRow, error) {
	var p remapProposalRow
	err := row.Scan(&p.ID, &p.ControlID, &p.BundleID, &p.LegacyAnchorID, &p.LegacySCFID,
		&p.LegacyRelease, &p.TargetRelease, &p.Class, &p.Status, &p.Successors)
	return p, err
}

// ApproveRemap approves one proposal: the control is re-anchored on the
// chosen successor as a superseding revision, and the proposal becomes
// approved.
func (s *Store) ApproveRemap(ctx context.Context, in RemapApproveInput) (RemapDecision, error) {
	if err := checkDecider(in.DecidedBy, in.Reason); err != nil {
		return RemapDecision{}, err
	}
	var out RemapDecision
	err := s.inPgxTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		p, err := lockRemapProposal(ctx, tx, tenantID, in.ProposalID)
		if err != nil {
			return err
		}
		out, err = approveLocked(ctx, tx, tenantID, p, in.SCFID, in.Reason, in.DecidedBy, false)
		return err
	})
	return out, err
}

// RejectRemap rejects an open proposal. The control stays on its legacy
// anchor and the proposal can still be approved or kept legacy.
func (s *Store) RejectRemap(ctx context.Context, in RemapCloseInput) (RemapDecision, error) {
	return s.closeRemap(ctx, in, RemapStatusRejected, []string{RemapStatusOpen})
}

// KeepLegacyRemap keeps the control on its legacy anchor for good.
func (s *Store) KeepLegacyRemap(ctx context.Context, in RemapCloseInput) (RemapDecision, error) {
	return s.closeRemap(ctx, in, RemapStatusKeptLegacy, remapDecidable)
}

func (s *Store) closeRemap(ctx context.Context, in RemapCloseInput, status string, from []string) (RemapDecision, error) {
	if err := checkDecider(in.DecidedBy, in.Reason); err != nil {
		return RemapDecision{}, err
	}
	var out RemapDecision
	err := s.inPgxTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		p, err := lockRemapProposal(ctx, tx, tenantID, in.ProposalID)
		if err != nil {
			return err
		}
		if !slices.Contains(from, p.Status) {
			return fmt.Errorf("%w (status %s)", ErrRemapProposalDecided, p.Status)
		}
		if err := setRemapStatus(ctx, tx, tenantID, p.ID, status, uuid.Nil); err != nil {
			return err
		}
		out = RemapDecision{
			ProposalID: p.ID.String(), Decision: status, BundleID: p.BundleID,
			LegacyControlID: p.ControlID.String(), LegacyAnchorID: p.LegacyAnchorID.String(),
			LegacySCFID: p.LegacySCFID, LegacyRelease: p.LegacyRelease,
			Reason: strings.TrimSpace(in.Reason), DecidedBy: in.DecidedBy,
		}
		return insertRemapDecision(ctx, tx, tenantID, &out, uuid.Nil, uuid.Nil)
	})
	return out, err
}

// BulkApproveUnchangedRemaps approves every open unchanged proposal of the
// tenant with its single successor. Rejected and kept-legacy proposals are
// never touched: a person already decided them. The proposals are locked
// with NOWAIT, so a concurrent bulk-approve gets ErrRemapBulkRunning
// instead of queueing; a re-run after success finds nothing to do.
func (s *Store) BulkApproveUnchangedRemaps(ctx context.Context, decidedBy string) (RemapBulkResult, error) {
	if err := checkDecider(decidedBy, ""); err != nil {
		return RemapBulkResult{}, err
	}
	out := RemapBulkResult{Approved: []RemapDecision{}, Skipped: []RemapBulkSkip{}}
	err := s.inPgxTx(ctx, func(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
		rows, err := tx.Query(ctx, `
			SELECT `+remapProposalColumns+`
			FROM scf_remap_proposals
			WHERE tenant_id = $1 AND class = $2 AND status = $3
			ORDER BY bundle_id, id
			FOR UPDATE NOWAIT`, tenantID, remapClassUnchanged, RemapStatusOpen)
		if err != nil {
			return lockErr(err)
		}
		var props []remapProposalRow
		for rows.Next() {
			p, err := scanRemapProposal(rows)
			if err != nil {
				rows.Close()
				return lockErr(err)
			}
			props = append(props, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return lockErr(err)
		}
		for _, p := range props {
			// A savepoint per proposal: one that cannot be applied is
			// skipped without aborting the others.
			sp, err := tx.Begin(ctx)
			if err != nil {
				return fmt.Errorf("control remap: savepoint: %w", err)
			}
			d, err := approveLocked(ctx, sp, tenantID, p, "", "", decidedBy, true)
			switch {
			case err == nil:
				if err := sp.Commit(ctx); err != nil {
					return fmt.Errorf("control remap: release savepoint: %w", err)
				}
				out.Approved = append(out.Approved, d)
			case errors.Is(err, ErrRemapControlChanged), errors.Is(err, ErrRemapAnchorNotInRelease),
				errors.Is(err, ErrRemapAnchorRequired):
				if rbErr := sp.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("control remap: roll back savepoint: %w", rbErr)
				}
				out.Skipped = append(out.Skipped, RemapBulkSkip{
					ProposalID: p.ID.String(), BundleID: p.BundleID,
					Reason: strings.TrimPrefix(err.Error(), "control remap: "),
				})
			default:
				_ = sp.Rollback(ctx)
				return err
			}
		}
		return nil
	})
	out.ApprovedCount = len(out.Approved)
	if err != nil {
		return RemapBulkResult{}, err
	}
	return out, nil
}

// approveLocked re-anchors the control of a proposal locked by the caller.
func approveLocked(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, p remapProposalRow, scfID, reason, decidedBy string, bulk bool) (RemapDecision, error) {
	if !slices.Contains(remapDecidable, p.Status) {
		return RemapDecision{}, fmt.Errorf("%w (status %s)", ErrRemapProposalDecided, p.Status)
	}
	code := strings.TrimSpace(scfID)
	if code == "" {
		if p.Class == "split" || p.Class == "no_successor" || len(p.Successors) != 1 {
			return RemapDecision{}, fmt.Errorf("%w (class %s)", ErrRemapAnchorRequired, p.Class)
		}
		code = p.Successors[0]
	}

	// The proposal describes the control's active version on the legacy
	// anchor. If the control was re-versioned or re-anchored since, the
	// proposal is stale and approving it would re-anchor something else.
	var manifest, description string
	var anchor *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT bundle_manifest_yaml, description, scf_anchor_id
		FROM controls
		WHERE tenant_id = $1 AND id = $2 AND superseded_by IS NULL
		FOR UPDATE`, tenantID, p.ControlID).Scan(&manifest, &description, &anchor)
	if errors.Is(err, pgx.ErrNoRows) {
		return RemapDecision{}, fmt.Errorf("%w: control %s has a newer version", ErrRemapControlChanged, p.BundleID)
	}
	if err != nil {
		return RemapDecision{}, fmt.Errorf("control remap: lock control: %w", err)
	}
	if anchor == nil || *anchor != p.LegacyAnchorID {
		return RemapDecision{}, fmt.Errorf("%w: control %s is no longer on its legacy anchor", ErrRemapControlChanged, p.BundleID)
	}

	raw, err := reanchorManifest([]byte(manifest), code, p.TargetRelease)
	if err != nil {
		return RemapDecision{}, err
	}
	bundle, err := FinalizeBundleForHTTP(raw)
	if err != nil {
		return RemapDecision{}, fmt.Errorf("control remap: rebuild manifest of %s: %w", p.BundleID, err)
	}
	// The stored manifest holds the YAML only; the active row's description
	// also carries any DESCRIPTION.md text, so keep it.
	bundle.Manifest.Description = description

	res, err := UploadTx(ctx, tx, bundle, decidedBy)
	switch {
	case errors.Is(err, ErrSCFCodeNotInRelease), errors.Is(err, ErrSCFReleaseUnknown):
		return RemapDecision{}, fmt.Errorf("%w: %s is not in SCF release %s", ErrRemapAnchorNotInRelease, code, p.TargetRelease)
	case err != nil:
		return RemapDecision{}, fmt.Errorf("control remap: re-anchor %s: %w", p.BundleID, err)
	case res.SupersededID != p.ControlID:
		// The rewritten manifest always changes scf_release, so the upload
		// must supersede the locked version; anything else is a bug.
		return RemapDecision{}, fmt.Errorf("%w: re-anchoring %s did not supersede it", ErrRemapControlChanged, p.BundleID)
	}

	if err := setRemapStatus(ctx, tx, tenantID, p.ID, RemapStatusApproved, res.AnchorID); err != nil {
		return RemapDecision{}, err
	}
	out := RemapDecision{
		ProposalID: p.ID.String(), Decision: RemapStatusApproved, Bulk: bulk, BundleID: p.BundleID,
		LegacyControlID: p.ControlID.String(), LegacyAnchorID: p.LegacyAnchorID.String(),
		LegacySCFID: p.LegacySCFID, LegacyRelease: p.LegacyRelease,
		NewControlID: res.ControlID.String(), NewVersion: res.Version,
		NewAnchorID: res.AnchorID.String(), NewSCFID: code, NewRelease: res.SCFRelease,
		Reason: strings.TrimSpace(reason), DecidedBy: decidedBy,
	}
	if err := insertRemapDecision(ctx, tx, tenantID, &out, res.ControlID, res.AnchorID); err != nil {
		return RemapDecision{}, err
	}
	return out, nil
}

// reanchorManifest returns the control.yaml with scf_anchor_id set to code
// and scf_release to release, every other key left as it was.
func reanchorManifest(raw []byte, code, release string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("control remap: parse stored manifest: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("control remap: stored manifest is not a YAML mapping")
	}
	m := doc.Content[0]
	set := func(key, value string) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == key {
				m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
				return
			}
		}
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	set("scf_anchor_id", code)
	set("scf_release", release)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("control remap: write manifest: %w", err)
	}
	return out, nil
}

func checkDecider(decidedBy, reason string) error {
	if strings.TrimSpace(decidedBy) == "" {
		return ErrRemapDeciderRequired
	}
	if len([]rune(strings.TrimSpace(reason))) > maxRemapReason {
		return ErrRemapReasonTooLong
	}
	return nil
}

// lockRemapProposal reads and locks one proposal. RLS and the explicit
// tenant filter keep it to the caller's tenant, so another tenant's id reads
// as not found.
func lockRemapProposal(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, id string) (remapProposalRow, error) {
	pid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return remapProposalRow{}, ErrRemapProposalNotFound
	}
	p, err := scanRemapProposal(tx.QueryRow(ctx, `
		SELECT `+remapProposalColumns+`
		FROM scf_remap_proposals WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, pid))
	if errors.Is(err, pgx.ErrNoRows) {
		return remapProposalRow{}, ErrRemapProposalNotFound
	}
	if err != nil {
		return remapProposalRow{}, fmt.Errorf("control remap: lock proposal: %w", err)
	}
	return p, nil
}

func setRemapStatus(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID, status string, target uuid.UUID) error {
	var targetArg any
	if target != uuid.Nil {
		targetArg = target
	}
	tag, err := tx.Exec(ctx, `
		UPDATE scf_remap_proposals SET status = $3, target_anchor_id = $4
		WHERE tenant_id = $1 AND id = $2`, tenantID, id, status, targetArg)
	if err != nil {
		return fmt.Errorf("control remap: record status: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrRemapProposalNotFound
	}
	return nil
}

func insertRemapDecision(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, d *RemapDecision, newControl, newAnchor uuid.UUID) error {
	var newControlArg, newAnchorArg, newSCF, newRelease, reason any
	if newControl != uuid.Nil {
		newControlArg, newAnchorArg, newSCF, newRelease = newControl, newAnchor, d.NewSCFID, d.NewRelease
	}
	if d.Reason != "" {
		reason = d.Reason
	}
	var decidedAt string
	err := tx.QueryRow(ctx, `
		INSERT INTO scf_remap_decisions (
			tenant_id, proposal_id, decision, bulk, legacy_control_id,
			legacy_anchor_id, legacy_scf_id, legacy_release,
			new_control_id, new_anchor_id, new_scf_id, new_release,
			reason, decided_by)
		VALUES ($1, $2::uuid, $3, $4, $5::uuid, $6::uuid, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING to_char(decided_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		tenantID, d.ProposalID, d.Decision, d.Bulk, d.LegacyControlID,
		d.LegacyAnchorID, d.LegacySCFID, d.LegacyRelease,
		newControlArg, newAnchorArg, newSCF, newRelease, reason, d.DecidedBy).Scan(&decidedAt)
	if err != nil {
		return fmt.Errorf("control remap: record decision: %w", err)
	}
	d.DecidedAt = decidedAt
	return nil
}

// pgLockNotAvailable is SQLSTATE 55P03, raised by FOR UPDATE NOWAIT.
const pgLockNotAvailable = "55P03"

func lockErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgLockNotAvailable {
		return ErrRemapBulkRunning
	}
	return fmt.Errorf("control remap: lock unchanged proposals: %w", err)
}

// inPgxTx is inTx handing fn the transaction itself, for UploadTx.
func (s *Store) inPgxTx(ctx context.Context, fn func(context.Context, pgx.Tx, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("control: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("control: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("control: commit: %w", err)
	}
	return nil
}
