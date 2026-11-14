package scfimport

// Re-binding controls after a new SCF release (#1971, D3 — Matt's call,
// 2026-11-14).
//
// A new release_version mints new anchor rows (#1768), and coverage joins
// controls to crosswalk edges by anchor row, so a control left on the
// previous release's row earns no framework credit. RebindControls moves a
// control to the current release's anchor only when that anchor has the
// same SCF code AND the same title, i.e. its meaning did not change. Every
// other control on a legacy anchor is reported as needing re-anchoring, with
// a note saying where its concept went; nothing is moved silently.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/data/scfremap"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ControlOnLegacyAnchor is one active control bound to an anchor of an SCF
// release that is no longer current.
type ControlOnLegacyAnchor struct {
	TenantID       uuid.UUID
	ControlID      uuid.UUID
	BundleID       string
	ControlTitle   string
	LegacySCFID    string
	LegacyTitle    string
	LegacyRelease  string
	CurrentRelease string
	// Note says where the anchor's concept went; empty for a re-bound row.
	Note string
}

// RebindReport is the outcome of RebindControls.
type RebindReport struct {
	// Rebound controls moved to the current release's anchor with the same
	// code and title.
	Rebound []ControlOnLegacyAnchor
	// NeedsReanchoring controls stay on their legacy anchor: the code is
	// gone from the current release or now names a different control.
	NeedsReanchoring []ControlOnLegacyAnchor
}

// Rebindable reports whether a control on a legacy anchor can move to the
// current release's anchor without changing what it is anchored to: the
// same code must exist with the same title.
func Rebindable(r dbx.ListControlsOnLegacySCFAnchorsRow) bool {
	return r.CurrentAnchorID.Valid && r.CurrentAnchorTitle != nil && *r.CurrentAnchorTitle == r.LegacyAnchorTitle
}

// ReanchorNote tells an operator where a legacy anchor's concept went, most
// specific source first: the bundled remap table (data/scfremap, the D5
// table), then SCF's own legacy ID (#1769), then a code whose title changed,
// then a code that is gone.
func ReanchorNote(r dbx.ListControlsOnLegacySCFAnchorsRow) (string, error) {
	remap, ok, err := scfremap.Lookup(r.LegacyRelease, r.LegacyScfID)
	if err != nil {
		return "", err
	}
	switch {
	case ok:
		return remap.Note(), nil
	case r.SuccessorScfID != "":
		return fmt.Sprintf("%s -> %s in %s (SCF lists %s as its legacy ID)", r.LegacyScfID, r.SuccessorScfID, r.CurrentRelease, r.LegacyScfID), nil
	case r.CurrentAnchorID.Valid && r.CurrentAnchorTitle != nil:
		return fmt.Sprintf("%s is %q in %s, was %q: check the control still means it", r.LegacyScfID, *r.CurrentAnchorTitle, r.CurrentRelease, r.LegacyAnchorTitle), nil
	default:
		return fmt.Sprintf("%s is not in %s", r.LegacyScfID, r.CurrentRelease), nil
	}
}

func legacyRow(r dbx.ListControlsOnLegacySCFAnchorsRow) ControlOnLegacyAnchor {
	return ControlOnLegacyAnchor{
		TenantID:       uuid.UUID(r.TenantID.Bytes),
		ControlID:      uuid.UUID(r.ControlID.Bytes),
		BundleID:       r.BundleID,
		ControlTitle:   r.ControlTitle,
		LegacySCFID:    r.LegacyScfID,
		LegacyTitle:    r.LegacyAnchorTitle,
		LegacyRelease:  r.LegacyRelease,
		CurrentRelease: r.CurrentRelease,
	}
}

// RebindControls runs across every tenant, so db must be the migrate (owner)
// role, as for Import. Run it after Import; with a single SCF release it
// finds nothing.
func RebindControls(ctx context.Context, db DBTX) (RebindReport, error) {
	conn, ok := db.(pgxBeginner)
	if !ok {
		return RebindReport{}, errors.New("scfimport: db must support BeginTx (pgx.Conn / pgxpool.Pool)")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return RebindReport{}, fmt.Errorf("scfimport: rebind: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := dbx.New(tx)
	rows, err := q.ListControlsOnLegacySCFAnchors(ctx, pgtype.UUID{})
	if err != nil {
		return RebindReport{}, fmt.Errorf("scfimport: rebind: list controls on legacy anchors: %w", err)
	}
	var report RebindReport
	for _, r := range rows {
		row := legacyRow(r)
		if !Rebindable(r) {
			if row.Note, err = ReanchorNote(r); err != nil {
				return RebindReport{}, err
			}
			report.NeedsReanchoring = append(report.NeedsReanchoring, row)
			continue
		}
		scfID := r.LegacyScfID
		n, err := q.RebindControlAnchorInPlace(ctx, dbx.RebindControlAnchorInPlaceParams{
			TenantID:    r.TenantID,
			ID:          r.ControlID,
			ScfAnchorID: r.CurrentAnchorID,
			ScfID:       &scfID,
		})
		if err != nil {
			return RebindReport{}, fmt.Errorf("scfimport: rebind control %s: %w", row.ControlID, err)
		}
		if n == 1 {
			report.Rebound = append(report.Rebound, row)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RebindReport{}, fmt.Errorf("scfimport: rebind: commit: %w", err)
	}
	return report, nil
}
