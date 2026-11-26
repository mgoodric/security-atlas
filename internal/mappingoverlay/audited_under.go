package mappingoverlay

// The "audited under" report (#1801, #1735 user stories 17-23): every
// tenant control whose anchor, through catalog plus the tenant's active
// overlay edges, does not reach a criterion the control was tested under
// (its tested_under external references, #1737).
//
// The report is read live on every call, so a new overlay edge or a
// re-anchored control shows at once. It is informational: it lists
// candidate anchors but never changes a control.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// GapCandidate is an SCF anchor a catalog edge joins the criterion to: an
// anchor that would reach it if the control were re-anchored there.
type GapCandidate struct {
	AnchorID         uuid.UUID
	AnchorCode       string
	AnchorTitle      string
	RelationshipType string
	Strength         float64
}

// Gap is one report row: a control and a tested-under criterion its anchor
// does not reach.
type Gap struct {
	ControlID        uuid.UUID
	ControlBundleID  string
	ControlTitle     string
	AnchorFit        string
	AnchorFitNote    string
	AnchorID         *uuid.UUID // nil when the control has no anchor
	AnchorCode       string
	AnchorSCFRelease string
	Framework        string
	FrameworkVersion string
	RequirementID    uuid.UUID
	RequirementCode  string
	RequirementTitle string
	// TestedUnderNote is the reference's note (e.g. "FY25 report s4"): the
	// source an "add overlay edge" from this row pre-fills.
	TestedUnderNote string
	Candidates      []GapCandidate
}

// UnresolvedRef is a tested-under reference that names no requirement in
// its framework's current version (#2324): a typo, a withdrawn code, or a
// framework the catalog does not hold. The report cannot place it, so it
// lists it for the organisation to fix.
type UnresolvedRef struct {
	ControlID       uuid.UUID
	ControlBundleID string
	ControlTitle    string
	System          string
	Code            string
	TestedUnderNote string
}

// AuditedUnderRowCap is the most rows the report returns, and the most
// unresolved references it lists (#2324). A tenant past it gets the first
// rows in report order and Truncated set.
const AuditedUnderRowCap = 5000

// AuditedUnderReport is the report: its rows, the SCF release the candidate
// anchors were drawn from (empty when no SCF release is current, in which
// case candidates come from every release), and the tested-under references
// it could not resolve. GapTotal and UnresolvedTotal count every row before
// the cap; Truncated is set when either list was cut.
type AuditedUnderReport struct {
	SCFRelease      string
	Gaps            []Gap
	GapTotal        int
	Unresolved      []UnresolvedRef
	UnresolvedTotal int
	Truncated       bool
}

// AuditedUnder builds the report for the caller's tenant. framework, when
// set, keeps only that framework's criteria. limit caps each list; a value
// outside 1..AuditedUnderRowCap means AuditedUnderRowCap.
func (s *Store) AuditedUnder(ctx context.Context, framework string, limit int) (AuditedUnderReport, error) {
	if limit < 1 || limit > AuditedUnderRowCap {
		limit = AuditedUnderRowCap
	}
	var out AuditedUnderReport
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListAuditedUnderGaps(ctx, dbx.ListAuditedUnderGapsParams{
			TenantID:      pgUUID(tenantID),
			FrameworkSlug: optional(framework),
			RowLimit:      int32(limit), // #nosec G115 -- bounded by AuditedUnderRowCap above.
		})
		if err != nil {
			return fmt.Errorf("mapping overlay: audited-under gaps: %w", err)
		}
		if len(rows) > 0 {
			out.GapTotal = int(rows[0].Total)
		}
		unresolved, err := q.ListUnresolvedTestedUnderRefs(ctx, dbx.ListUnresolvedTestedUnderRefsParams{
			TenantID:      pgUUID(tenantID),
			FrameworkSlug: optional(framework),
			RowLimit:      int32(limit), // #nosec G115 -- bounded by AuditedUnderRowCap above.
		})
		if err != nil {
			return fmt.Errorf("mapping overlay: unresolved tested-under references: %w", err)
		}
		out.Unresolved = make([]UnresolvedRef, len(unresolved))
		for i, u := range unresolved {
			out.Unresolved[i] = UnresolvedRef{
				ControlID:       uuid.UUID(u.ControlID.Bytes),
				ControlBundleID: u.ControlBundleID,
				ControlTitle:    u.ControlTitle,
				System:          u.RefSystem,
				Code:            u.RefCode,
				TestedUnderNote: u.TestedUnderNote,
			}
			out.UnresolvedTotal = int(u.Total)
		}
		out.Truncated = out.GapTotal > len(rows) || out.UnresolvedTotal > len(unresolved)
		// Candidates are drawn from the current SCF release, the release
		// coverage reads by default (#2203). An empty catalog has none.
		var releaseID pgtype.UUID
		rel, err := scfrelease.Lookup(ctx, q, "")
		switch {
		case err == nil:
			releaseID, out.SCFRelease = rel.ID, rel.Version
		case !errors.Is(err, scfrelease.ErrNoCurrentRelease):
			return fmt.Errorf("mapping overlay: current SCF release: %w", err)
		}
		reqIDs := make([]pgtype.UUID, 0, len(rows))
		seen := map[pgtype.UUID]bool{}
		for _, r := range rows {
			if !seen[r.RequirementID] {
				seen[r.RequirementID] = true
				reqIDs = append(reqIDs, r.RequirementID)
			}
		}
		candidates := map[pgtype.UUID][]GapCandidate{}
		if len(reqIDs) > 0 {
			cands, err := q.ListCatalogAnchorsForRequirements(ctx, dbx.ListCatalogAnchorsForRequirementsParams{
				RequirementIds: reqIDs, ScfReleaseID: releaseID,
			})
			if err != nil {
				return fmt.Errorf("mapping overlay: candidate anchors: %w", err)
			}
			for _, c := range cands {
				candidates[c.FrameworkRequirementID] = append(candidates[c.FrameworkRequirementID], GapCandidate{
					AnchorID:         uuid.UUID(c.ScfAnchorID.Bytes),
					AnchorCode:       c.AnchorCode,
					AnchorTitle:      c.AnchorTitle,
					RelationshipType: string(c.RelationshipType),
					Strength:         c.Strength,
				})
			}
		}
		out.Gaps = make([]Gap, len(rows))
		for i, r := range rows {
			out.Gaps[i] = gapFromRow(r, candidates[r.RequirementID])
		}
		return nil
	})
	return out, err
}

func gapFromRow(r dbx.ListAuditedUnderGapsRow, cands []GapCandidate) Gap {
	g := Gap{
		ControlID:        uuid.UUID(r.ControlID.Bytes),
		ControlBundleID:  r.ControlBundleID,
		ControlTitle:     r.ControlTitle,
		AnchorFit:        r.AnchorFit,
		Framework:        r.FrameworkSlug,
		FrameworkVersion: r.FrameworkVersion,
		RequirementID:    uuid.UUID(r.RequirementID.Bytes),
		RequirementCode:  r.RequirementCode,
		RequirementTitle: r.RequirementTitle,
		TestedUnderNote:  r.TestedUnderNote,
		Candidates:       cands,
	}
	if r.AnchorFitNote != nil {
		g.AnchorFitNote = *r.AnchorFitNote
	}
	if r.ScfAnchorID.Valid {
		id := uuid.UUID(r.ScfAnchorID.Bytes)
		g.AnchorID = &id
	}
	if r.AnchorCode != nil {
		g.AnchorCode = *r.AnchorCode
	}
	if r.AnchorScfRelease != nil {
		g.AnchorSCFRelease = *r.AnchorScfRelease
	}
	return g
}

// AuditedUnderColumns is the report export's CSV header.
var AuditedUnderColumns = []string{
	"framework", "framework_version", "requirement_code", "requirement_title",
	"control_id", "control_bundle_id", "control_title",
	"anchor_code", "anchor_scf_release", "anchor_fit", "anchor_fit_note",
	"tested_under_note", "candidate_anchors", "candidate_scf_release",
}

// AuditedUnderRow renders a gap as the export's CSV cells, in
// AuditedUnderColumns order. Candidate anchors are joined as
// "CODE (relationship strength)" with "; ".
func AuditedUnderRow(g Gap, scfRelease string) []string {
	cands := make([]string, len(g.Candidates))
	for i, c := range g.Candidates {
		cands[i] = fmt.Sprintf("%s (%s %s)", c.AnchorCode, c.RelationshipType, strconv.FormatFloat(c.Strength, 'f', -1, 64))
	}
	return []string{
		g.Framework, g.FrameworkVersion, g.RequirementCode, g.RequirementTitle,
		g.ControlID.String(), g.ControlBundleID, g.ControlTitle,
		g.AnchorCode, g.AnchorSCFRelease, g.AnchorFit, g.AnchorFitNote,
		g.TestedUnderNote, strings.Join(cands, "; "), scfRelease,
	}
}
