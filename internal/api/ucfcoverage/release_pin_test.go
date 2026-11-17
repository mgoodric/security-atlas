package ucfcoverage

// #2315 (nit from the review of #2308): the empty-catalog fallback of the
// coverage release pin, exercised directly. With no SCF release current
// and no ?scf_release, the read stays unpinned (nil, no error); a named
// release that is not loaded still fails.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// releaseReader is a scfrelease.Querier over a fixed list of SCF releases.
type releaseReader struct{ versions []dbx.FrameworkVersion }

func (f releaseReader) ListFrameworkVersionsBySlug(context.Context, string) ([]dbx.FrameworkVersion, error) {
	return f.versions, nil
}

func (releaseReader) GetSCFAnchorByVersionAndSCFID(context.Context, dbx.GetSCFAnchorByVersionAndSCFIDParams) (dbx.ScfAnchor, error) {
	return dbx.ScfAnchor{}, errors.New("not used")
}

func (releaseReader) GetSCFAnchorByID(context.Context, pgtype.UUID) (dbx.ScfAnchor, error) {
	return dbx.ScfAnchor{}, errors.New("not used")
}

func (releaseReader) GetFrameworkVersionByID(context.Context, pgtype.UUID) (dbx.FrameworkVersion, error) {
	return dbx.FrameworkVersion{}, errors.New("not used")
}

func TestPinSCFRelease(t *testing.T) {
	t.Parallel()
	current := dbx.FrameworkVersion{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, Version: "B", Status: dbx.FrameworkVersionStatusCurrent}
	legacy := dbx.FrameworkVersion{ID: pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, Version: "A", Status: dbx.FrameworkVersionStatusLegacy}
	for _, tc := range []struct {
		name     string
		versions []dbx.FrameworkVersion
		label    string
		want     string // "" means unpinned
		wantErr  error
	}{
		{name: "no release loaded, no label: unpinned", versions: nil},
		{name: "only a legacy release, no label: unpinned", versions: []dbx.FrameworkVersion{legacy}},
		{name: "no label pins the current release", versions: []dbx.FrameworkVersion{legacy, current}, want: "B"},
		{name: "a label pins that release", versions: []dbx.FrameworkVersion{legacy, current}, label: "A", want: "A"},
		{name: "an unloaded label fails", versions: nil, label: "A", wantErr: scfrelease.ErrUnknownRelease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rel, err := pinSCFRelease(context.Background(), releaseReader{tc.versions}, tc.label)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) || rel != nil {
					t.Fatalf("got %v, %v; want nil, %v", rel, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			switch {
			case tc.want == "" && rel != nil:
				t.Fatalf("pinned to %q; want unpinned", rel.Version)
			case tc.want != "" && (rel == nil || rel.Version != tc.want):
				t.Fatalf("pinned to %v; want %q", rel, tc.want)
			}
		})
	}
}
