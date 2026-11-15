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

// RemapClassFor classifies one row of ListControlsOnLegacySCFAnchors and
// returns the successor codes it proposes, sorted.
//
// A current anchor continues the legacy anchor when SCF lists the legacy
// code as its legacy ID, or when it has the same code and lists no other
// code as its legacy ID. A release with no legacy IDs at all gives no
// lineage, so every row is no_successor.
func RemapClassFor(r dbx.ListControlsOnLegacySCFAnchorsRow) (RemapClass, []string) {
	if !r.CurrentReleaseHasLegacyIds {
		return RemapNoSuccessor, []string{}
	}
	codes := append([]string{}, r.LegacySuccessorScfIds...)
	titles := append([]string{}, r.LegacySuccessorTitles...)
	sameCode := r.CurrentAnchorID.Valid
	if sameCode && r.CurrentAnchorLegacyScfID != "" && r.CurrentAnchorLegacyScfID != r.LegacyScfID {
		return RemapReusedID, codes
	}
	if sameCode && r.CurrentAnchorLegacyScfID == "" {
		// The same code with no legacy ID of its own continues itself. When
		// it lists the legacy code it is already among the legacy successors.
		codes = insertSorted(codes, r.LegacyScfID)
		title := ""
		if r.CurrentAnchorTitle != nil {
			title = *r.CurrentAnchorTitle
		}
		titles = append(titles, title)
	}
	switch len(codes) {
	case 0:
		return RemapNoSuccessor, codes
	case 1:
		if titles[0] == r.LegacyAnchorTitle {
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
