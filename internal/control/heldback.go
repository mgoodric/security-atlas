// Issue #2358 — stock controls whose shipped update the boot seed holds
// back. keepReviewedAnchor keeps a person's reviewed lineage (an approved
// SCF re-anchor, or an edit on top of one) when bootstrap re-uploads the
// shipped manifest, and records the hold in stock_control_holdbacks. This
// file reads those records back for GET /v1/admin/controls/held-back.

package control

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// HeldBackStockControl is one stock control the boot seed did not update
// because a person reviewed it.
type HeldBackStockControl struct {
	BundleID string `json:"bundle_id"`
	Title    string `json:"title"`
	// The control's active revision, which the boot seed kept.
	ActiveControlID    string `json:"active_control_id"`
	ActiveVersion      int32  `json:"active_version"`
	ActiveManifestHash string `json:"active_manifest_hash"`
	ActiveUploadedAt   string `json:"active_uploaded_at"`
	// The approved SCF re-anchor the active revision is, or descends from.
	ApprovedControlID string `json:"approved_control_id"`
	ApprovedVersion   int32  `json:"approved_version"`
	ApprovedSCFID     string `json:"approved_scf_id"`
	ApprovedRelease   string `json:"approved_release"`
	ApprovedAt        string `json:"approved_at"`
	// The shipped manifest the boot seed did not apply, and the stock
	// manifest the approval replaced. ShippedUpdate is true when they
	// differ: the platform shipped a change to this control after the
	// re-anchor, and the tenant has not received it.
	ShippedManifestHash  string `json:"shipped_manifest_hash"`
	ReplacedManifestHash string `json:"replaced_manifest_hash"`
	ShippedUpdate        bool   `json:"shipped_update"`
	FirstHeldAt          string `json:"first_held_at"`
	LastHeldAt           string `json:"last_held_at"`
}

// ListHeldBackStockControls returns the tenant's held-back stock controls,
// ordered by bundle_id. RLS and an explicit tenant filter keep it to the
// caller's tenant. A bundle that no longer ships is left out (#2599): the
// boot seed never uploads it again, so its row is never cleared.
func (s *Store) ListHeldBackStockControls(ctx context.Context) ([]HeldBackStockControl, error) {
	out := []HeldBackStockControl{}
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListStockControlHoldbacks(ctx, pgUUID(tenantID))
		if err != nil {
			return fmt.Errorf("list held-back stock controls: %w", err)
		}
		for _, r := range rows {
			if !scfimport.IsShippedStockBundle(r.BundleID) {
				continue
			}
			out = append(out, heldBackFromRow(r))
		}
		return nil
	})
	return out, err
}

func heldBackFromRow(r dbx.ListStockControlHoldbacksRow) HeldBackStockControl {
	return HeldBackStockControl{
		BundleID:             r.BundleID,
		Title:                r.Title,
		ActiveControlID:      uuid.UUID(r.ActiveControlID.Bytes).String(),
		ActiveVersion:        r.ActiveVersion,
		ActiveManifestHash:   r.ActiveManifestHash,
		ActiveUploadedAt:     heldBackTime(r.ActiveUploadedAt),
		ApprovedControlID:    uuid.UUID(r.ApprovedControlID.Bytes).String(),
		ApprovedVersion:      r.ApprovedVersion,
		ApprovedSCFID:        heldBackString(r.ApprovedScfID),
		ApprovedRelease:      heldBackString(r.ApprovedRelease),
		ApprovedAt:           heldBackTime(r.ApprovedAt),
		ShippedManifestHash:  r.ShippedManifestHash,
		ReplacedManifestHash: r.ReplacedManifestHash,
		ShippedUpdate:        r.ShippedManifestHash != r.ReplacedManifestHash,
		FirstHeldAt:          heldBackTime(r.FirstHeldAt),
		LastHeldAt:           heldBackTime(r.LastHeldAt),
	}
}

func heldBackTime(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func heldBackString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
