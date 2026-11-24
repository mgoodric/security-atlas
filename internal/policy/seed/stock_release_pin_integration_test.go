//go:build integration

package seed_test

// Issue #3033: the stock policies declare the SCF release of the sample
// catalog the bootstrap imports (migrations/fixtures/scf-sample.json), and
// every code they link is in it. `atlas-cli policy seed-stock` on a fresh
// install binds every link in that release with no release-fallback
// warning, which #1774 needs before it removes the fallback.
//
// Before #3033 they declared "2026.3", which no install loads, and linked
// four codes the sample lacks. A policy row stores no scf_release and
// nothing re-reads policies/stock on upgrade, so an install seeded from the
// old files keeps its policies as they are: the re-pin neither supersedes
// nor republishes them, and a later seed-stock run only inserts drafts.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/scfseed"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/policy/seed"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const repoRoot = "../../.."

// preRepinCodes maps each code #3033 linked in place of one the sample
// lacks back to the code the pre-#3033 file linked.
var preRepinCodes = map[string]string{
	"IAC-12": "IAC-22",
	"CHG-03": "CHG-04",
	"IRO-14": "IRO-01",
	"TPM-14": "TPM-04",
}

// sampleLoaded makes sure the sample catalog is imported and returns its
// release label.
func sampleLoaded(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	if err := scfseed.EnsureSCFCatalog(ctx, admin); err != nil {
		t.Fatalf("EnsureSCFCatalog: %v", err)
	}
	cat, err := scfimport.Load(filepath.Join(repoRoot, "migrations", "fixtures", "scf-sample.json"))
	if err != nil {
		t.Fatalf("load sample: %v", err)
	}
	if _, err := scfrelease.Lookup(ctx, dbx.New(admin), cat.ReleaseVersion); err != nil {
		t.Fatalf("sample release %s is not loaded: %v", cat.ReleaseVersion, err)
	}
	return cat.ReleaseVersion
}

func shippedStockPolicies(t *testing.T) []seed.StockPolicy {
	t.Helper()
	policies, err := seed.LoadFromFS(os.DirFS(repoRoot), "policies/stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	return policies
}

// preRepinStockPolicies rebuilds the pre-#3033 files from the shipped ones:
// scf_release "2026.3" and the four codes the sample lacks.
func preRepinStockPolicies(t *testing.T, release string) []seed.StockPolicy {
	t.Helper()
	fsys := fstest.MapFS{}
	for _, p := range shippedStockPolicies(t) {
		b, err := os.ReadFile(filepath.Join(repoRoot, p.SourcePath))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		pin := "scf_release: " + release + "\n"
		if !strings.Contains(s, pin) {
			t.Fatalf("%s does not declare %q", p.SourcePath, pin)
		}
		s = strings.Replace(s, pin, "scf_release: \"2026.3\"\n", 1)
		for now, was := range preRepinCodes {
			s = strings.Replace(s, "  - "+now+"\n", "  - "+was+"\n", 1)
		}
		fsys["stock/"+filepath.Base(p.SourcePath)] = &fstest.MapFile{Data: []byte(s)}
	}
	policies, err := seed.LoadFromFS(fsys, "stock")
	if err != nil {
		t.Fatalf("LoadFromFS(pre-#3033): %v", err)
	}
	return policies
}

// bindStockPolicyCodes creates one control per code the shipped policies
// link, bound to that code's anchor in release, as the stock bundle upload
// would bind it. A code release lacks gets no control; the seed reports it
// missing.
func bindStockPolicyCodes(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID, release string) {
	t.Helper()
	seen := map[string]bool{}
	for _, p := range shippedStockPolicies(t) {
		for _, code := range p.FrontMatter.LinkedControlIDs {
			if seen[code] {
				continue
			}
			seen[code] = true
			if _, err := admin.Exec(context.Background(), `
				INSERT INTO controls (id, tenant_id, scf_id, scf_anchor_id, title, control_family, implementation_type, bundle_id)
				SELECT g.id, $1, a.scf_id, a.id, 'Test control', 'IAC', 'automated', 'legacy_' || g.id::text
				FROM scf_anchors a
				JOIN framework_versions fv ON fv.id = a.framework_version_id
				JOIN frameworks f ON f.id = fv.framework_id
				CROSS JOIN (SELECT gen_random_uuid() AS id) g
				WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.version = $2 AND a.scf_id = $3
			`, tenant, release, code); err != nil {
				t.Fatalf("seed control on %s %s: %v", release, code, err)
			}
		}
	}
}

func TestSeed_FreshInstallBindsStockPoliciesInTheSampleRelease(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	release := sampleLoaded(t, admin)
	tenant := freshTenant(t, admin)
	bindStockPolicyCodes(t, admin, tenant, release)

	report, err := seed.Seed(context.Background(), app, tenant, shippedStockPolicies(t), seed.NewSQLAnchorResolver(admin))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(report.SCFReleaseWarnings) != 0 {
		t.Errorf("fresh install: want no SCF release warnings, got %v", report.SCFReleaseWarnings)
	}
	if len(report.MissingAnchors) != 0 {
		t.Errorf("fresh install: want every linked code to resolve in %s, missing %v", release, report.MissingAnchors)
	}
	for _, p := range report.Loaded {
		if p.LinkedControlCount != 3 {
			t.Errorf("%s: linked %d controls; want 3", p.Title, p.LinkedControlCount)
		}
	}
}

// policyRow is the part of a policy row the upgrade must not touch.
type policyRow struct {
	ID          uuid.UUID
	Predecessor *uuid.UUID
	Title       string
	Version     string
	Status      string
	Linked      []uuid.UUID
}

func readPolicyRows(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) map[uuid.UUID]policyRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT id, predecessor_id, title, version, status, linked_control_ids
		FROM policies WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("read policies: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]policyRow{}
	for rows.Next() {
		var r policyRow
		if err := rows.Scan(&r.ID, &r.Predecessor, &r.Title, &r.Version, &r.Status, &r.Linked); err != nil {
			t.Fatalf("scan policy: %v", err)
		}
		out[r.ID] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSeed_UpgradeFromPreRepinStockKeepsPolicyHistory(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	release := sampleLoaded(t, admin)
	tenant := freshTenant(t, admin)
	bindStockPolicyCodes(t, admin, tenant, release)
	resolver := seed.NewSQLAnchorResolver(admin)

	// An install seeded from the pre-#3033 files: every policy falls back
	// to the current release, with a warning.
	old, err := seed.Seed(ctx, app, tenant, preRepinStockPolicies(t, release), resolver)
	if err != nil {
		t.Fatalf("Seed(pre-#3033): %v", err)
	}
	if len(old.SCFReleaseWarnings) != seed.StockPolicyCount {
		t.Fatalf("pre-#3033 seed: want %d release-fallback warnings, got %v", seed.StockPolicyCount, old.SCFReleaseWarnings)
	}

	// The operator takes one of them through publish and a second version,
	// so it has a version history.
	tctx, err := tenancy.WithTenant(ctx, tenant.String())
	if err != nil {
		t.Fatal(err)
	}
	store := policy.NewStore(app)
	approver := policy.Approver{UserID: "6f1c2a7e-0000-4000-8000-00000000a001", CredentialID: "key_approver"}
	const submitter = "6f1c2a7e-0000-4000-8000-00000000a004"
	publish := func(id uuid.UUID, version string) {
		t.Helper()
		if _, err := store.SubmitForReview(tctx, id, submitter); err != nil {
			t.Fatalf("SubmitForReview %s: %v", version, err)
		}
		if _, err := store.Approve(tctx, id, approver); err != nil {
			t.Fatalf("Approve %s: %v", version, err)
		}
		if _, err := store.Publish(tctx, id, policy.PublishInput{NewVersion: version, PublishedBy: "key_publisher"}); err != nil {
			t.Fatalf("Publish %s: %v", version, err)
		}
	}
	first := old.Loaded[0]
	publish(first.ID, "1.0.0")
	v1, err := store.Get(tctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.Create(tctx, policy.CreateInput{
		PredecessorID:               &first.ID,
		Title:                       v1.Title,
		Version:                     "1.1.0",
		BodyMd:                      v1.BodyMd + "\nRevised.\n",
		OwnerRole:                   v1.OwnerRole,
		ApproverRole:                v1.ApproverRole,
		LinkedControlIDs:            v1.LinkedControlIDs,
		AcknowledgmentRequiredRoles: v1.AcknowledgmentRequiredRoles,
		SourceAttribution:           v1.SourceAttribution,
		CreatedBy:                   "key_creator",
	})
	if err != nil {
		t.Fatalf("Create next version: %v", err)
	}
	publish(next.ID, "1.1.0")
	// VersionChain walks forward from the id it is given, so start at 1.0.0.
	chainBefore, err := store.VersionChain(tctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := readPolicyRows(t, admin, tenant)

	// The upgrade: the boot re-imports the sample (frozen, so a no-op) and
	// ships the re-pinned files; the operator re-runs seed-stock.
	sampleLoaded(t, admin)
	again, err := seed.Seed(ctx, app, tenant, shippedStockPolicies(t), resolver)
	if err != nil {
		t.Fatalf("Seed(shipped): %v", err)
	}
	if len(again.SCFReleaseWarnings) != 0 {
		t.Errorf("re-seed after upgrade: want no SCF release warnings, got %v", again.SCFReleaseWarnings)
	}

	after := readPolicyRows(t, admin, tenant)
	for id, was := range before {
		now, ok := after[id]
		if !ok {
			t.Errorf("policy %s (%s %s) is gone after the upgrade", id, was.Title, was.Version)
			continue
		}
		if now.Version != was.Version || now.Status != was.Status || now.Title != was.Title ||
			!slices.Equal(now.Linked, was.Linked) || (now.Predecessor == nil) != (was.Predecessor == nil) ||
			(now.Predecessor != nil && *now.Predecessor != *was.Predecessor) {
			t.Errorf("policy %s changed on upgrade:\n  before %+v\n  after  %+v", id, was, now)
		}
	}
	for id, now := range after {
		if _, existed := before[id]; existed {
			continue
		}
		if now.Status != policy.StateDraft || now.Predecessor != nil {
			t.Errorf("re-seed wrote %s as %s with predecessor %v; want a fresh draft", now.Title, now.Status, now.Predecessor)
		}
	}
	chainAfter, err := store.VersionChain(tctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chainAfter) != len(chainBefore) {
		t.Fatalf("version chain: %d versions before the upgrade, %d after", len(chainBefore), len(chainAfter))
	}
	for i := range chainBefore {
		if chainAfter[i].ID != chainBefore[i].ID || chainAfter[i].Version != chainBefore[i].Version || chainAfter[i].Status != chainBefore[i].Status {
			t.Errorf("version chain[%d]: before %s %s %s, after %s %s %s", i,
				chainBefore[i].ID, chainBefore[i].Version, chainBefore[i].Status,
				chainAfter[i].ID, chainAfter[i].Version, chainAfter[i].Status)
		}
	}
	if len(chainAfter) != 2 {
		t.Errorf("version chain has %d versions; want 2 (1.0.0 superseded, 1.1.0 published)", len(chainAfter))
	}
}
