// Package scfimport ingests the Secure Controls Framework catalog into
// Postgres. It reads either the JSON shape documented in the Catalog type
// below (Load) or SCF's published Excel workbook directly (LoadWorkbook).
//
// An imported release is frozen. Re-running with a release_version that is
// already loaded compares the incoming catalog with the stored anchors:
// identical content is a no-op, and any difference is a
// *ReleaseConflictError naming the differing anchors, with nothing written
// (migration 20260916000000 backs this with a trigger on scf_anchors). A new
// release_version creates a new framework_versions row, demotes the previous
// "current" → "legacy", and inserts a fresh set of anchors against the new
// version. Old versions stay queryable for audit replay.
package scfimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
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

// ReleaseConflictError reports a re-import of an already-imported release
// label whose content differs from the stored anchors. Imported anchors are
// frozen; a changed catalog ships under a new release label.
type ReleaseConflictError struct {
	ReleaseVersion string
	// Changed lists scf_ids present in both whose family, title,
	// description or subtopics differ.
	Changed []string
	// Added lists scf_ids in the incoming catalog but not the stored release.
	Added []string
	// Missing lists scf_ids in the stored release but not the incoming catalog.
	Missing []string
}

func (e *ReleaseConflictError) Error() string {
	var parts []string
	if len(e.Changed) > 0 {
		parts = append(parts, "changed: "+strings.Join(e.Changed, ", "))
	}
	if len(e.Added) > 0 {
		parts = append(parts, "added: "+strings.Join(e.Added, ", "))
	}
	if len(e.Missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(e.Missing, ", "))
	}
	return fmt.Sprintf(
		"scfimport: SCF release %q is already imported and its anchors are frozen; the incoming catalog differs (%s); nothing was written — import the changed catalog under a new release label",
		e.ReleaseVersion, strings.Join(parts, "; "))
}

// diffRelease compares the stored anchors of a release with the incoming
// catalog. It returns nil when they are identical.
func diffRelease(release string, stored []dbx.ScfAnchor, incoming []Control) *ReleaseConflictError {
	byID := make(map[string]dbx.ScfAnchor, len(stored))
	for _, a := range stored {
		byID[a.ScfID] = a
	}
	conflict := &ReleaseConflictError{ReleaseVersion: release}
	seen := make(map[string]bool, len(incoming))
	for _, ctl := range incoming {
		seen[ctl.SCFID] = true
		row, ok := byID[ctl.SCFID]
		switch {
		case !ok:
			conflict.Added = append(conflict.Added, ctl.SCFID)
		case !anchorContentEqual(row, ctl):
			conflict.Changed = append(conflict.Changed, ctl.SCFID)
		}
	}
	for _, a := range stored {
		if !seen[a.ScfID] {
			conflict.Missing = append(conflict.Missing, a.ScfID)
		}
	}
	if len(conflict.Changed)+len(conflict.Added)+len(conflict.Missing) == 0 {
		return nil
	}
	return conflict
}

func importIntoTx(ctx context.Context, tx pgx.Tx, cat *Catalog) (Report, error) {
	q := dbx.New(tx)

	// 0. A release label that is already imported is frozen: compare before
	//    any write. Identical content is a no-op; a difference is refused.
	//    A version row with no anchors yet (a load that never inserted any)
	//    falls through to the normal load path.
	versions, err := q.ListFrameworkVersionsBySlug(ctx, "scf")
	if err != nil {
		return Report{}, fmt.Errorf("scfimport: list versions: %w", err)
	}
	isNew := true
	for _, v := range versions {
		if v.Version != cat.ReleaseVersion {
			continue
		}
		isNew = false
		stored, err := q.ListSCFAnchorsForVersion(ctx, dbx.ListSCFAnchorsForVersionParams{
			FrameworkVersionID: v.ID,
			Limit:              math.MaxInt32,
			Offset:             0,
		})
		if err != nil {
			return Report{}, fmt.Errorf("scfimport: list stored anchors for %s: %w", cat.ReleaseVersion, err)
		}
		if len(stored) == 0 {
			break
		}
		if conflict := diffRelease(cat.ReleaseVersion, stored, cat.Controls); conflict != nil {
			return Report{}, conflict
		}
		return Report{
			ReleaseVersion:     cat.ReleaseVersion,
			FrameworkID:        uuidString(v.FrameworkID),
			FrameworkVersionID: uuidString(v.ID),
			IsNewVersion:       false,
			Unchanged:          len(stored),
		}, nil
	}

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

	// 2. If new, demote previous "current" → "legacy" so the at-most-one-
	//    current invariant holds.
	if isNew {
		if err := q.DemoteCurrentFrameworkVersions(ctx, framework.ID); err != nil {
			return Report{}, fmt.Errorf("scfimport: demote current: %w", err)
		}
	}

	// 3. Upsert the framework_version pinned to the imported release.
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

	// 4. Point frameworks.latest_version_id at this row.
	if err := q.SetLatestVersion(ctx, dbx.SetLatestVersionParams{
		ID:              framework.ID,
		LatestVersionID: pgtype.UUID{Bytes: version.ID.Bytes, Valid: true},
	}); err != nil {
		return Report{}, fmt.Errorf("scfimport: set latest version: %w", err)
	}

	// 5. Insert each anchor. Only a new release (or an empty version row)
	//    reaches here, so every anchor is new; a row that already exists
	//    means a concurrent load, and the unique (framework_version_id,
	//    scf_id) key rejects it.
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
		if _, err := q.InsertSCFAnchor(ctx, dbx.InsertSCFAnchorParams{
			ID:                 uuidFromString("anchor-" + cat.ReleaseVersion + "-" + ctl.SCFID),
			FrameworkVersionID: version.ID,
			ScfID:              ctl.SCFID,
			Family:             ctl.Family,
			Title:              ctl.Title,
			Description:        ctl.Description,
			Subtopics:          subtopics,
		}); err != nil {
			return Report{}, fmt.Errorf("scfimport: insert anchor %s: %w", ctl.SCFID, err)
		}
		report.Created++
	}
	return report, nil
}

// anchorContentEqual returns true when the persisted row's content matches
// the incoming control. Subtopics compare by value, not by bytes: Postgres
// re-serializes JSONB, so the stored text does not match json.Marshal output
// byte-for-byte once a subtopic is present. A null and an empty list are the
// same (no subtopics).
func anchorContentEqual(row dbx.ScfAnchor, ctl Control) bool {
	if row.Family != ctl.Family || row.Title != ctl.Title || row.Description != ctl.Description {
		return false
	}
	var stored []Subtopic
	if len(row.Subtopics) > 0 {
		if err := json.Unmarshal(row.Subtopics, &stored); err != nil {
			return false
		}
	}
	if len(stored) == 0 && len(ctl.Subtopics) == 0 {
		return true
	}
	return reflect.DeepEqual(stored, ctl.Subtopics)
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
