//go:build integration

// Issue #1908: a description-only re-upload updates the active control in
// place, so rows keyed on the control id stay attached; any other change
// still supersedes.

package control_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func manifestWith(bundleID, anchor, title, description string) []byte {
	return []byte(`bundle_schema_version: "1"
bundle_id: ` + bundleID + `
title: "` + title + `"
description: "` + description + `"
scf_anchor_id: ` + anchor + `
implementation_type: manual_attested
`)
}

// seedControlLinks attaches an owner, an evidence record, an active
// exception and a risk link to controlID through the migrate pool.
func seedControlLinks(t *testing.T, admin *pgxpool.Pool, tenant string, controlID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO control_owner_assignments (tenant_id, control_id, owner_user_id, assigned_by)
		  VALUES ($1, $2, $3, $4)`, []any{tenant, controlID, uuid.New(), uuid.New()}},
		{`INSERT INTO evidence_records (id, tenant_id, control_id, control_ref, observed_at,
		      provenance, result, hash)
		  VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, 'pass', $6)`,
			[]any{uuid.New(), tenant, controlID, controlID.String(), now, hashOf(controlID.String())}},
		{`INSERT INTO exceptions (id, tenant_id, control_id, scope_cell_predicate, justification,
		      requested_by, approved_by, approved_at, expires_at, status)
		  VALUES ($1, $2, $3, '{"op":"true"}'::jsonb, 'compensated', 'requester', 'approver',
		      $4, $5, 'active')`,
			[]any{uuid.New(), tenant, controlID, now, now.Add(30 * 24 * time.Hour)}},
	}
	for _, s := range stmts {
		if _, err := admin.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed link: %v\n%s", err, s.sql)
		}
	}
	riskID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO risks (id, tenant_id, title, category)
		VALUES ($1, $2, 'Linked risk', 'operational')`, riskID, tenant); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO risk_control_links (risk_id, control_id, tenant_id)
		VALUES ($1, $2, $3)`, riskID, controlID, tenant); err != nil {
		t.Fatalf("seed risk link: %v", err)
	}
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// linkCounts returns how many owner, evidence, active-exception and
// risk-link rows reference controlID.
func linkCounts(t *testing.T, admin *pgxpool.Pool, tenant string, controlID uuid.UUID) map[string]int {
	t.Helper()
	queries := map[string]string{
		"owner":     `SELECT count(*) FROM control_owner_assignments WHERE tenant_id = $1 AND control_id = $2`,
		"evidence":  `SELECT count(*) FROM evidence_records WHERE tenant_id = $1 AND control_id = $2`,
		"exception": `SELECT count(*) FROM exceptions WHERE tenant_id = $1 AND control_id = $2 AND status = 'active'`,
		"risk_link": `SELECT count(*) FROM risk_control_links WHERE tenant_id = $1 AND control_id = $2`,
	}
	out := map[string]int{}
	for name, q := range queries {
		var n int
		if err := admin.QueryRow(context.Background(), q, tenant, controlID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		out[name] = n
	}
	return out
}

func TestUpload_DescriptionOnlyChangeUpdatesInPlace(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	_, code := seedSCFAnchor(t, admin, "IAC-1908", "IAC")
	tenant := dbtest.SeedTenant(t, admin,
		"control_manifest_revisions", "risk_control_links", "risks", "exceptions",
		"control_owner_assignments", "evidence_records", "controls",
	)
	store := control.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	b1, err := control.FinalizeBundleForHTTP(manifestWith("in_place", code, "Access review", "Quarterly access review."))
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	res1, err := store.Upload(ctx, b1, "key_admin")
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	seedControlLinks(t, admin, tenant, res1.ControlID)
	want := map[string]int{"owner": 1, "evidence": 1, "exception": 1, "risk_link": 1}

	// Description-only edit: same control id, same version, nothing superseded.
	b2, err := control.FinalizeBundleForHTTP(manifestWith("in_place", code, "Access review", "Access is reviewed every quarter by the owner."))
	if err != nil {
		t.Fatalf("parse v1 reworded: %v", err)
	}
	res2, err := store.Upload(ctx, b2, "key_admin")
	if err != nil {
		t.Fatalf("description-only upload: %v", err)
	}
	if !res2.UpdatedInPlace || res2.ControlID != res1.ControlID || res2.Version != 1 ||
		res2.IsNewBundle || res2.SupersededID != (uuid.UUID{}) {
		t.Fatalf("description-only upload should update in place; got %+v (prior id %s)", res2, res1.ControlID)
	}

	var (
		description, hash string
		total             int
	)
	if err := admin.QueryRow(context.Background(), `
		SELECT description, bundle_manifest_hash FROM controls WHERE id = $1 AND superseded_by IS NULL
	`, res1.ControlID).Scan(&description, &hash); err != nil {
		t.Fatalf("read active row: %v", err)
	}
	if description != "Access is reviewed every quarter by the owner." {
		t.Fatalf("description not updated: %q", description)
	}
	if hash != b2.ManifestHashHex {
		t.Fatalf("hash not updated: got %s, want %s", hash, b2.ManifestHashHex)
	}
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM controls WHERE tenant_id = $1 AND bundle_id = 'in_place'
	`, tenant).Scan(&total); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if total != 1 {
		t.Fatalf("description-only upload inserted a row: %d controls rows", total)
	}
	if got := linkCounts(t, admin, tenant, res1.ControlID); !maps.Equal(got, want) {
		t.Fatalf("links after description-only upload: got %v, want %v", got, want)
	}

	// The overwritten manifest is kept as one revision row.
	var revYAML, revHash, revBy string
	if err := admin.QueryRow(context.Background(), `
		SELECT bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_by
		FROM control_manifest_revisions WHERE tenant_id = $1 AND control_id = $2
	`, tenant, res1.ControlID).Scan(&revYAML, &revHash, &revBy); err != nil {
		t.Fatalf("read revision row: %v", err)
	}
	if revYAML != string(b1.ManifestYAMLRaw) || revHash != b1.ManifestHashHex || revBy != "key_admin" {
		t.Fatalf("revision row should hold the v1 manifest; got hash %s by %q, want %s", revHash, revBy, b1.ManifestHashHex)
	}
	countRevisions := func() int {
		t.Helper()
		var n int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM control_manifest_revisions WHERE tenant_id = $1 AND control_id = $2
		`, tenant, res1.ControlID).Scan(&n); err != nil {
			t.Fatalf("count revisions: %v", err)
		}
		return n
	}

	// The same bytes again are a no-op, because the hash moved with the YAML.
	b2again, _ := control.FinalizeBundleForHTTP(manifestWith("in_place", code, "Access review", "Access is reviewed every quarter by the owner."))
	res3, err := store.Upload(ctx, b2again, "key_admin")
	if err != nil {
		t.Fatalf("repeat upload: %v", err)
	}
	if res3.UpdatedInPlace || res3.ControlID != res1.ControlID || res3.Version != 1 {
		t.Fatalf("repeat upload should be a no-op; got %+v", res3)
	}
	if n := countRevisions(); n != 1 {
		t.Fatalf("repeat upload added a revision row: %d rows, want 1", n)
	}

	// A title change is not prose-only: it supersedes, as before.
	b4, _ := control.FinalizeBundleForHTTP(manifestWith("in_place", code, "Access recertification", "Access is reviewed every quarter by the owner."))
	res4, err := store.Upload(ctx, b4, "key_admin")
	if err != nil {
		t.Fatalf("title-change upload: %v", err)
	}
	if res4.UpdatedInPlace || res4.Version != 2 || res4.SupersededID != res1.ControlID || res4.ControlID == res1.ControlID {
		t.Fatalf("title change should supersede; got %+v", res4)
	}
	if n := countRevisions(); n != 1 {
		t.Fatalf("a superseding upload wrote a revision row: %d rows, want 1", n)
	}
}

// Issue #1958: adding external_refs to a control that already has
// evidence updates it in place, so the evidence stays attached and both
// the control (by its new ref) and its evidence stay searchable.
func TestUpload_ExternalRefsOnlyChangeUpdatesInPlace(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	_, code := seedSCFAnchor(t, admin, "IAC-1958", "IAC")
	tenant := dbtest.SeedTenant(t, admin,
		"control_manifest_revisions", "risk_control_links", "risks", "exceptions",
		"control_owner_assignments", "evidence_records", "controls",
	)
	store := control.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	v1 := manifestWith("refs_in_place", code, "Risk assessment", "Annual risk assessment.")
	b1, err := control.FinalizeBundleForHTTP(v1)
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	res1, err := store.Upload(ctx, b1, "key_admin")
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	seedControlLinks(t, admin, tenant, res1.ControlID)
	want := map[string]int{"owner": 1, "evidence": 1, "exception": 1, "risk_link": 1}

	withRefs := string(v1) + `external_refs:
  - system: acme-audit
    id: ZQRA-1958
  - system: soc2
    kind: tested_under
    id: CC3.1
    note: "quokkareport section 4"
`
	b2, err := control.FinalizeBundleForHTTP([]byte(withRefs))
	if err != nil {
		t.Fatalf("parse v1 with refs: %v", err)
	}
	res2, err := store.Upload(ctx, b2, "key_admin")
	if err != nil {
		t.Fatalf("refs-only upload: %v", err)
	}
	if !res2.UpdatedInPlace || res2.ControlID != res1.ControlID || res2.Version != 1 ||
		res2.SupersededID != (uuid.UUID{}) {
		t.Fatalf("refs-only upload should update in place; got %+v (prior id %s)", res2, res1.ControlID)
	}
	if got := linkCounts(t, admin, tenant, res1.ControlID); !maps.Equal(got, want) {
		t.Fatalf("links after refs-only upload: got %v, want %v", got, want)
	}

	var refs, hash string
	if err := admin.QueryRow(context.Background(), `
		SELECT external_refs::text, bundle_manifest_hash FROM controls
		WHERE id = $1 AND superseded_by IS NULL
	`, res1.ControlID).Scan(&refs, &hash); err != nil {
		t.Fatalf("read active row: %v", err)
	}
	if !strings.Contains(refs, "ZQRA-1958") || !strings.Contains(refs, "tested_under") {
		t.Fatalf("external_refs not updated: %s", refs)
	}
	if hash != b2.ManifestHashHex {
		t.Fatalf("hash not updated: got %s, want %s", hash, b2.ManifestHashHex)
	}
	var revHash string
	if err := admin.QueryRow(context.Background(), `
		SELECT bundle_manifest_hash FROM control_manifest_revisions
		WHERE tenant_id = $1 AND control_id = $2
	`, tenant, res1.ControlID).Scan(&revHash); err != nil {
		t.Fatalf("read revision row: %v", err)
	}
	if revHash != b1.ManifestHashHex {
		t.Fatalf("revision row should hold the v1 manifest; got %s, want %s", revHash, b1.ManifestHashHex)
	}

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"}))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	search := func(q, types string) []string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/search?q="+q+"&types="+types, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("search %s: %v", q, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("search %s: status %d", q, resp.StatusCode)
		}
		var body struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode search: %v", err)
		}
		ids := make([]string, 0, len(body.Hits))
		for _, h := range body.Hits {
			ids = append(ids, h.ID)
		}
		return ids
	}
	for _, q := range []string{"zqra-1958", "quokkareport"} {
		if ids := search(q, "controls"); len(ids) != 1 || ids[0] != res1.ControlID.String() {
			t.Fatalf("q=%s: want exactly control %s, got %v", q, res1.ControlID, ids)
		}
	}
	// seedControlLinks writes the evidence with control_ref = the control id.
	if ids := search(res1.ControlID.String(), "evidence"); len(ids) != 1 {
		t.Fatalf("the control's evidence should still be searchable; got %v", ids)
	}
}

// Issue #1958: concurrent in-place uploads of one control serialise on the
// active row, so every overwritten manifest lands in
// control_manifest_revisions exactly once and none is lost.
func TestUpload_ConcurrentInPlaceUploadsKeepEveryRevision(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	_, code := seedSCFAnchor(t, admin, "IAC-1958C", "IAC")
	tenant := dbtest.SeedTenant(t, admin, "control_manifest_revisions", "controls")
	store := control.NewStore(app)
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}

	b1, err := control.FinalizeBundleForHTTP(manifestWith("concurrent", code, "Access review", "Original."))
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	res1, err := store.Upload(ctx, b1, "key_admin")
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}

	const n = 8
	hashes := map[string]bool{b1.ManifestHashHex: true}
	bundles := make([]*control.Bundle, n)
	for i := range bundles {
		b, err := control.FinalizeBundleForHTTP(manifestWith("concurrent", code, "Access review", fmt.Sprintf("Edit %d.", i)))
		if err != nil {
			t.Fatalf("parse edit %d: %v", i, err)
		}
		bundles[i] = b
		hashes[b.ManifestHashHex] = true
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i, b := range bundles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := store.Upload(ctx, b, "key_admin")
			if err == nil && (!res.UpdatedInPlace || res.ControlID != res1.ControlID) {
				err = fmt.Errorf("want an in-place update of %s, got %+v", res1.ControlID, res)
			}
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}

	// The n revision rows plus the active row hold each of the n+1
	// manifests exactly once.
	seen := map[string]int{}
	rows, err := admin.Query(context.Background(), `
		SELECT bundle_manifest_hash FROM control_manifest_revisions
		WHERE tenant_id = $1 AND control_id = $2
		UNION ALL
		SELECT bundle_manifest_hash FROM controls WHERE id = $2 AND superseded_by IS NULL
	`, tenant, res1.ControlID)
	if err != nil {
		t.Fatalf("read manifests: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[h]++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(seen) != len(hashes) {
		t.Fatalf("want %d distinct manifests across revisions + active row, got %d: %v", len(hashes), len(seen), seen)
	}
	for h, c := range seen {
		if !hashes[h] || c != 1 {
			t.Fatalf("manifest %s seen %d times (known=%v); all: %v", h, c, hashes[h], seen)
		}
	}
}
