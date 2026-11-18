package scfimport

// The SCF remap queue (#1775).
//
// After a new SCF release becomes current, every non-pinned control left on
// an anchor of an older release gets one proposal in scf_remap_proposals.
// The proposal reads the new release's legacy IDs (#1769) to name the
// successor anchors, and RemapClass sorts it into one of five classes. The
// proposal itself moves nothing: whether the control moved is the #1971 D3
// rule's call (Classify), recorded as the proposal's status.

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// RemapClass is how a legacy anchor's concept carries into the current
// release, judged by SCF's legacy IDs.
type RemapClass string

const (
	// RemapUnchanged: one successor, with the legacy anchor's title.
	RemapUnchanged RemapClass = "unchanged"
	// RemapRenamed: one successor, with a different title.
	RemapRenamed RemapClass = "renamed"
	// RemapSplit: several successors.
	RemapSplit RemapClass = "split"
	// RemapReusedID: the current release has the legacy code, but its
	// legacy ID names a different code, so the code now carries another
	// control's lineage.
	RemapReusedID RemapClass = "reused_id"
	// RemapNoSuccessor: nothing in the current release continues the
	// anchor, or the release carries no legacy IDs to tell.
	RemapNoSuccessor RemapClass = "no_successor"
)

// RemapClasses lists every class in the order the import summary prints
// them.
var RemapClasses = []RemapClass{RemapUnchanged, RemapRenamed, RemapSplit, RemapReusedID, RemapNoSuccessor}

// Proposal statuses: what the D3 upgrade did with the control.
const (
	// ProposalOpen: the control is still on its legacy anchor.
	ProposalOpen = "open"
	// ProposalRebound: the upgrade moved the control in place (same code,
	// same title), so the proposal is a record, not a to-do.
	ProposalRebound = "rebound"
)

// Proposal sources: which path re-bound or flagged the control (#2236).
const (
	// SourceBoot: RebindControls, during `atlas-cli catalog import-scf`.
	SourceBoot = "boot"
	// SourceUpload: control.Store.Upload, re-binding or flagging an
	// unchanged manifest in place.
	SourceUpload = "upload"
)

// RecordRemapProposal writes the durable record of what a re-bind did with
// one control on a legacy anchor (#2236): the tenant, the control, the
// legacy anchor and release, the target release, the anchor it moved to
// (for status rebound; none for open), and the path that did it. Call it in
// the transaction that moves or flags the control. A record for the same
// control and target release is left alone, except that an open one becomes
// rebound when this call re-bound the control. It returns the proposal's
// class and whether this call wrote a row (#2325: false on a re-run that
// found the record already there).
func RecordRemapProposal(ctx context.Context, q *dbx.Queries, r dbx.ListControlsOnLegacySCFAnchorsRow, status, source string) (RemapClass, bool, error) {
	class, successors := RemapClassFor(r)
	var target pgtype.UUID
	if status == ProposalRebound {
		target = r.CurrentAnchorID
	}
	n, err := q.InsertSCFRemapProposal(ctx, dbx.InsertSCFRemapProposalParams{
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
		TargetAnchorID:           target,
		Source:                   source,
	})
	if err != nil {
		return "", false, fmt.Errorf("scfimport: queue remap proposal for control %s: %w", uuid.UUID(r.ControlID.Bytes), err)
	}
	return class, n == 1, nil
}

// RemapClassFor classifies one row of ListControlsOnLegacySCFAnchors and
// returns the successor codes it proposes, sorted.
func RemapClassFor(r dbx.ListControlsOnLegacySCFAnchorsRow) (RemapClass, []string) {
	return ClassifyLineage(AnchorLineage{
		LegacySCFID:                r.LegacyScfID,
		LegacyAnchorTitle:          r.LegacyAnchorTitle,
		HasCurrentAnchor:           r.CurrentAnchorID.Valid,
		CurrentAnchorTitle:         r.CurrentAnchorTitle,
		CurrentAnchorLegacySCFID:   r.CurrentAnchorLegacyScfID,
		LegacySuccessorSCFIDs:      r.LegacySuccessorScfIds,
		LegacySuccessorTitles:      r.LegacySuccessorTitles,
		CurrentReleaseHasLegacyIDs: r.CurrentReleaseHasLegacyIds,
	})
}

// AnchorLineage is what the current release says about one legacy anchor:
// the anchor with the same code, if any, and the anchors that list the
// legacy code as their legacy ID. Control proposals (#1775) and crosswalk
// edge proposals (#1777) both classify from it, so the two queues cannot
// disagree about an anchor.
type AnchorLineage struct {
	LegacySCFID       string
	LegacyAnchorTitle string
	// HasCurrentAnchor: the current release has an anchor with the legacy
	// code; CurrentAnchorTitle and CurrentAnchorLegacySCFID describe it
	// ("" when it lists no legacy ID).
	HasCurrentAnchor         bool
	CurrentAnchorTitle       *string
	CurrentAnchorLegacySCFID string
	// The current anchors whose legacy ID is the legacy code, codes and
	// titles in the same order.
	LegacySuccessorSCFIDs      []string
	LegacySuccessorTitles      []string
	CurrentReleaseHasLegacyIDs bool
}

// ClassifyLineage classifies a legacy anchor and returns the successor
// codes it proposes, sorted.
//
// A current anchor continues the legacy anchor when SCF lists the legacy
// code as its legacy ID, or when it has the same code and lists no other
// code as its legacy ID. A release with no legacy IDs at all gives no
// lineage, so every anchor is no_successor.
func ClassifyLineage(l AnchorLineage) (RemapClass, []string) {
	if !l.CurrentReleaseHasLegacyIDs {
		return RemapNoSuccessor, []string{}
	}
	codes := append([]string{}, l.LegacySuccessorSCFIDs...)
	titles := append([]string{}, l.LegacySuccessorTitles...)
	sameCode := l.HasCurrentAnchor
	if sameCode && l.CurrentAnchorLegacySCFID != "" && l.CurrentAnchorLegacySCFID != l.LegacySCFID {
		return RemapReusedID, codes
	}
	if sameCode && l.CurrentAnchorLegacySCFID == "" {
		// The same code with no legacy ID of its own continues itself. When
		// it lists the legacy code it is already among the legacy successors.
		codes = insertSorted(codes, l.LegacySCFID)
		title := ""
		if l.CurrentAnchorTitle != nil {
			title = *l.CurrentAnchorTitle
		}
		titles = append(titles, title)
	}
	switch len(codes) {
	case 0:
		return RemapNoSuccessor, codes
	case 1:
		if titles[0] == l.LegacyAnchorTitle {
			return RemapUnchanged, codes
		}
		return RemapRenamed, codes
	default:
		return RemapSplit, codes
	}
}

func insertSorted(codes []string, code string) []string {
	i := 0
	for i < len(codes) && codes[i] < code {
		i++
	}
	codes = append(codes, "")
	copy(codes[i+1:], codes[i:])
	codes[i] = code
	return codes
}
