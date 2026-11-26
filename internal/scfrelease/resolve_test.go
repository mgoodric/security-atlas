package scfrelease_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
)

// fakeQ is an in-memory catalog of two releases: A (legacy) and B
// (current), each with an IAC-06; only B has CRY-03.1.
type fakeQ struct {
	versions []dbx.FrameworkVersion
	extra    []dbx.FrameworkVersion // versions under another framework slug
	anchors  []dbx.ScfAnchor
	err      error
}

func pgID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func newFake() *fakeQ {
	a := dbx.FrameworkVersion{ID: pgID(), Version: "A", Status: dbx.FrameworkVersionStatusLegacy}
	b := dbx.FrameworkVersion{ID: pgID(), Version: "B", Status: dbx.FrameworkVersionStatusCurrent}
	return &fakeQ{
		versions: []dbx.FrameworkVersion{b, a},
		anchors: []dbx.ScfAnchor{
			{ID: pgID(), FrameworkVersionID: a.ID, ScfID: "IAC-06", Title: "A's MFA"},
			{ID: pgID(), FrameworkVersionID: b.ID, ScfID: "IAC-06", Title: "B's MFA"},
			{ID: pgID(), FrameworkVersionID: b.ID, ScfID: "CRY-03.1", Title: "Transmission Integrity"},
		},
	}
}

func (f *fakeQ) ListFrameworkVersionsBySlug(_ context.Context, slug string) ([]dbx.FrameworkVersion, error) {
	if f.err != nil {
		return nil, f.err
	}
	if slug != "scf" {
		return nil, nil
	}
	return f.versions, nil
}

func (f *fakeQ) GetSCFAnchorByVersionAndSCFID(_ context.Context, arg dbx.GetSCFAnchorByVersionAndSCFIDParams) (dbx.ScfAnchor, error) {
	for _, a := range f.anchors {
		if a.FrameworkVersionID == arg.FrameworkVersionID && a.ScfID == arg.ScfID {
			return a, nil
		}
	}
	return dbx.ScfAnchor{}, pgx.ErrNoRows
}

func (f *fakeQ) GetSCFAnchorByID(_ context.Context, id pgtype.UUID) (dbx.ScfAnchor, error) {
	for _, a := range f.anchors {
		if a.ID == id {
			return a, nil
		}
	}
	return dbx.ScfAnchor{}, pgx.ErrNoRows
}

// GetFrameworkVersionByID reads any version, including ones outside the
// "scf" slug (extra), the way the real query has no slug filter.
func (f *fakeQ) GetFrameworkVersionByID(_ context.Context, id pgtype.UUID) (dbx.FrameworkVersion, error) {
	if f.err != nil {
		return dbx.FrameworkVersion{}, f.err
	}
	for _, v := range append(f.versions, f.extra...) {
		if v.ID == id {
			return v, nil
		}
	}
	return dbx.FrameworkVersion{}, pgx.ErrNoRows
}

func (f *fakeQ) anchor(release, code string) dbx.ScfAnchor {
	for _, v := range f.versions {
		if v.Version != release {
			continue
		}
		for _, a := range f.anchors {
			if a.FrameworkVersionID == v.ID && a.ScfID == code {
				return a
			}
		}
	}
	panic("no fixture anchor " + release + "/" + code)
}

func TestResolve(t *testing.T) {
	t.Parallel()
	f := newFake()
	ctx := context.Background()
	idA := uuid.UUID(f.anchor("A", "IAC-06").ID.Bytes).String()

	cases := []struct {
		name        string
		release     string
		ref         string
		wantErr     error
		wantTitle   string
		wantRelease string
	}{
		{"code in named release", "A", "IAC-06", nil, "A's MFA", "A"},
		{"code defaults to current", "", "IAC-06", nil, "B's MFA", "B"},
		{"uuid without release resolves in its own release", "", idA, nil, "A's MFA", "A"},
		{"uuid in the named release", "A", idA, nil, "A's MFA", "A"},
		{"uuid from another release", "B", idA, scfrelease.ErrCodeNotInRelease, "", ""},
		{"uuid with unknown release", "Z", idA, scfrelease.ErrUnknownRelease, "", ""},
		{"unknown release", "Z", "IAC-06", scfrelease.ErrUnknownRelease, "", ""},
		{"code missing from named release", "A", "CRY-03.1", scfrelease.ErrCodeNotInRelease, "", ""},
		{"unknown uuid falls back to code lookup", "", uuid.NewString(), scfrelease.ErrCodeNotInRelease, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row, rel, err := scfrelease.Resolve(ctx, f, tc.release, tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v; want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if row.Title != tc.wantTitle || rel.Version != tc.wantRelease {
				t.Fatalf("got %q in %q; want %q in %q", row.Title, rel.Version, tc.wantTitle, tc.wantRelease)
			}
		})
	}
}

// The two named failures are distinct errors, so a caller can tell them
// apart, and each message names what was missing.
func TestResolve_ErrorsAreDistinctAndNamed(t *testing.T) {
	t.Parallel()
	f := newFake()
	_, _, errRel := scfrelease.Resolve(context.Background(), f, "Z", "IAC-06")
	_, _, errCode := scfrelease.Resolve(context.Background(), f, "A", "CRY-03.1")
	if errors.Is(errRel, scfrelease.ErrCodeNotInRelease) || errors.Is(errCode, scfrelease.ErrUnknownRelease) {
		t.Fatalf("errors overlap: %v / %v", errRel, errCode)
	}
	if got := errRel.Error(); got != `scf release: unknown SCF release "Z"` {
		t.Errorf("release error = %q", got)
	}
	if got := errCode.Error(); got != `scf release: SCF code not in release: "CRY-03.1" is not in SCF release "A"` {
		t.Errorf("code error = %q", got)
	}
}

// An anchor read by uuid with no release resolves even when its version
// sits under a framework row not slugged "scf" (pre-#1770 behaviour, which
// ucfcoverage's synthetic-framework tests rely on).
func TestResolve_UUIDOutsideSCFSlug(t *testing.T) {
	t.Parallel()
	f := newFake()
	other := dbx.FrameworkVersion{ID: pgID(), Version: "scf-484", Status: dbx.FrameworkVersionStatusCurrent}
	f.extra = append(f.extra, other)
	a := dbx.ScfAnchor{ID: pgID(), FrameworkVersionID: other.ID, ScfID: "VSN-1", Title: "synthetic"}
	f.anchors = append(f.anchors, a)
	row, rel, err := scfrelease.Resolve(context.Background(), f, "", uuid.UUID(a.ID.Bytes).String())
	if err != nil || row.ID != a.ID || rel.Version != "scf-484" {
		t.Fatalf("got %v %q %v; want the synthetic anchor in scf-484", row.ID, rel.Version, err)
	}
}

func TestLookup_NoCurrentRelease(t *testing.T) {
	t.Parallel()
	f := &fakeQ{}
	if _, err := scfrelease.Lookup(context.Background(), f, ""); !errors.Is(err, scfrelease.ErrNoCurrentRelease) {
		t.Fatalf("err = %v; want ErrNoCurrentRelease", err)
	}
	if _, _, err := scfrelease.Resolve(context.Background(), f, "", "IAC-06"); !errors.Is(err, scfrelease.ErrNoCurrentRelease) {
		t.Fatalf("Resolve err = %v; want ErrNoCurrentRelease", err)
	}
}

func TestResolve_QueryErrorsPropagate(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	f := newFake()
	idA := uuid.UUID(f.anchor("A", "IAC-06").ID.Bytes).String()
	f.err = boom
	if _, _, err := scfrelease.Resolve(context.Background(), f, "A", "IAC-06"); !errors.Is(err, boom) {
		t.Fatalf("code path err = %v", err)
	}
	if _, _, err := scfrelease.Resolve(context.Background(), f, "A", idA); !errors.Is(err, boom) {
		t.Fatalf("uuid path err = %v", err)
	}
}
