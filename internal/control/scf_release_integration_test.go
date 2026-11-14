//go:build integration

// #1770: control.yaml's scf_release binds scf_anchor_id within that SCF
// release. Release A and release B are the internal/scfrelease/testdata
// fixtures; B is imported second and is current.

package control_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func releaseBundleYAML(bundleID, anchor, release string) string {
	y := yamlFor(bundleID, anchor, "manual_attested")
	if release != "" {
		y += "scf_release: " + release + "\n"
	}
	return y
}

func anchorIDIn(t *testing.T, admin *pgxpool.Pool, release, code string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := admin.QueryRow(context.Background(), `
		SELECT a.id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.tenant_id IS NULL AND fv.version = $1 AND a.scf_id = $2`, release, code).Scan(&id); err != nil {
		t.Fatalf("anchor %s in %s: %v", code, release, err)
	}
	return id
}

func uploadYAML(t *testing.T, store *control.Store, tenant, y string) (control.UploadResult, error) {
	t.Helper()
	bundle, err := control.FinalizeBundleForHTTP([]byte(y))
	if err != nil {
		t.Fatalf("FinalizeBundleForHTTP: %v", err)
	}
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return store.Upload(ctx, bundle, "key_admin")
}

func boundAnchor(t *testing.T, admin *pgxpool.Pool, controlID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT scf_anchor_id FROM controls WHERE id = $1`, controlID).Scan(&id); err != nil {
		t.Fatalf("read control: %v", err)
	}
	return id
}

// A bundle declaring release A binds to A's IAC-06 even though B, which
// also has an IAC-06, is current.
func TestUpload_SCFRelease_BindsInDeclaredRelease(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	wantA := anchorIDIn(t, admin, scfreleasetest.ReleaseA, "IAC-06")
	if wantA == anchorIDIn(t, admin, scfreleasetest.ReleaseB, "IAC-06") {
		t.Fatal("fixture releases share an IAC-06 anchor row")
	}

	res, err := uploadYAML(t, store, tenant, releaseBundleYAML("rel_a_mfa", "IAC-06", scfreleasetest.ReleaseA))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got := boundAnchor(t, admin, res.ControlID); got != wantA {
		t.Fatalf("bound to %s; want release A's IAC-06 %s", got, wantA)
	}
	if res.AnchorID != wantA || res.SCFRelease != scfreleasetest.ReleaseA {
		t.Fatalf("result anchor=%s release=%q; want %s %q", res.AnchorID, res.SCFRelease, wantA, scfreleasetest.ReleaseA)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("declared release produced warnings: %v", res.Warnings)
	}
}

// An unknown release and a code missing from the declared release fail
// with distinct, named errors, and nothing is written.
func TestUpload_SCFRelease_DistinctErrors(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	_, errRelease := uploadYAML(t, store, tenant, releaseBundleYAML("rel_unknown", "IAC-06", "no-such-release"))
	if !errors.Is(errRelease, control.ErrSCFReleaseUnknown) || errors.Is(errRelease, control.ErrSCFCodeNotInRelease) {
		t.Fatalf("unknown release: err = %v; want only ErrSCFReleaseUnknown", errRelease)
	}
	if !strings.Contains(errRelease.Error(), `"no-such-release"`) {
		t.Errorf("unknown-release error does not name the release: %v", errRelease)
	}

	// CRY-03.1 exists only in release B.
	_, errCode := uploadYAML(t, store, tenant, releaseBundleYAML("rel_missing", "CRY-03.1", scfreleasetest.ReleaseA))
	if !errors.Is(errCode, control.ErrSCFCodeNotInRelease) || errors.Is(errCode, control.ErrSCFReleaseUnknown) {
		t.Fatalf("missing code: err = %v; want only ErrSCFCodeNotInRelease", errCode)
	}
	if !strings.Contains(errCode.Error(), `"CRY-03.1" is not in SCF release "test-1770-a"`) {
		t.Errorf("missing-code error does not name code and release: %v", errCode)
	}
	if errors.Is(errRelease, control.ErrSCFAnchorUnknown) || errors.Is(errCode, control.ErrSCFAnchorUnknown) {
		t.Errorf("declared-release errors collapse into ErrSCFAnchorUnknown: %v / %v", errRelease, errCode)
	}

	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM controls WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count controls: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d controls written by failed uploads", n)
	}
}

// A bundle with no scf_release uploads as before, binding in the current
// release (B), and returns the deprecation warning.
func TestUpload_SCFRelease_AbsentWarnsAndBindsCurrent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	scfreleasetest.LoadAB(t, admin)
	tenant := freshTenant(t, admin)
	store := control.NewStore(app)

	res, err := uploadYAML(t, store, tenant, releaseBundleYAML("rel_none", "IAC-06", ""))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if want := anchorIDIn(t, admin, scfreleasetest.ReleaseB, "IAC-06"); boundAnchor(t, admin, res.ControlID) != want {
		t.Fatalf("bound to %s; want current release B's IAC-06 %s", boundAnchor(t, admin, res.ControlID), want)
	}
	if res.SCFRelease != scfreleasetest.ReleaseB {
		t.Errorf("SCFRelease = %q; want %q", res.SCFRelease, scfreleasetest.ReleaseB)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != control.SCFReleaseDeprecationWarning {
		t.Fatalf("Warnings = %v; want the deprecation warning", res.Warnings)
	}

	// An unknown code with no release is still the pre-#1770 error.
	if _, err := uploadYAML(t, store, tenant, releaseBundleYAML("rel_none_bad", "NOPE-99", "")); !errors.Is(err, control.ErrSCFAnchorUnknown) {
		t.Fatalf("unknown code without release: err = %v; want ErrSCFAnchorUnknown", err)
	}
}
