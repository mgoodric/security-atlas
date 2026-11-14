// Package scfrelease resolves SCF codes against a named SCF release
// (#1770, the expand step of #1718's release-aware anchors).
//
// The SCF reuses codes between releases: the same code can name a different
// control, or the same control under a new title. A code on its own is
// therefore ambiguous once more than one release is loaded, and every
// caller that binds or reads an anchor by code goes through Resolve with
// the release it means. An empty release means the current one, which is
// what every caller did before releases could be named.
//
// Resolve has two distinct, named failures: ErrUnknownRelease (no SCF
// release carries that label) and ErrCodeNotInRelease (the release exists
// but has no anchor with that code). Callers map them to different
// operator-facing messages, so an author can tell a typo in the release
// from a code the release withdrew.
package scfrelease

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ErrUnknownRelease is returned when the named SCF release is not loaded.
var ErrUnknownRelease = errors.New("scf release: unknown SCF release")

// ErrCodeNotInRelease is returned when the release is loaded but has no
// anchor with the given code (or the given anchor uuid belongs to another
// release).
var ErrCodeNotInRelease = errors.New("scf release: SCF code not in release")

// ErrNoCurrentRelease is returned when no release was named and no SCF
// release is current (an empty catalog).
var ErrNoCurrentRelease = errors.New("scf release: no current SCF release is loaded")

// Querier is the subset of dbx.Queries the resolver reads through.
// *dbx.Queries satisfies it.
type Querier interface {
	ListFrameworkVersionsBySlug(ctx context.Context, slug string) ([]dbx.FrameworkVersion, error)
	GetSCFAnchorByVersionAndSCFID(ctx context.Context, arg dbx.GetSCFAnchorByVersionAndSCFIDParams) (dbx.ScfAnchor, error)
	GetSCFAnchorByID(ctx context.Context, id pgtype.UUID) (dbx.ScfAnchor, error)
	GetFrameworkVersionByID(ctx context.Context, id pgtype.UUID) (dbx.FrameworkVersion, error)
}

// Release is one loaded SCF release: its framework_versions row id, its
// label (e.g. "2025.2") and its status (current / legacy / withdrawn).
type Release struct {
	ID      pgtype.UUID
	Version string
	Status  string
}

// Lookup returns the SCF release with the given label, or the current
// release when label is empty.
func Lookup(ctx context.Context, q Querier, label string) (Release, error) {
	versions, err := q.ListFrameworkVersionsBySlug(ctx, "scf")
	if err != nil {
		return Release{}, fmt.Errorf("scf release: list releases: %w", err)
	}
	for _, v := range versions {
		if label == "" && v.Status == dbx.FrameworkVersionStatusCurrent {
			return releaseOf(v), nil
		}
		if label != "" && v.Version == label {
			return releaseOf(v), nil
		}
	}
	if label == "" {
		return Release{}, ErrNoCurrentRelease
	}
	return Release{}, fmt.Errorf("%w %q", ErrUnknownRelease, label)
}

// Resolve turns (release, ref) into an anchor. ref is an SCF code such as
// "IAC-06", or an anchor uuid. An empty release means the current one,
// except for a uuid ref with no release, which resolves to that anchor in
// whichever release holds it (the pre-#1770 behaviour for machine-written
// bundles). A uuid ref with a named release must belong to that release.
//
// The returned Release is the one the anchor was resolved in.
func Resolve(ctx context.Context, q Querier, release, ref string) (dbx.ScfAnchor, Release, error) {
	if u, err := uuid.Parse(ref); err == nil {
		row, err := q.GetSCFAnchorByID(ctx, pgtype.UUID{Bytes: u, Valid: true})
		switch {
		case err == nil:
			return resolveByID(ctx, q, release, ref, row)
		case !errors.Is(err, pgx.ErrNoRows):
			return dbx.ScfAnchor{}, Release{}, fmt.Errorf("scf release: get anchor by id: %w", err)
		}
		// Not an anchor id: fall through and try it as a code.
	}

	rel, err := Lookup(ctx, q, release)
	if err != nil {
		return dbx.ScfAnchor{}, Release{}, err
	}
	row, err := q.GetSCFAnchorByVersionAndSCFID(ctx, dbx.GetSCFAnchorByVersionAndSCFIDParams{
		FrameworkVersionID: rel.ID,
		ScfID:              ref,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbx.ScfAnchor{}, Release{}, fmt.Errorf("%w: %q is not in SCF release %q", ErrCodeNotInRelease, ref, rel.Version)
	}
	if err != nil {
		return dbx.ScfAnchor{}, Release{}, fmt.Errorf("scf release: get anchor by code: %w", err)
	}
	return row, rel, nil
}

// resolveByID checks an anchor found by uuid against the named release
// (if any) and returns it with the release that holds it.
func resolveByID(ctx context.Context, q Querier, release, ref string, row dbx.ScfAnchor) (dbx.ScfAnchor, Release, error) {
	if release != "" {
		rel, err := Lookup(ctx, q, release)
		if err != nil {
			return dbx.ScfAnchor{}, Release{}, err
		}
		if rel.ID != row.FrameworkVersionID {
			return dbx.ScfAnchor{}, Release{}, fmt.Errorf("%w: anchor %s is not in SCF release %q", ErrCodeNotInRelease, ref, release)
		}
		return row, rel, nil
	}
	// No release named: the anchor's own version, read by id so an anchor
	// is never refused for how its framework row is labelled.
	v, err := q.GetFrameworkVersionByID(ctx, row.FrameworkVersionID)
	if err != nil {
		return dbx.ScfAnchor{}, Release{}, fmt.Errorf("scf release: get anchor's release: %w", err)
	}
	return row, releaseOf(v), nil
}

func releaseOf(v dbx.FrameworkVersion) Release {
	return Release{ID: v.ID, Version: v.Version, Status: string(v.Status)}
}
