package frameworkscope

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Statement of Applicability export (issue #1791, spec #1744). The export
// covers every requirement of a framework version with its decision on one
// scope revision: the activated revision by default, a named revision, or the
// revision in force at a point in time. Encoding to CSV or xlsx is the HTTP
// layer's job; this file assembles the rows and the title.

var (
	// ErrFrameworkVersionNotFound is returned when the framework version is
	// not visible to the caller tenant. HTTP 404.
	ErrFrameworkVersionNotFound = errors.New("frameworkscope: framework version not found")
	// ErrRevisionMismatch is returned when ?revision= names a scope revision
	// of a different framework version. HTTP 400.
	ErrRevisionMismatch = errors.New("frameworkscope: revision belongs to a different framework version")
)

// isoSlug is the catalog slug of ISO/IEC 27001, whose export is titled
// "Statement of Applicability".
const isoSlug = "iso27001"

// Export titles.
const (
	TitleSoA           = "Statement of Applicability"
	TitleApplicability = "Applicability"
)

// SoASelector picks the revision an export reads. With neither field set the
// activated revision is used. Setting both is a caller error.
type SoASelector struct {
	Revision *uuid.UUID
	AsOf     *time.Time
}

// SoAExport is everything an export file carries. Applicability.Scope is nil
// when no revision is selected (no activated revision, or none in force at
// as_of); every requirement is then undecided.
type SoAExport struct {
	FrameworkName    string
	FrameworkSlug    string
	FrameworkVersion string
	Applicability    Applicability
	// Approver is the actor of the selected revision's current approval,
	// from the workflow log; empty when the revision is not approved.
	Approver string
	// LinkedControls lists, per requirement, the bundle ids of the active
	// tenant controls that reach it through the crosswalk.
	LinkedControls map[uuid.UUID][]string
}

// Title is "Statement of Applicability" for ISO/IEC 27001 and
// "Applicability" for every other framework.
func (e SoAExport) Title() string {
	if e.FrameworkSlug == isoSlug {
		return TitleSoA
	}
	return TitleApplicability
}

// Subtitle names the framework and version, e.g. "ISO/IEC 27001 2022".
func (e SoAExport) Subtitle() string {
	return e.FrameworkName + " " + e.FrameworkVersion
}

// SoAHeader is the export's column row. The first four names match the
// decision import's columns (#1790).
var SoAHeader = []string{
	"requirement_code",
	"requirement_title",
	"decision",
	"justification",
	"implementation_ref",
	"linked_controls",
	"contradiction",
	"scope_revision",
	"approver",
	"approved_at",
}

// Rows renders one row per requirement, in requirement-code order, aligned
// with SoAHeader. An undecided requirement shows `undecided`. Cells are raw
// text; formula neutralisation is the encoder's concern.
func (e SoAExport) Rows() [][]string {
	var revision, approver, approvedAt string
	if sc := e.Applicability.Scope; sc != nil {
		revision = sc.ID.String()
		approver = e.Approver
		if sc.ApprovedAt != nil {
			approvedAt = sc.ApprovedAt.UTC().Format(time.RFC3339)
		}
	}
	rows := make([][]string, 0, len(e.Applicability.Requirements))
	for _, r := range e.Applicability.Requirements {
		decision, justification, implRef := FilterUndecided, "", ""
		if d := r.Decision; d != nil {
			decision, justification, implRef = d.Decision, d.Justification, d.ImplementationRef
		}
		rows = append(rows, []string{
			r.Code,
			r.Title,
			decision,
			justification,
			implRef,
			strings.Join(e.LinkedControls[r.RequirementID], "; "),
			strconv.FormatBool(r.Contradiction),
			revision,
			approver,
			approvedAt,
		})
	}
	return rows
}

// SoAExport assembles the export for a framework version from the revision
// sel picks.
func (s *Store) SoAExport(ctx context.Context, frameworkVersionID uuid.UUID, sel SoASelector) (SoAExport, error) {
	var out SoAExport
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		label, err := q.GetFrameworkVersionLabel(ctx, dbx.GetFrameworkVersionLabelParams{
			FrameworkVersionID: pgUUID(frameworkVersionID),
			TenantID:           pgUUID(tenantID),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrFrameworkVersionNotFound
			}
			return fmt.Errorf("get framework version: %w", err)
		}
		out.FrameworkName, out.FrameworkSlug, out.FrameworkVersion = label.FrameworkName, label.FrameworkSlug, label.FrameworkVersion

		sc, err := selectRevision(ctx, q, tenantID, frameworkVersionID, sel)
		if err != nil {
			return err
		}
		if out.Applicability, err = listApplicability(ctx, q, tenantID, frameworkVersionID, sc); err != nil {
			return err
		}
		if sc != nil && sc.ApprovedAt != nil {
			// approver_user_id is set only for UUID credential ids; the
			// workflow log records every approval's actor.
			if out.Approver, err = q.GetFrameworkScopeLatestApprover(ctx, dbx.GetFrameworkScopeLatestApproverParams{
				TenantID:         pgUUID(tenantID),
				FrameworkScopeID: pgUUID(sc.ID),
			}); err != nil {
				return fmt.Errorf("get approver: %w", err)
			}
		}

		links, err := q.ListFrameworkVersionLinkedControls(ctx, dbx.ListFrameworkVersionLinkedControlsParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(frameworkVersionID),
		})
		if err != nil {
			return fmt.Errorf("list linked controls: %w", err)
		}
		out.LinkedControls = make(map[uuid.UUID][]string)
		for _, l := range links {
			id := uuid.UUID(l.RequirementID.Bytes)
			out.LinkedControls[id] = append(out.LinkedControls[id], l.BundleID)
		}
		return nil
	})
	return out, err
}

// selectRevision resolves sel to a scope revision of the framework version,
// or nil when none is in force.
func selectRevision(ctx context.Context, q *dbx.Queries, tenantID, frameworkVersionID uuid.UUID, sel SoASelector) (*FrameworkScope, error) {
	var (
		row dbx.FrameworkScope
		err error
	)
	switch {
	case sel.Revision != nil:
		row, err = q.GetFrameworkScopeByID(ctx, dbx.GetFrameworkScopeByIDParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(*sel.Revision),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
	case sel.AsOf != nil:
		row, err = q.GetFrameworkScopeAsOf(ctx, dbx.GetFrameworkScopeAsOfParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(frameworkVersionID),
			EffectiveFrom:      pgtype.Timestamptz{Time: *sel.AsOf, Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
	default:
		row, err = q.GetActivatedFrameworkScope(ctx, dbx.GetActivatedFrameworkScopeParams{
			TenantID:           pgUUID(tenantID),
			FrameworkVersionID: pgUUID(frameworkVersionID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("select revision: %w", err)
	}
	sc := rowToScope(row)
	if sc.FrameworkVersionID != frameworkVersionID {
		return nil, ErrRevisionMismatch
	}
	return &sc, nil
}
