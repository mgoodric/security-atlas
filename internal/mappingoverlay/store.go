// Package mappingoverlay manages tenant overlay edges (#1797, the first step
// of #1735): an organisation's own requirement -> SCF anchor edges, stored
// in fw_to_scf_overlay_edges under RLS.
//
// An edge names its requirement by framework version plus code and its
// anchor by SCF release plus code. The anchor goes through the release-aware
// resolver (internal/scfrelease), so an unknown release and a code missing
// from that release stay distinct errors. A pair that already has an active
// catalog edge is refused with that edge named: the overlay would add
// nothing. Frozen framework versions are allowed, because an overlay edge
// never changes a requirement.
//
// Every create and retire writes one fw_to_scf_overlay_edge_audit_log row in
// the same transaction, so a refused or rolled-back change leaves no trail.
//
// Coverage does not read overlay edges yet; that is #1735's coverage-union
// step, a change to effective_fw_to_scf_edges.
package mappingoverlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Status values of an overlay edge.
const (
	StatusActive  = "active"
	StatusRetired = "retired"
)

// Overlay edge errors. The HTTP layer maps each to a status and message.
var (
	// ErrUnknownFrameworkVersion: no catalog framework has that slug and
	// version (or, with no version named, no current version).
	ErrUnknownFrameworkVersion = errors.New("mapping overlay: unknown framework version")
	// ErrUnknownRequirement: the framework version has no requirement with
	// that code.
	ErrUnknownRequirement = errors.New("mapping overlay: requirement not in framework version")
	// ErrDuplicateOverlay: the tenant already has an active overlay edge
	// for this requirement and anchor.
	ErrDuplicateOverlay = errors.New("mapping overlay: an active overlay edge already joins this requirement and anchor")
	// ErrNotFound: the caller's tenant has no overlay edge with that id.
	ErrNotFound = errors.New("mapping overlay: overlay edge not found")
	// ErrAlreadyRetired: the edge is already retired.
	ErrAlreadyRetired = errors.New("mapping overlay: overlay edge is already retired")
)

// InvalidEdgeError is a request that fails the shape rules: a missing
// field, a relationship outside the vocabulary, a strength out of range.
type InvalidEdgeError struct{ Reason string }

func (e *InvalidEdgeError) Error() string {
	return "mapping overlay: invalid overlay edge: " + e.Reason
}

// CatalogEdgeExistsError refuses an overlay edge whose requirement and
// anchor are already joined by an active catalog edge, naming that edge.
type CatalogEdgeExistsError struct {
	EdgeID           uuid.UUID
	RelationshipType string
	MappingTier      string
}

func (e *CatalogEdgeExistsError) Error() string {
	return fmt.Sprintf("mapping overlay: catalog edge %s (%s, %s) already joins this requirement and anchor",
		e.EdgeID, e.RelationshipType, e.MappingTier)
}

// Field limits, mirroring the fw_to_scf_overlay_edges CHECKs so a bad
// request is a 400 naming the rule rather than a constraint name.
const (
	maxRationale = 4000
	maxSourceRef = 1000
	maxReason    = 4000
)

// relationships is the catalog-edge vocabulary an overlay edge may use.
// no_relationship is refused: an overlay edge that asserts no relationship
// would credit nothing.
var relationships = map[dbx.StrmRelationshipType]bool{
	dbx.StrmRelationshipTypeEqual:          true,
	dbx.StrmRelationshipTypeSubsetOf:       true,
	dbx.StrmRelationshipTypeSupersetOf:     true,
	dbx.StrmRelationshipTypeIntersectsWith: true,
}

// NewEdge is the input to Create.
type NewEdge struct {
	// Framework is the framework slug (e.g. "soc2").
	Framework string
	// FrameworkVersion is the version label (e.g. "2017"); empty means the
	// framework's current version.
	FrameworkVersion string
	// RequirementCode is the requirement's code in that version.
	RequirementCode string
	// SCFRelease is the SCF release label; empty means the current release.
	SCFRelease string
	// AnchorCode is the SCF code (e.g. "HRS-07.1") in that release.
	AnchorCode       string
	RelationshipType string
	Strength         float64
	Rationale        string
	SourceRef        string
}

// Edge is an overlay edge with the codes it was named by.
type Edge struct {
	ID                     uuid.UUID
	FrameworkRequirementID uuid.UUID
	SCFAnchorID            uuid.UUID
	Framework              string
	FrameworkVersion       string
	RequirementCode        string
	SCFRelease             string
	AnchorCode             string
	RelationshipType       string
	Strength               float64
	Rationale              string
	SourceRef              string
	Status                 string
	RetirementReason       string
	CreatedBy              string
	CreatedAt              time.Time
	RetiredBy              string
	RetiredAt              *time.Time
}

// Filter narrows List. Every field is optional.
type Filter struct {
	Framework   string
	Requirement string
	Anchor      string
	Status      string
}

// Store reads and writes overlay edges under the caller's tenant.
type Store struct{ pool *pgxpool.Pool }

// NewStore constructs a Store over a pool connected as atlas_app, so RLS
// binds.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Validate checks the shape rules that need no database. Create calls it
// first; it is exported so the HTTP layer and tests can use the same rules.
func (in NewEdge) Validate() error {
	switch {
	case strings.TrimSpace(in.Framework) == "":
		return &InvalidEdgeError{Reason: "framework is required"}
	case strings.TrimSpace(in.RequirementCode) == "":
		return &InvalidEdgeError{Reason: "requirement_code is required"}
	case strings.TrimSpace(in.AnchorCode) == "":
		return &InvalidEdgeError{Reason: "anchor_code is required"}
	case !relationships[dbx.StrmRelationshipType(in.RelationshipType)]:
		return &InvalidEdgeError{Reason: "relationship_type must be one of equal, subset_of, superset_of, intersects_with"}
	case in.Strength < 0 || in.Strength > 1:
		return &InvalidEdgeError{Reason: "strength must be between 0 and 1"}
	case strings.TrimSpace(in.Rationale) == "":
		return &InvalidEdgeError{Reason: "rationale is required"}
	case len(in.Rationale) > maxRationale:
		return &InvalidEdgeError{Reason: fmt.Sprintf("rationale exceeds %d characters", maxRationale)}
	case strings.TrimSpace(in.SourceRef) == "":
		return &InvalidEdgeError{Reason: "source_ref is required"}
	case len(in.SourceRef) > maxSourceRef:
		return &InvalidEdgeError{Reason: fmt.Sprintf("source_ref exceeds %d characters", maxSourceRef)}
	}
	return nil
}

// ValidateStatus checks a list filter's status.
func ValidateStatus(s string) error {
	if s == "" || s == StatusActive || s == StatusRetired {
		return nil
	}
	return &InvalidEdgeError{Reason: "status must be active or retired"}
}

// Create adds an overlay edge for the caller's tenant.
func (s *Store) Create(ctx context.Context, in NewEdge, actor string) (Edge, error) {
	if err := in.Validate(); err != nil {
		return Edge{}, err
	}
	if actor == "" {
		return Edge{}, errors.New("mapping overlay: create has no actor")
	}
	var out Edge
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		req, err := resolveRequirement(ctx, q, in.Framework, in.FrameworkVersion, in.RequirementCode)
		if err != nil {
			return err
		}
		anchor, _, err := scfrelease.Resolve(ctx, q, in.SCFRelease, in.AnchorCode)
		if err != nil {
			return err
		}
		if err := refuseCatalogDuplicate(ctx, q, req.ID, anchor.ID); err != nil {
			return err
		}
		row, err := q.InsertOverlayEdge(ctx, dbx.InsertOverlayEdgeParams{
			TenantID:               pgUUID(tenantID),
			FrameworkRequirementID: req.ID,
			ScfAnchorID:            anchor.ID,
			RelationshipType:       dbx.StrmRelationshipType(in.RelationshipType),
			Strength:               in.Strength,
			Rationale:              in.Rationale,
			SourceRef:              in.SourceRef,
			CreatedBy:              actor,
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrDuplicateOverlay
			}
			return fmt.Errorf("mapping overlay: insert: %w", err)
		}
		out, err = getView(ctx, q, tenantID, row.ID)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, tenantID, "create", nil, &out, actor)
	})
	return out, err
}

// List returns the caller's overlay edges, newest first.
func (s *Store) List(ctx context.Context, f Filter) ([]Edge, error) {
	if err := ValidateStatus(f.Status); err != nil {
		return nil, err
	}
	var out []Edge
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListOverlayEdges(ctx, dbx.ListOverlayEdgesParams{
			TenantID:        pgUUID(tenantID),
			FrameworkSlug:   optional(f.Framework),
			RequirementCode: optional(f.Requirement),
			AnchorCode:      optional(f.Anchor),
			Status:          optional(f.Status),
		})
		if err != nil {
			return fmt.Errorf("mapping overlay: list: %w", err)
		}
		out = make([]Edge, len(rows))
		for i, r := range rows {
			out[i] = edgeFromView(dbx.GetOverlayEdgeViewRow(r))
		}
		return nil
	})
	return out, err
}

// Retire marks an active overlay edge retired with a reason. The row stays.
func (s *Store) Retire(ctx context.Context, id uuid.UUID, reason, actor string) (Edge, error) {
	if strings.TrimSpace(reason) == "" {
		return Edge{}, &InvalidEdgeError{Reason: "reason is required"}
	}
	if len(reason) > maxReason {
		return Edge{}, &InvalidEdgeError{Reason: fmt.Sprintf("reason exceeds %d characters", maxReason)}
	}
	if actor == "" {
		return Edge{}, errors.New("mapping overlay: retire has no actor")
	}
	var out Edge
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		cur, err := q.LockOverlayEdge(ctx, dbx.LockOverlayEdgeParams{TenantID: pgUUID(tenantID), ID: pgUUID(id)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("mapping overlay: lock: %w", err)
		}
		if cur.Status == StatusRetired {
			return ErrAlreadyRetired
		}
		before, err := getView(ctx, q, tenantID, cur.ID)
		if err != nil {
			return err
		}
		if _, err := q.RetireOverlayEdge(ctx, dbx.RetireOverlayEdgeParams{
			Reason: reason, Actor: actor, TenantID: pgUUID(tenantID), ID: cur.ID,
		}); err != nil {
			return fmt.Errorf("mapping overlay: retire: %w", err)
		}
		out, err = getView(ctx, q, tenantID, cur.ID)
		if err != nil {
			return err
		}
		return writeAudit(ctx, q, tenantID, "retire", &before, &out, actor)
	})
	return out, err
}

// resolveRequirement finds the requirement by framework slug, version label
// (empty: the current version) and code, in the global catalog.
func resolveRequirement(ctx context.Context, q *dbx.Queries, framework, version, code string) (dbx.FrameworkRequirement, error) {
	versions, err := q.ListFrameworkVersionsBySlug(ctx, framework)
	if err != nil {
		return dbx.FrameworkRequirement{}, fmt.Errorf("mapping overlay: list framework versions: %w", err)
	}
	var versionID pgtype.UUID
	for _, v := range versions {
		if (version == "" && v.Status == dbx.FrameworkVersionStatusCurrent) || (version != "" && v.Version == version) {
			versionID = v.ID
			break
		}
	}
	if !versionID.Valid {
		label := version
		if label == "" {
			label = "current"
		}
		return dbx.FrameworkRequirement{}, fmt.Errorf("%w: %s %s", ErrUnknownFrameworkVersion, framework, label)
	}
	req, err := q.GetFrameworkRequirementByVersionAndCode(ctx, dbx.GetFrameworkRequirementByVersionAndCodeParams{
		FrameworkVersionID: versionID, Code: code,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.FrameworkRequirement{}, fmt.Errorf("%w: %q", ErrUnknownRequirement, code)
	}
	if err != nil {
		return dbx.FrameworkRequirement{}, fmt.Errorf("mapping overlay: get requirement: %w", err)
	}
	return req, nil
}

// refuseCatalogDuplicate refuses a pair an active catalog edge already
// joins. A catalog edge a reviewer rejected (ADR 0018) is not active, so an
// organisation may still assert the pair itself.
func refuseCatalogDuplicate(ctx context.Context, q *dbx.Queries, reqID, anchorID pgtype.UUID) error {
	edge, err := q.GetFwToScfEdge(ctx, dbx.GetFwToScfEdgeParams{
		FrameworkRequirementID: reqID, ScfAnchorID: anchorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mapping overlay: read catalog edge: %w", err)
	}
	if edge.MappingTier == dbx.CrosswalkMappingTierRejected {
		return nil
	}
	return &CatalogEdgeExistsError{
		EdgeID:           uuid.UUID(edge.ID.Bytes),
		RelationshipType: string(edge.RelationshipType),
		MappingTier:      string(edge.MappingTier),
	}
}

func getView(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, id pgtype.UUID) (Edge, error) {
	row, err := q.GetOverlayEdgeView(ctx, dbx.GetOverlayEdgeViewParams{TenantID: pgUUID(tenantID), ID: id})
	if err != nil {
		return Edge{}, fmt.Errorf("mapping overlay: read edge: %w", err)
	}
	return edgeFromView(row), nil
}

func edgeFromView(r dbx.GetOverlayEdgeViewRow) Edge {
	e := Edge{
		ID:                     uuid.UUID(r.ID.Bytes),
		FrameworkRequirementID: uuid.UUID(r.FrameworkRequirementID.Bytes),
		SCFAnchorID:            uuid.UUID(r.ScfAnchorID.Bytes),
		Framework:              r.FrameworkSlug,
		FrameworkVersion:       r.FrameworkVersion,
		RequirementCode:        r.RequirementCode,
		SCFRelease:             r.ScfRelease,
		AnchorCode:             r.AnchorCode,
		RelationshipType:       string(r.RelationshipType),
		Strength:               r.Strength,
		Rationale:              r.Rationale,
		SourceRef:              r.SourceRef,
		Status:                 r.Status,
		CreatedBy:              r.CreatedBy,
		CreatedAt:              r.CreatedAt.Time,
	}
	if r.RetirementReason != nil {
		e.RetirementReason = *r.RetirementReason
	}
	if r.RetiredBy != nil {
		e.RetiredBy = *r.RetiredBy
	}
	if r.RetiredAt.Valid {
		t := r.RetiredAt.Time
		e.RetiredAt = &t
	}
	return e
}

// auditState is an overlay edge as the audit trail records it.
type auditState struct {
	Framework        string  `json:"framework"`
	FrameworkVersion string  `json:"framework_version"`
	RequirementCode  string  `json:"requirement_code"`
	SCFRelease       string  `json:"scf_release"`
	AnchorCode       string  `json:"anchor_code"`
	RelationshipType string  `json:"relationship_type"`
	Strength         float64 `json:"strength"`
	Rationale        string  `json:"rationale"`
	SourceRef        string  `json:"source_ref"`
	Status           string  `json:"status"`
	RetirementReason string  `json:"retirement_reason,omitempty"`
}

func auditJSON(e *Edge) ([]byte, error) {
	if e == nil {
		return nil, nil
	}
	return json.Marshal(auditState{
		Framework: e.Framework, FrameworkVersion: e.FrameworkVersion, RequirementCode: e.RequirementCode,
		SCFRelease: e.SCFRelease, AnchorCode: e.AnchorCode, RelationshipType: e.RelationshipType,
		Strength: e.Strength, Rationale: e.Rationale, SourceRef: e.SourceRef, Status: e.Status,
		RetirementReason: e.RetirementReason,
	})
}

func writeAudit(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, action string, before, after *Edge, actor string) error {
	b, err := auditJSON(before)
	if err != nil {
		return fmt.Errorf("mapping overlay: marshal audit before: %w", err)
	}
	a, err := auditJSON(after)
	if err != nil {
		return fmt.Errorf("mapping overlay: marshal audit after: %w", err)
	}
	if err := q.InsertOverlayEdgeAuditLog(ctx, dbx.InsertOverlayEdgeAuditLogParams{
		TenantID:      pgUUID(tenantID),
		OverlayEdgeID: pgUUID(after.ID),
		Action:        action,
		Before:        b,
		After:         a,
		Actor:         actor,
	}); err != nil {
		return fmt.Errorf("mapping overlay: write audit log: %w", err)
	}
	return nil
}

func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("mapping overlay: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mapping overlay: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, dbx.New(tx), tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mapping overlay: commit: %w", err)
	}
	return nil
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
