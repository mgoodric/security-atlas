// Issue #1775 — GET /v1/controls/remap-proposals.
//
// When `atlas-cli catalog import-scf` makes a new SCF release current, every
// non-pinned control left on an older release's anchor gets one remap
// proposal (scfimport.RebindControls), classified from the new release's
// legacy IDs as unchanged, renamed, split, reused_id or no_successor. A
// proposal moves nothing; its status says whether the #1971 D3 upgrade
// re-bound the control in place ("rebound") or left it for a person
// ("open"); a reviewer's decision (#1776) makes it approved, rejected or
// kept_legacy. This endpoint lists the bearer tenant's proposals, filterable
// by ?class=, ?class_not= and ?status=.
//
// #2325: the list is keyset paged. ?limit= (default 100, max 500, the
// controls list's bounds) sizes a page; the response's next_cursor, passed
// back as ?cursor=, reads the next one, and total counts every proposal the
// filters match. ?status=rebound&class_not=unchanged lists the controls the
// upgrade moved that a person may still want to look at (a split, a rename
// or a reused code). A pending proposal whose control has a newer version is
// left out: it is stale, and deciding it is refused.
//
// Authz: the path resolves to resource "controls", action "read", the same
// OPA admit as GET /v1/controls. RLS and the query's explicit tenant id
// keep the read to the bearer's tenant.

package controls

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// remapProposalLister is the store seam the handler reads through; the
// control.Store satisfies it.
type remapProposalLister interface {
	ListRemapProposals(ctx context.Context, f control.RemapProposalFilter) ([]dbx.ListSCFRemapProposalsRow, int64, error)
}

// RemapProposalsHandler exposes GET /v1/controls/remap-proposals.
type RemapProposalsHandler struct {
	store remapProposalLister
}

// NewRemapProposalsHandler wires the handler over the control store.
func NewRemapProposalsHandler(store remapProposalLister) *RemapProposalsHandler {
	return &RemapProposalsHandler{store: store}
}

// RemapProposal is one proposal on the wire. It and RemapProposalsResponse
// are exported so internal/api/openapi can derive their schemas; the web
// client's types are generated from that spec (#2394).
type RemapProposal struct {
	ID              string   `json:"id"`
	ControlID       string   `json:"control_id"`
	BundleID        string   `json:"bundle_id"`
	Title           string   `json:"title"`
	LegacySCFID     string   `json:"legacy_scf_id"`
	LegacyRelease   string   `json:"legacy_release"`
	TargetRelease   string   `json:"target_release"`
	Class           string   `json:"class"`
	Status          string   `json:"status"`
	SuccessorSCFIDs []string `json:"successor_scf_ids"`
	CreatedAt       string   `json:"created_at"`
}

type RemapProposalsResponse struct {
	Proposals []RemapProposal `json:"proposals"`
	// Count is the proposals on this page; Total every one the filters
	// match.
	Count      int    `json:"count"`
	Total      int64  `json:"total"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// remapCursorWire is the opaque ?cursor= value: the last row of a page, in
// the list's order.
type remapCursorWire struct {
	CreatedAt time.Time `json:"t"`
	BundleID  string    `json:"b"`
	ID        uuid.UUID `json:"i"`
}

func encodeRemapCursor(p dbx.ListSCFRemapProposalsRow) string {
	raw, _ := json.Marshal(remapCursorWire{CreatedAt: p.CreatedAt.Time, BundleID: p.BundleID, ID: uuid.UUID(p.ID.Bytes)})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeRemapCursor(s string) (*control.RemapProposalCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	var c remapCursorWire
	if err := json.Unmarshal(raw, &c); err != nil || c.CreatedAt.IsZero() || c.ID == uuid.Nil {
		return nil, false
	}
	return &control.RemapProposalCursor{CreatedAt: c.CreatedAt, BundleID: c.BundleID, ID: c.ID}, true
}

var remapStatuses = control.RemapStatuses

// filterParam reads an optional query filter; an empty value means any, and
// a value outside allowed is rejected.
func filterParam(r *http.Request, name string, allowed []string) (*string, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, true
	}
	if !slices.Contains(allowed, v) {
		return nil, false
	}
	return &v, true
}

// List handles GET /v1/controls/remap-proposals. Always 200 with an
// envelope; 400 on an unknown class, class_not or status, or a malformed
// cursor.
func (h *RemapProposalsHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, err := tenancy.TenantFromContext(r.Context()); err != nil {
		httpresp.WriteError(w, http.StatusUnauthorized, "tenant context missing")
		return
	}
	classes := make([]string, len(scfimport.RemapClasses))
	for i, c := range scfimport.RemapClasses {
		classes[i] = string(c)
	}
	class, ok := filterParam(r, "class", classes)
	if !ok {
		httpresp.WriteError(w, http.StatusBadRequest, "class must be one of unchanged, renamed, split, reused_id, no_successor")
		return
	}
	classNot, ok := filterParam(r, "class_not", classes)
	if !ok {
		httpresp.WriteError(w, http.StatusBadRequest, "class_not must be one of unchanged, renamed, split, reused_id, no_successor")
		return
	}
	status, ok := filterParam(r, "status", remapStatuses)
	if !ok {
		httpresp.WriteError(w, http.StatusBadRequest, "status must be one of "+strings.Join(remapStatuses, ", "))
		return
	}
	limit, _ := listPagination(r)
	var after *control.RemapProposalCursor
	if v := r.URL.Query().Get("cursor"); v != "" {
		if after, ok = decodeRemapCursor(v); !ok {
			httpresp.WriteError(w, http.StatusBadRequest, "cursor is not a next_cursor this endpoint returned")
			return
		}
	}
	// One row past the page says whether another page follows.
	rows, total, err := h.store.ListRemapProposals(r.Context(), control.RemapProposalFilter{
		Class: class, ClassNot: classNot, Status: status, After: after, Limit: limit + 1,
	})
	if err != nil {
		httperr.WriteInternal(w, r, "list scf remap proposals", err)
		return
	}
	next := ""
	if len(rows) > int(limit) {
		rows = rows[:limit]
		next = encodeRemapCursor(rows[len(rows)-1])
	}
	out := make([]RemapProposal, len(rows))
	for i, p := range rows {
		out[i] = RemapProposal{
			ID:              uuid.UUID(p.ID.Bytes).String(),
			ControlID:       uuid.UUID(p.ControlID.Bytes).String(),
			BundleID:        p.BundleID,
			Title:           p.ControlTitle,
			LegacySCFID:     p.LegacyScfID,
			LegacyRelease:   p.LegacyRelease,
			TargetRelease:   p.TargetRelease,
			Class:           p.Class,
			Status:          p.Status,
			SuccessorSCFIDs: p.SuccessorScfIds,
			CreatedAt:       p.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if out[i].SuccessorSCFIDs == nil {
			out[i].SuccessorSCFIDs = []string{}
		}
	}
	httpresp.WriteJSON(w, http.StatusOK, RemapProposalsResponse{Proposals: out, Count: len(out), Total: total, NextCursor: next})
}
