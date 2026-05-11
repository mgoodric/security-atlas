// Package scfimport ingests the Secure Controls Framework JSON catalog
// into Postgres. The catalog JSON shape is documented in the Catalog type
// below — operators convert SCF's Excel/JSON release into this shape.
//
// The importer is idempotent: re-running with the same release_version
// upserts in place (no duplicates); a new release_version creates a new
// framework_versions row, demotes the previous "current" → "legacy", and
// inserts a fresh set of anchors against the new version. Old versions
// stay queryable for audit replay.
package scfimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// SchemaVersion is the importer's accepted JSON schema. Bumped when the
// shape changes incompatibly so converters can refuse to import.
const SchemaVersion = "1.0"

// Catalog is the JSON shape the importer parses. Documented for community
// converters; treat as a stable v1 contract.
type Catalog struct {
	SchemaVersion  string    `json:"schema_version"`
	ReleaseVersion string    `json:"release_version"`
	ReleaseDate    string    `json:"release_date"`
	Controls       []Control `json:"controls"`
}

// Control is one SCF anchor's record in the catalog JSON.
type Control struct {
	SCFID       string     `json:"scf_id"`
	Family      string     `json:"family"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Subtopics   []Subtopic `json:"subtopics,omitempty"`
}

// Subtopic mirrors the SCF sub-control structure. Persisted as JSONB.
type Subtopic struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Report summarizes one import run. ISC-21 + AC-6.
type Report struct {
	ReleaseVersion     string
	FrameworkID        string
	FrameworkVersionID string
	IsNewVersion       bool
	Created            int
	Updated            int
	Unchanged          int
}

// Load reads a JSON catalog file from disk.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("scfimport: read %s: %w", path, err)
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("scfimport: parse %s: %w", path, err)
	}
	if err := validate(&cat); err != nil {
		return nil, err
	}
	return &cat, nil
}

func validate(cat *Catalog) error {
	if cat.SchemaVersion != SchemaVersion {
		return fmt.Errorf("scfimport: unsupported schema_version %q (importer expects %q)", cat.SchemaVersion, SchemaVersion)
	}
	if cat.ReleaseVersion == "" {
		return errors.New("scfimport: release_version is required")
	}
	if len(cat.Controls) == 0 {
		return errors.New("scfimport: catalog has zero controls")
	}
	for i, c := range cat.Controls {
		if c.SCFID == "" {
			return fmt.Errorf("scfimport: control[%d] missing scf_id", i)
		}
		if c.Family == "" {
			return fmt.Errorf("scfimport: control[%d] (%s) missing family", i, c.SCFID)
		}
		if c.Title == "" {
			return fmt.Errorf("scfimport: control[%d] (%s) missing title", i, c.SCFID)
		}
	}
	return nil
}

// Import runs the catalog against the database in a single transaction.
// Returns a Report on success; rolls back on any error.
func Import(ctx context.Context, db DBTX, cat *Catalog) (Report, error) {
	conn, ok := db.(pgxBeginner)
	if !ok {
		return Report{}, errors.New("scfimport: db must support BeginTx (pgx.Conn / pgxpool.Pool)")
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Report{}, fmt.Errorf("scfimport: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	report, err := importIntoTx(ctx, tx, cat)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("scfimport: commit: %w", err)
	}
	return report, nil
}

// DBTX is the minimum surface Import needs. Both *pgxpool.Pool and
// *pgx.Conn satisfy it.
type DBTX interface{}

type pgxBeginner interface {
	BeginTx(ctx context.Context, txOpts pgx.TxOptions) (pgx.Tx, error)
}

func importIntoTx(ctx context.Context, tx pgx.Tx, cat *Catalog) (Report, error) {
	q := dbx.New(tx)

	// 1. Upsert the SCF framework row.
	frameworkID := uuidFromString("scf")
	framework, err := q.UpsertFramework(ctx, dbx.UpsertFrameworkParams{
		ID:          frameworkID,
		Name:        pgtypeText("Secure Controls Framework"),
		Slug:        "scf",
		Issuer:      "Secure Controls Framework Council",
		Description: pgtypeText("Canonical control catalog with STRM crosswalk to 200+ frameworks."),
	})
	if err != nil {
		return Report{}, fmt.Errorf("scfimport: upsert framework: %w", err)
	}

	// 2. Decide if this is a new release_version.
	versions, err := q.ListFrameworkVersionsBySlug(ctx, "scf")
	if err != nil {
		return Report{}, fmt.Errorf("scfimport: list versions: %w", err)
	}
	isNew := true
	for _, v := range versions {
		if v.Version == cat.ReleaseVersion {
			isNew = false
			break
		}
	}

	// 3. If new, demote previous "current" → "legacy" so the at-most-one-
	//    current invariant holds.
	if isNew {
		if err := q.DemoteCurrentFrameworkVersions(ctx, framework.ID); err != nil {
			return Report{}, fmt.Errorf("scfimport: demote current: %w", err)
		}
	}

	// 4. Upsert the framework_version pinned to the imported release.
	effectiveFrom := parseDate(cat.ReleaseDate)
	version, err := q.UpsertFrameworkVersion(ctx, dbx.UpsertFrameworkVersionParams{
		ID:            uuidFromString("scf-" + cat.ReleaseVersion),
		FrameworkID:   framework.ID,
		Version:       cat.ReleaseVersion,
		EffectiveFrom: effectiveFrom,
		EffectiveTo:   pgtype.Date{},
		Status:        dbx.FrameworkVersionStatusCurrent,
	})
	if err != nil {
		return Report{}, fmt.Errorf("scfimport: upsert framework_version: %w", err)
	}

	// 5. Point frameworks.latest_version_id at this row.
	if err := q.SetLatestVersion(ctx, dbx.SetLatestVersionParams{
		ID:              framework.ID,
		LatestVersionID: pgtype.UUID{Bytes: version.ID.Bytes, Valid: true},
	}); err != nil {
		return Report{}, fmt.Errorf("scfimport: set latest version: %w", err)
	}

	// 6. Upsert each anchor; tally created vs updated.
	report := Report{
		ReleaseVersion:     cat.ReleaseVersion,
		FrameworkID:        uuidString(framework.ID),
		FrameworkVersionID: uuidString(version.ID),
		IsNewVersion:       isNew,
	}
	for _, ctl := range cat.Controls {
		subtopics, err := json.Marshal(ctl.Subtopics)
		if err != nil {
			return Report{}, fmt.Errorf("scfimport: marshal subtopics for %s: %w", ctl.SCFID, err)
		}
		row, err := q.UpsertSCFAnchor(ctx, dbx.UpsertSCFAnchorParams{
			ID:                 uuidFromString("anchor-" + cat.ReleaseVersion + "-" + ctl.SCFID),
			FrameworkVersionID: version.ID,
			ScfID:              ctl.SCFID,
			Family:             ctl.Family,
			Title:              ctl.Title,
			Description:        ctl.Description,
			Subtopics:          subtopics,
		})
		if err != nil {
			return Report{}, fmt.Errorf("scfimport: upsert anchor %s: %w", ctl.SCFID, err)
		}
		switch {
		case row.Inserted:
			report.Created++
		case wasContentUpdated(row, ctl, subtopics):
			report.Updated++
		default:
			report.Unchanged++
		}
	}
	return report, nil
}

// wasContentUpdated returns true when the upsert touched a meaningful
// field (vs. a no-op rewrite). A row counted as Updated must reflect a
// real content change; Unchanged is the steady-state idempotency signal.
func wasContentUpdated(row dbx.UpsertSCFAnchorRow, ctl Control, subtopics []byte) bool {
	if row.Family != ctl.Family || row.Title != ctl.Title || row.Description != ctl.Description {
		return true
	}
	if string(row.Subtopics) != string(subtopics) {
		return true
	}
	return false
}

func uuidFromString(seed string) pgtype.UUID {
	id := uuid.NewSHA1(uuid.NameSpaceDNS, []byte("security-atlas/scfimport/"+seed))
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func pgtypeText(s string) string { return s }

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
