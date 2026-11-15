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
//
// One exception (#2321, Matt's call, 2026-11-15): a stock control, one whose
// stored control.yaml is byte-identical to a revision the platform shipped
// under controls/soc2/, also moves when the code is unchanged and only its
// title differs. The platform chose that anchor, and the code still names
// the same concept. Tenant-authored controls keep the D3 rule.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

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
	// code and title, or with the same code when the control is stock
	// (#2321).
	Rebound []ControlOnLegacyAnchor
	// NeedsReanchoring controls stay on their legacy anchor: the code is
	// gone from the current release or now names a different control.
	NeedsReanchoring []ControlOnLegacyAnchor
	// Pinned controls declare scf_release (#1770) in their bundle, so they
	// are bound to that release on purpose and are never moved.
	Pinned []ControlOnLegacyAnchor
	// SupersededMidRun counts rebindable controls a concurrent upload
	// superseded between the listing and the update. They are not moved
	// here; the new version bound its anchor on upload.
	SupersededMidRun int
	// Proposals counts the remap proposals (#1775) of this run by class:
	// one per non-pinned control above, whether it was re-bound (status
	// rebound) or still needs re-anchoring (status open). A control
	// superseded mid-run gets none.
	Proposals map[RemapClass]int
}

// IsPinned reports whether a stored control.yaml binds its anchor to one SCF
// release on purpose, so no re-bind may move it (#1770, #1971 D3). Two
// forms pin: a declared scf_release, and an scf_anchor_id written as an
// anchor UUID, which scfrelease.Resolve binds to that exact row in whichever
// release holds it. parsed is false when the manifest does not decode; the
// caller must not move such a control either.
func IsPinned(manifestYAML string) (pinned, parsed bool) {
	var doc struct {
		SCFRelease  string `yaml:"scf_release"`
		SCFAnchorID string `yaml:"scf_anchor_id"`
	}
	if err := yaml.Unmarshal([]byte(manifestYAML), &doc); err != nil {
		return false, false
	}
	if strings.TrimSpace(doc.SCFRelease) != "" {
		return true, true
	}
	_, isUUID := uuid.Parse(strings.TrimSpace(doc.SCFAnchorID))
	return isUUID == nil, true
}

// unparseableNote is the re-anchoring note for a control whose stored
// control.yaml does not decode: its pin cannot be read, so it is not moved.
const unparseableNote = "stored control.yaml does not parse, so its SCF release pin cannot be read; re-upload the bundle"

// Rebindable reports whether a control on a legacy anchor can move to the
// current release's anchor without changing what it is anchored to: the
// same code must exist with the same title.
func Rebindable(r dbx.ListControlsOnLegacySCFAnchorsRow) bool {
	return r.CurrentAnchorID.Valid && r.CurrentAnchorTitle != nil && *r.CurrentAnchorTitle == r.LegacyAnchorTitle
}

// IsStockManifest reports whether a stored control.yaml is one the platform
// shipped for bundleID: its sha256 is a revision of controls/soc2/ (#2321).
// Content, not a field, decides it, so a tenant cannot mark a control of its
// own as stock; uploading a shipped manifest unchanged is uploading the stock
// control.
func IsStockManifest(bundleID, manifestYAML string) bool {
	sum := sha256.Sum256([]byte(manifestYAML))
	id, ok := stockManifestHashes[hex.EncodeToString(sum[:])]
	return ok && id == bundleID
}

// StockTitleOnlyRebindable reports whether a stock control on a legacy
// anchor can move to the current release's anchor with the same code whose
// title changed (#2321). The code must still continue the legacy anchor: SCF
// lists no other code as its legacy ID, and the bundled remap table moves
// nothing off it. A code reused for another control's lineage, or remapped
// elsewhere, is not a title-only change.
func StockTitleOnlyRebindable(r dbx.ListControlsOnLegacySCFAnchorsRow) (bool, error) {
	if !r.CurrentAnchorID.Valid || r.CurrentAnchorTitle == nil {
		return false, nil
	}
	if r.CurrentAnchorLegacyScfID != "" && r.CurrentAnchorLegacyScfID != r.LegacyScfID {
		return false, nil
	}
	if _, remapped, err := scfremap.Lookup(r.LegacyRelease, r.LegacyScfID); err != nil || remapped {
		return false, err
	}
	return IsStockManifest(r.BundleID, r.BundleManifestYaml), nil
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

// Outcome is what the D3 rule makes of one control on a legacy anchor.
type Outcome int

const (
	// outcomeUnknown is the zero value, returned only with an error.
	outcomeUnknown Outcome = iota
	// OutcomeRebindable: the current release has the same code with the
	// same title, or the control is stock and only the title changed
	// (#2321), so the control can move there in place.
	OutcomeRebindable
	// OutcomeNeedsReanchoring: the code is gone or now means something
	// else, or the stored manifest does not parse; a person re-anchors it.
	OutcomeNeedsReanchoring
	// OutcomePinned: the bundle binds its anchor to one release on purpose
	// (#1770), so it is neither moved nor flagged.
	OutcomePinned
)

// Classify applies the D3 rule to one row of ListControlsOnLegacySCFAnchors.
// The upgrade (RebindControls) and the tenant read (GET
// /v1/controls/needs-reanchoring, #2176) both call it, so the dashboard
// warning and the upgrade output cannot disagree about which controls need
// a person. The returned row carries a Note only for
// OutcomeNeedsReanchoring.
func Classify(r dbx.ListControlsOnLegacySCFAnchorsRow) (ControlOnLegacyAnchor, Outcome, error) {
	row := legacyRow(r)
	// A bundle pinned to an SCF release (#1770) is bound there on purpose:
	// moving it would be the silent re-point D3 forbids. A manifest whose
	// pin cannot be read is not moved either.
	pinned, parsed := IsPinned(r.BundleManifestYaml)
	switch {
	case !parsed:
		row.Note = unparseableNote
		return row, OutcomeNeedsReanchoring, nil
	case pinned:
		return row, OutcomePinned, nil
	case Rebindable(r):
		return row, OutcomeRebindable, nil
	}
	stock, err := StockTitleOnlyRebindable(r)
	if err != nil {
		return ControlOnLegacyAnchor{}, outcomeUnknown, err
	}
	if stock {
		return row, OutcomeRebindable, nil
	}
	note, err := ReanchorNote(r)
	if err != nil {
		return ControlOnLegacyAnchor{}, outcomeUnknown, err
	}
	row.Note = note
	return row, OutcomeNeedsReanchoring, nil
}

// NeedsReanchoring returns the rows the D3 rule flags for a person, in
// input order, each with its note. Pinned and rebindable rows are left out.
func NeedsReanchoring(rows []dbx.ListControlsOnLegacySCFAnchorsRow) ([]ControlOnLegacyAnchor, error) {
	out := []ControlOnLegacyAnchor{}
	for _, r := range rows {
		row, outcome, err := Classify(r)
		if err != nil {
			return nil, err
		}
		if outcome == OutcomeNeedsReanchoring {
			out = append(out, row)
		}
	}
	return out, nil
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
	report := RebindReport{Proposals: map[RemapClass]int{}}
	// queue records the #1775 remap proposal for a control the D3 rule did
	// not leave pinned.
	queue := func(r dbx.ListControlsOnLegacySCFAnchorsRow, status string) error {
		class, successors := RemapClassFor(r)
		if _, err := q.InsertSCFRemapProposal(ctx, dbx.InsertSCFRemapProposalParams{
			TenantID:                 r.TenantID,
			ControlID:                r.ControlID,
			BundleID:                 r.BundleID,
			LegacyAnchorID:           r.LegacyAnchorID,
			LegacyScfID:              r.LegacyScfID,
			LegacyRelease:            r.LegacyRelease,
			TargetFrameworkVersionID: r.CurrentFrameworkVersionID,
			TargetRelease:            r.CurrentRelease,
			Class:                    string(class),
			Status:                   status,
			SuccessorScfIds:          successors,
		}); err != nil {
			return fmt.Errorf("scfimport: queue remap proposal for control %s: %w", uuid.UUID(r.ControlID.Bytes), err)
		}
		report.Proposals[class]++
		return nil
	}
	for _, r := range rows {
		row, outcome, err := Classify(r)
		if err != nil {
			return RebindReport{}, err
		}
		switch outcome {
		case OutcomePinned:
			report.Pinned = append(report.Pinned, row)
			continue
		case OutcomeNeedsReanchoring:
			report.NeedsReanchoring = append(report.NeedsReanchoring, row)
			if err := queue(r, ProposalOpen); err != nil {
				return RebindReport{}, err
			}
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
			if err := queue(r, ProposalRebound); err != nil {
				return RebindReport{}, err
			}
		} else {
			// Superseded by a concurrent upload between the list and the
			// update; the new version binds through the upload path.
			report.SupersededMidRun++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RebindReport{}, fmt.Errorf("scfimport: rebind: commit: %w", err)
	}
	return report, nil
}
