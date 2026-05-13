package soc2import

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Report summarizes one import run. Used by the CLI to print a one-line
// summary and by tests to assert idempotency (second-run = all Unchanged).
type Report struct {
	FrameworkSlug      string
	FrameworkVersion   string
	FrameworkID        string
	FrameworkVersionID string
	IsNewVersion       bool

	RequirementsCreated   int
	RequirementsUpdated   int
	RequirementsUnchanged int

	EdgesCreated   int
	EdgesUpdated   int
	EdgesUnchanged int

	// MappingsByAttribution tallies edges by their source_attribution
	// label. Exposed for the HITL audit summary so the orchestrator
	// can read "X community-draft mappings ingested, 0 scf_official"
	// without a second query.
	MappingsByAttribution map[string]int
}

// pgxBeginner is the minimum surface Import needs from the DB handle.
// Both *pgxpool.Pool and *pgx.Conn satisfy it.
type pgxBeginner interface {
	BeginTx(ctx context.Context, txOpts pgx.TxOptions) (pgx.Tx, error)
}

// Import runs the crosswalk against the database in a single transaction.
// Returns a Report on success; rolls back on any error. The DB handle must
// have INSERT/UPDATE rights on frameworks, framework_versions,
// framework_requirements, and fw_to_scf_edges (atlas_migrate role).
func Import(ctx context.Context, db pgxBeginner, cw *Crosswalk) (Report, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, fmt.Errorf("soc2import: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	report, err := importIntoTx(ctx, tx, cw)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("soc2import: commit: %w", err)
	}
	return report, nil
}

func importIntoTx(ctx context.Context, tx pgx.Tx, cw *Crosswalk) (Report, error) {
	q := dbx.New(tx)

	// 1. Upsert the framework row. Deterministic UUID derived from the
	//    framework slug so re-imports never duplicate.
	frameworkID := uuidFromString("framework-" + cw.FrameworkSlug)
	framework, err := q.UpsertFramework(ctx, dbx.UpsertFrameworkParams{
		ID:          frameworkID,
		Name:        cw.FrameworkName,
		Slug:        cw.FrameworkSlug,
		Issuer:      cw.FrameworkIssuer,
		Description: "",
	})
	if err != nil {
		return Report{}, fmt.Errorf("soc2import: upsert framework: %w", err)
	}

	// 2. Decide if this is a new framework_version. Same demotion logic as
	//    slice 006's SCF importer — only one row per framework can be
	//    status='current' at a time.
	versions, err := q.ListFrameworkVersionsBySlug(ctx, cw.FrameworkSlug)
	if err != nil {
		return Report{}, fmt.Errorf("soc2import: list versions: %w", err)
	}
	isNew := true
	for _, v := range versions {
		if v.Version == cw.FrameworkVersion {
			isNew = false
			break
		}
	}
	if isNew {
		if err := q.DemoteCurrentFrameworkVersions(ctx, framework.ID); err != nil {
			return Report{}, fmt.Errorf("soc2import: demote current: %w", err)
		}
	}

	versionID := uuidFromString("fv-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion)
	version, err := q.UpsertFrameworkVersion(ctx, dbx.UpsertFrameworkVersionParams{
		ID:            versionID,
		FrameworkID:   framework.ID,
		Version:       cw.FrameworkVersion,
		EffectiveFrom: parseDate(cw.ReleaseDate),
		EffectiveTo:   pgtype.Date{},
		Status:        dbx.FrameworkVersionStatusCurrent,
	})
	if err != nil {
		return Report{}, fmt.Errorf("soc2import: upsert framework_version: %w", err)
	}

	if err := q.SetLatestVersion(ctx, dbx.SetLatestVersionParams{
		ID:              framework.ID,
		LatestVersionID: pgtype.UUID{Bytes: version.ID.Bytes, Valid: true},
	}); err != nil {
		return Report{}, fmt.Errorf("soc2import: set latest version: %w", err)
	}

	report := Report{
		FrameworkSlug:         cw.FrameworkSlug,
		FrameworkVersion:      cw.FrameworkVersion,
		FrameworkID:           uuidString(framework.ID),
		FrameworkVersionID:    uuidString(version.ID),
		IsNewVersion:          isNew,
		MappingsByAttribution: map[string]int{},
	}

	// 3. Upsert requirement rows. Track ids for the mapping pass.
	reqIDByCode := make(map[string]pgtype.UUID, len(cw.Requirements))
	for _, r := range cw.Requirements {
		reqID := uuidFromString("req-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion + "-" + r.Code)

		existing, lookupErr := q.GetFrameworkRequirementByVersionAndCode(ctx, dbx.GetFrameworkRequirementByVersionAndCodeParams{
			FrameworkVersionID: version.ID,
			Code:               r.Code,
		})
		switch {
		case lookupErr == nil:
			reqIDByCode[r.Code] = existing.ID
			if existing.Title == r.Title && existing.Body == r.Body {
				report.RequirementsUnchanged++
				continue
			}
			if _, err := q.UpsertFrameworkRequirement(ctx, dbx.UpsertFrameworkRequirementParams{
				ID:                 existing.ID,
				FrameworkVersionID: version.ID,
				Code:               r.Code,
				Title:              r.Title,
				Body:               r.Body,
			}); err != nil {
				return Report{}, fmt.Errorf("soc2import: update requirement %s: %w", r.Code, err)
			}
			report.RequirementsUpdated++
		case errors.Is(lookupErr, pgx.ErrNoRows):
			row, err := q.UpsertFrameworkRequirement(ctx, dbx.UpsertFrameworkRequirementParams{
				ID:                 reqID,
				FrameworkVersionID: version.ID,
				Code:               r.Code,
				Title:              r.Title,
				Body:               r.Body,
			})
			if err != nil {
				return Report{}, fmt.Errorf("soc2import: insert requirement %s: %w", r.Code, err)
			}
			reqIDByCode[r.Code] = row.ID
			report.RequirementsCreated++
		default:
			return Report{}, fmt.Errorf("soc2import: lookup requirement %s: %w", r.Code, lookupErr)
		}
	}

	// 4. Keep framework_versions.requirement_count in sync.
	count, err := q.CountFrameworkRequirementsForVersion(ctx, version.ID)
	if err != nil {
		return Report{}, fmt.Errorf("soc2import: count requirements: %w", err)
	}
	if err := q.UpdateFrameworkVersionRequirementCount(ctx, dbx.UpdateFrameworkVersionRequirementCountParams{
		ID:               version.ID,
		RequirementCount: int32(count),
	}); err != nil {
		return Report{}, fmt.Errorf("soc2import: update requirement_count: %w", err)
	}

	// 5. Upsert edges. Resolve scf_anchor strings to anchor IDs against
	//    the current SCF version (slice 006). Unknown anchors are a hard
	//    error — the operator should fix the YAML.
	for _, m := range cw.Mappings {
		reqID, ok := reqIDByCode[m.TSCCode]
		if !ok {
			return Report{}, fmt.Errorf("soc2import: mapping references unknown tsc_code %q after requirement pass", m.TSCCode)
		}
		anchor, err := q.GetSCFAnchorBySCFID(ctx, m.SCFAnchor)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Report{}, fmt.Errorf("soc2import: scf_anchor %q not found — import the SCF catalog first (slice 006)", m.SCFAnchor)
			}
			return Report{}, fmt.Errorf("soc2import: lookup scf_anchor %s: %w", m.SCFAnchor, err)
		}

		attribution := m.SourceAttribution
		if attribution == "" {
			attribution = cw.SourceAttribution
		}
		relType := dbx.StrmRelationshipType(m.RelationshipType)
		srcAttr := dbx.CrosswalkSourceAttribution(attribution)
		report.MappingsByAttribution[attribution]++

		edgeID := uuidFromString("edge-" + cw.FrameworkSlug + "-" + cw.FrameworkVersion + "-" + m.TSCCode + "-" + m.SCFAnchor)

		existing, lookupErr := q.GetFwToScfEdge(ctx, dbx.GetFwToScfEdgeParams{
			FrameworkRequirementID: reqID,
			ScfAnchorID:            anchor.ID,
		})
		switch {
		case lookupErr == nil:
			if edgeContentEqual(existing, relType, m.Strength, srcAttr, m.Rationale) {
				report.EdgesUnchanged++
				continue
			}
			if _, err := q.UpdateFwToScfEdge(ctx, dbx.UpdateFwToScfEdgeParams{
				ID:                existing.ID,
				RelationshipType:  relType,
				Strength:          m.Strength,
				SourceAttribution: srcAttr,
				Rationale:         m.Rationale,
			}); err != nil {
				return Report{}, fmt.Errorf("soc2import: update edge %s→%s: %w", m.TSCCode, m.SCFAnchor, err)
			}
			report.EdgesUpdated++
		case errors.Is(lookupErr, pgx.ErrNoRows):
			if _, err := q.InsertFwToScfEdge(ctx, dbx.InsertFwToScfEdgeParams{
				ID:                     edgeID,
				FrameworkRequirementID: reqID,
				ScfAnchorID:            anchor.ID,
				RelationshipType:       relType,
				Strength:               m.Strength,
				SourceAttribution:      srcAttr,
				Rationale:              m.Rationale,
			}); err != nil {
				return Report{}, fmt.Errorf("soc2import: insert edge %s→%s: %w", m.TSCCode, m.SCFAnchor, err)
			}
			report.EdgesCreated++
		default:
			return Report{}, fmt.Errorf("soc2import: lookup edge %s→%s: %w", m.TSCCode, m.SCFAnchor, lookupErr)
		}
	}

	return report, nil
}

func edgeContentEqual(row dbx.FwToScfEdge, relType dbx.StrmRelationshipType, strength float64, srcAttr dbx.CrosswalkSourceAttribution, rationale string) bool {
	if row.RelationshipType != relType {
		return false
	}
	if row.Strength != strength {
		return false
	}
	if row.SourceAttribution != srcAttr {
		return false
	}
	if row.Rationale != rationale {
		return false
	}
	return true
}

func uuidFromString(seed string) pgtype.UUID {
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("security-atlas/soc2import/"+seed))
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func parseDate(s string) pgtype.Date {
	if s == "" {
		return pgtype.Date{}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t, Valid: true}
}
