//go:build integration

// Integration tests for #1747: importing a policy library with its
// historical lifecycle, and the next version of an imported policy going
// through the normal workflow. Real Postgres under RLS.

package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/policy/importfile"
)

func importFile(title, version, extra string) string {
	return "---\n" +
		"title: " + title + "\n" +
		"version: \"" + version + "\"\n" +
		"approved_by_name: Jane Doe\n" +
		"approved_at: 2024-02-01\n" +
		"effective_date: 2024-02-15\n" +
		"next_review_at: 2025-02-01\n" +
		"source_url: https://wiki.example.com/" + version + "\n" +
		extra +
		"---\n# " + title + "\n\nPolicy text.\n"
}

// parseAll mirrors the HTTP handler: parse every file, split into inputs
// and parse failures.
func parseAll(t *testing.T, files map[string]string, order ...string) ([]policy.ImportInput, []policy.ImportResult) {
	t.Helper()
	var items []policy.ImportInput
	var failures []policy.ImportResult
	for _, name := range order {
		in, err := importfile.Parse(name, []byte(files[name]))
		if err != nil {
			failures = append(failures, policy.ImportResult{SourceFile: name, Status: policy.ImportStatusFailed, Reason: err.Error()})
			continue
		}
		items = append(items, in)
	}
	return items, failures
}

func countPolicies(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), `SELECT count(*) FROM policies WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	return n
}

func TestImport_TwoValidFiles(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	var bundle string
	if err := admin.QueryRow(context.Background(), `SELECT bundle_id FROM controls WHERE id = $1`, ctrl).Scan(&bundle); err != nil {
		t.Fatalf("bundle id: %v", err)
	}
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	files := map[string]string{
		"a.md": importFile("Access Control Policy", "2.0", "linked_controls: ["+bundle+", "+ctrl.String()+"]\nowner_role: security_lead\n"),
		"b.md": importFile("Vendor Policy", "1.1", ""),
	}
	items, failures := parseAll(t, files, "a.md", "b.md")
	report, err := store.Import(ctx, items, failures, "key_admin", false)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !report.Committed || len(report.Results) != 2 {
		t.Fatalf("report = %+v", report)
	}
	for _, res := range report.Results {
		if res.Status != policy.ImportStatusImported || res.Policy == nil {
			t.Fatalf("result %s = %s (%s)", res.SourceFile, res.Status, res.Reason)
		}
		p := res.Policy
		if p.Status != policy.StatePublished || p.SourceAttribution != policy.SourceImported || !p.IsImported() {
			t.Errorf("%s: status=%s source=%s", res.SourceFile, p.Status, p.SourceAttribution)
		}
		if p.HistoricalApproverName == nil || *p.HistoricalApproverName != "Jane Doe" {
			t.Errorf("%s: historical approver = %v", res.SourceFile, p.HistoricalApproverName)
		}
		if p.HistoricalApprovedAt == nil || p.HistoricalApprovedAt.Format("2006-01-02") != "2024-02-01" {
			t.Errorf("%s: historical approved at = %v", res.SourceFile, p.HistoricalApprovedAt)
		}
		if p.EffectiveDate == nil || p.EffectiveDate.Format("2006-01-02") != "2024-02-15" {
			t.Errorf("%s: effective date = %v", res.SourceFile, p.EffectiveDate)
		}
		if p.NextReviewAt == nil || p.NextReviewAt.UTC().Format("2006-01-02") != "2025-02-01" {
			t.Errorf("%s: next review = %v", res.SourceFile, p.NextReviewAt)
		}
		if p.SourceURL == nil || *p.SourceURL != "https://wiki.example.com/"+p.Version {
			t.Errorf("%s: source url = %v", res.SourceFile, p.SourceURL)
		}
		if p.ApprovedBy != nil {
			t.Errorf("%s: approved_by must stay empty on import, got %q", res.SourceFile, *p.ApprovedBy)
		}
		if p.ImportedBy == nil || *p.ImportedBy != "key_admin" || p.ImportedAt == nil {
			t.Errorf("%s: importer = %v at %v", res.SourceFile, p.ImportedBy, p.ImportedAt)
		}
	}
	a := report.Results[0].Policy
	if len(a.LinkedControlIDs) != 1 || a.LinkedControlIDs[0] != ctrl {
		t.Errorf("bundle id and uuid for one control should resolve to it once, got %v", a.LinkedControlIDs)
	}
	if a.OwnerRole != "security_lead" || a.ApproverRole != importfile.DefaultRole {
		t.Errorf("roles = %q %q", a.OwnerRole, a.ApproverRole)
	}
	// Imported with no linked controls: allowed, and flagged on read.
	if b := report.Results[1].Policy; !b.IsOrphan() {
		t.Error("policy imported without linked controls should be an orphan")
	}

	// One audit record per imported policy.
	for _, res := range report.Results {
		var actor, file, approver, url string
		var approved time.Time
		if err := admin.QueryRow(context.Background(), `
			SELECT actor, source_file, historical_approver_name, historical_approved_at, source_url
			FROM policy_audit_log WHERE tenant_id = $1 AND policy_id = $2 AND action = 'imported'
		`, tenant, res.Policy.ID).Scan(&actor, &file, &approver, &approved, &url); err != nil {
			t.Fatalf("audit row for %s: %v", res.SourceFile, err)
		}
		if actor != "key_admin" || file != res.SourceFile || approver != "Jane Doe" ||
			approved.Format("2006-01-02") != "2024-02-01" || url != *res.Policy.SourceURL {
			t.Errorf("audit row for %s = %s %s %s %v %s", res.SourceFile, actor, file, approver, approved, url)
		}
	}

	// The audit log is append-only.
	if _, err := admin.Exec(context.Background(), `DELETE FROM policy_audit_log WHERE tenant_id = $1`, tenant); err == nil {
		t.Error("DELETE on policy_audit_log should be refused")
	}
}

func TestImport_FailureWritesNothingUnlessPartial(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	missing := "---\ntitle: Broken\nversion: \"1\"\napproved_by_name: Jane Doe\neffective_date: 2024-02-15\n---\nBody\n"
	files := map[string]string{
		"good.md":    importFile("Good Policy", "1.0", ""),
		"missing.md": missing,
	}
	items, failures := parseAll(t, files, "good.md", "missing.md")

	report, err := store.Import(ctx, items, failures, "key_admin", false)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Committed {
		t.Fatal("import with a failed file must not commit")
	}
	byFile := map[string]policy.ImportResult{}
	for _, r := range report.Results {
		byFile[r.SourceFile] = r
	}
	if r := byFile["missing.md"]; r.Status != policy.ImportStatusFailed || r.Reason == "" {
		t.Errorf("missing.md = %+v", r)
	}
	if r := byFile["good.md"]; r.Status != policy.ImportStatusNotImported {
		t.Errorf("good.md = %+v", r)
	}
	if n := countPolicies(t, admin, tenant); n != 0 {
		t.Fatalf("nothing should be written, found %d policies", n)
	}

	report, err = store.Import(ctx, items, failures, "key_admin", true)
	if err != nil {
		t.Fatalf("Import partial: %v", err)
	}
	if !report.Committed || !report.Failed() {
		t.Fatalf("partial report = %+v", report)
	}
	if n := countPolicies(t, admin, tenant); n != 1 {
		t.Fatalf("partial import should write the valid file, found %d policies", n)
	}
}

func TestImport_UnknownControlFails(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	files := map[string]string{
		"x.md": importFile("X", "1", "linked_controls: [no-such-bundle]\n"),
		"y.md": importFile("Y", "1", "linked_controls: ["+uuid.NewString()+"]\n"),
	}
	items, failures := parseAll(t, files, "x.md", "y.md")
	report, err := store.Import(ctx, items, failures, "key_admin", true)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, r := range report.Results {
		if r.Status != policy.ImportStatusFailed {
			t.Errorf("%s = %s, want failed (unknown linked control)", r.SourceFile, r.Status)
		}
	}
	if n := countPolicies(t, admin, tenant); n != 0 {
		t.Fatalf("found %d policies", n)
	}
}

func TestImport_DuplicateSkipped(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	if _, err := store.Create(ctx, policy.CreateInput{
		Title: "Existing", Version: "1.0", BodyMd: "b", OwnerRole: "o", ApproverRole: "a", CreatedBy: "key_author",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	files := map[string]string{
		"existing.md": importFile("Existing", "1.0", ""),
		"new.md":      importFile("New", "1.0", ""),
		"new-dup.md":  importFile("New", "1.0", ""),
	}
	items, failures := parseAll(t, files, "existing.md", "new.md", "new-dup.md")
	report, err := store.Import(ctx, items, failures, "key_admin", false)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !report.Committed {
		t.Fatalf("skips are not failures; report = %+v", report)
	}
	want := map[string]string{
		"existing.md": policy.ImportStatusSkipped,
		"new.md":      policy.ImportStatusImported,
		"new-dup.md":  policy.ImportStatusSkipped,
	}
	for _, r := range report.Results {
		if r.Status != want[r.SourceFile] {
			t.Errorf("%s = %s (%s), want %s", r.SourceFile, r.Status, r.Reason, want[r.SourceFile])
		}
		if r.Status == policy.ImportStatusSkipped && r.Reason == "" {
			t.Errorf("%s: a skip must say why", r.SourceFile)
		}
	}
	if n := countPolicies(t, admin, tenant); n != 2 {
		t.Fatalf("found %d policies, want 2", n)
	}
}

// The next version of an imported policy is a draft whose predecessor is
// the imported policy, and it publishes only through the normal workflow.
func TestImport_NextVersionUsesNormalWorkflow(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	items, failures := parseAll(t, map[string]string{"p.md": importFile("Chain Policy", "1.0", "")}, "p.md")
	report, err := store.Import(ctx, items, failures, "key_admin", false)
	if err != nil || !report.Committed {
		t.Fatalf("Import: %v %+v", err, report)
	}
	imported := report.Results[0].Policy

	in := validCreate([]uuid.UUID{ctrl}, "key_author")
	in.Title = imported.Title
	in.Version = "2.0"
	in.PredecessorID = &imported.ID
	draft, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create next version: %v", err)
	}
	if draft.Status != policy.StateDraft || draft.PredecessorID == nil || *draft.PredecessorID != imported.ID {
		t.Fatalf("draft = status %s predecessor %v", draft.Status, draft.PredecessorID)
	}
	if draft.IsImported() {
		t.Fatal("the next version is tenant-authored, not imported")
	}

	// Publish only from approved.
	if _, err := store.Publish(ctx, draft.ID, policy.PublishInput{NewVersion: "2.0", PublishedBy: "key_approver"}); !errors.Is(err, policy.ErrWrongState) {
		t.Fatalf("publish of a draft: want ErrWrongState, got %v", err)
	}
	if _, err := store.SubmitForReview(ctx, draft.ID, "key_author"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := store.Approve(ctx, draft.ID, "key_approver"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	published, err := store.Publish(ctx, draft.ID, policy.PublishInput{NewVersion: "2.0", PublishedBy: "key_approver"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if published.ID != draft.ID || published.Status != policy.StatePublished || published.Version != "2.0" {
		t.Fatalf("published = %s %s %s", published.ID, published.Status, published.Version)
	}
	prior, err := store.Get(ctx, imported.ID)
	if err != nil {
		t.Fatalf("get prior: %v", err)
	}
	if prior.Status != policy.StateSuperseded || !prior.IsImported() {
		t.Fatalf("prior = %s source %s", prior.Status, prior.SourceAttribution)
	}
	chain, err := store.VersionChain(ctx, imported.ID)
	if err != nil || len(chain) != 2 || chain[1].ID != draft.ID {
		t.Fatalf("chain = %v (%v)", chain, err)
	}

	// A predecessor takes one successor; a non-published one takes none.
	in.Version = "3.0"
	if _, err := store.Create(ctx, in); !errors.Is(err, policy.ErrPredecessorNotPublished) {
		t.Errorf("superseded predecessor: want ErrPredecessorNotPublished, got %v", err)
	}
	in.PredecessorID = &published.ID
	if _, err := store.Create(ctx, in); err != nil {
		t.Fatalf("draft v3: %v", err)
	}
	in.Version = "3.1"
	if _, err := store.Create(ctx, in); !errors.Is(err, policy.ErrPredecessorTaken) {
		t.Errorf("second successor: want ErrPredecessorTaken, got %v", err)
	}
	missing := uuid.New()
	in.PredecessorID = &missing
	if _, err := store.Create(ctx, in); !errors.Is(err, policy.ErrPredecessorNotFound) {
		t.Errorf("missing predecessor: want ErrPredecessorNotFound, got %v", err)
	}
}

func TestImport_SameVersionAsPredecessorRejectedAtPublish(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	items, failures := parseAll(t, map[string]string{"p.md": importFile("Same", "1.0", "")}, "p.md")
	report, err := store.Import(ctx, items, failures, "key_admin", false)
	if err != nil || !report.Committed {
		t.Fatalf("Import: %v %+v", err, report)
	}
	in := validCreate([]uuid.UUID{ctrl}, "key_author")
	in.Version = "1.0"
	in.PredecessorID = &report.Results[0].Policy.ID
	draft, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.SubmitForReview(ctx, draft.ID, "key_author"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(ctx, draft.ID, "key_approver"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(ctx, draft.ID, policy.PublishInput{NewVersion: "1.0", PublishedBy: "key_approver"}); !errors.Is(err, policy.ErrInvalidVersion) {
		t.Fatalf("want ErrInvalidVersion, got %v", err)
	}
	// The failed publish rolled back: the imported row is still published.
	prior, err := store.Get(ctx, report.Results[0].Policy.ID)
	if err != nil || prior.Status != policy.StatePublished {
		t.Fatalf("prior = %s (%v)", prior.Status, err)
	}
}

func TestCreate_RejectsImportedAttribution(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	store := policy.NewStore(app)
	ctx := ctxFor(t, tenant)

	in := validCreate(nil, "key_author")
	in.SourceAttribution = policy.SourceImported
	if _, err := store.Create(ctx, in); !errors.Is(err, policy.ErrInvalidSourceAttribution) {
		t.Fatalf("want ErrInvalidSourceAttribution, got %v", err)
	}
}
