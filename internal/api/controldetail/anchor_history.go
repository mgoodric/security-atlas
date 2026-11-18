// Issue #2392 — GET /v1/controls/{id}/anchor-history.
//
// Every reviewer decision on an SCF remap proposal (#1776) appends one row
// to scf_remap_decisions: the legacy code and release, the new code and
// release an approval re-anchored to, the superseding revision, the reason,
// who decided and when. That table is the control's anchor history. This
// read returns it for the control's whole bundle, across revisions, so the
// history survives the re-anchor that created a new revision.
//
// Keyset paged newest first on (decided_at, id), with the same opaque cursor
// and limit bounds as GET /v1/controls/{id}/history.
//
// Authz: the path resolves to resource "controls", action "read"; the
// handler's defense-in-depth gate is the same live-role check as the other
// control-detail reads (requireControlRead). The tenant comes from the
// credential only; RLS and the explicit tenant id bind both queries, so a
// control id from another tenant reads as no row and the handler answers
// 404.
package controldetail

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// errControlNotFound is the store's answer for a control id that is not
// the caller's tenant's control.
var errControlNotFound = errors.New("controldetail: control not found")

// anchorHistoryReader is the read seam GET /v1/controls/{id}/anchor-history
// reads through; the production *Store satisfies it and the unit tests
// inject a stub.
type anchorHistoryReader interface {
	AnchorHistoryForControl(ctx context.Context, controlID uuid.UUID, p historyPage) ([]dbx.ListControlAnchorHistoryPagedRow, error)
}

// AnchorHistoryForControl reads one page of the remap decisions on every
// revision of controlID's bundle. It returns errControlNotFound when
// controlID is not a control of the tenant in ctx.
func (s *Store) AnchorHistoryForControl(ctx context.Context, controlID uuid.UUID, p historyPage) ([]dbx.ListControlAnchorHistoryPagedRow, error) {
	var rows []dbx.ListControlAnchorHistoryPagedRow
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		bundleID, err := q.GetControlBundleForDetail(ctx, dbx.GetControlBundleForDetailParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(controlID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errControlNotFound
		}
		if err != nil {
			return err
		}
		rows, err = q.ListControlAnchorHistoryPaged(ctx, dbx.ListControlAnchorHistoryPagedParams{
			TenantID: pgUUID(tenantID),
			BundleID: bundleID,
			CursorTs: pgTimestamptz(p.cursor.ts),
			CursorID: pgUUID(p.cursor.id),
			RowLimit: p.pageRows + 1, // +1 probe row to detect a next page
		})
		return err
	})
	return rows, err
}

// anchorHistoryWire is one decision. Anchors are SCF codes with their
// release; the control revisions are version numbers. LegacyControlID and
// NewControlID let a client link to the revision without showing the id.
type anchorHistoryWire struct {
	ID              string  `json:"id"`
	Decision        string  `json:"decision"`
	Bulk            bool    `json:"bulk"`
	LegacyControlID string  `json:"legacy_control_id"`
	LegacyVersion   int32   `json:"legacy_version"`
	LegacySCFID     string  `json:"legacy_scf_id"`
	LegacyRelease   string  `json:"legacy_release"`
	NewControlID    *string `json:"new_control_id"`
	NewVersion      *int32  `json:"new_version"`
	NewSCFID        *string `json:"new_scf_id"`
	NewRelease      *string `json:"new_release"`
	Reason          *string `json:"reason"`
	// DecidedBy is the deciding principal: a users(id) uuid, or a
	// "system:" principal for a machine decision.
	DecidedBy string `json:"decided_by"`
	DecidedAt string `json:"decided_at"`
}

// AnchorHistory handles GET /v1/controls/{id}/anchor-history: 200 with
// `{control_id, decisions, count, next_cursor}`; 400 on a malformed id,
// cursor or limit; 403 without a tenant role; 404 when the control is not
// the caller's tenant's.
func (h *Handler) AnchorHistory(w http.ResponseWriter, r *http.Request) {
	ctx, controlID, ok := guardAndResolvePathControl(w, r)
	if !ok {
		return
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	pageRows, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpresp.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := h.anchors.AnchorHistoryForControl(ctx, controlID, historyPage{cursor: cursor, pageRows: pageRows})
	if errors.Is(err, errControlNotFound) {
		httpresp.WriteError(w, http.StatusNotFound, "control not found")
		return
	}
	if err != nil {
		httperr.WriteInternal(w, r, "controldetail anchor history", err)
		return
	}
	next := ""
	if int32(len(rows)) > pageRows {
		rows = rows[:pageRows]
		last := rows[len(rows)-1]
		next = encodeCursor(keyset{ts: last.DecidedAt.Time.UTC(), id: last.ID.Bytes})
	}
	out := make([]anchorHistoryWire, len(rows))
	for i, d := range rows {
		out[i] = anchorHistoryWire{
			ID:              uuidString(d.ID),
			Decision:        d.Decision,
			Bulk:            d.Bulk,
			LegacyControlID: uuidString(d.LegacyControlID),
			LegacyVersion:   d.LegacyVersion,
			LegacySCFID:     d.LegacyScfID,
			LegacyRelease:   d.LegacyRelease,
			NewControlID:    uuidPtr(d.NewControlID),
			NewVersion:      d.NewVersion,
			NewSCFID:        d.NewScfID,
			NewRelease:      d.NewRelease,
			Reason:          d.Reason,
			DecidedBy:       d.DecidedBy,
			DecidedAt:       tsString(d.DecidedAt),
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, map[string]any{
		"control_id":  controlID.String(),
		"decisions":   out,
		"count":       len(out),
		"next_cursor": next,
	})
}
