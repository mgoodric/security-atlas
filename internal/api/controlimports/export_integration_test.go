//go:build integration

// #1784 — integration tests for exporting a batch's approved controls as
// control.yaml bundles. Real Postgres with RLS and the assembled router, on
// the fixtures of integration_test.go and decisions_integration_test.go.
package controlimports_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/controlimport"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// readExport splits an export archive into its control.yaml files, keyed
// by bundle directory, refusing any other regular-file entry.
func readExport(t *testing.T, raw []byte) (map[string][]byte, []string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("export is not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	var order []string
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read export tar: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		dir, file := path.Split(hdr.Name)
		if file != "control.yaml" || dir == "" {
			t.Fatalf("unexpected export entry %q", hdr.Name)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		id := path.Clean(dir)
		out[id] = body
		order = append(order, id)
	}
	return out, order
}

// ----- AC: the export holds one valid bundle per approved control, with
// anchor, SCF release and external references; re-uploading is a no-op -----

func TestExportApprovedControlsAsBundles(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	cat := seedCatalog(t, admin)
	tenant := freshTenant(t, admin)
	other := freshTenant(t, admin)
	env := decisionServer(t, app, tenant)
	otherEnv := decisionServer(t, app, other)

	b := stageFixture(t, env.testEnv, cat.framework)
	exportPath := "/v1/controls/imports/" + b.ID + "/export"

	// Nothing approved yet: an empty archive.
	status, raw := doGet(t, env.testEnv, exportPath)
	if status != http.StatusOK {
		t.Fatalf("export before approval = %d; body=%s", status, raw)
	}
	if got, _ := readExport(t, raw); len(got) != 0 {
		t.Errorf("export before approval holds %d bundles, want 0", len(got))
	}

	approvals := []struct {
		orgID string
		body  map[string]any
		scfID string
	}{
		{"ORG-AC-01", map[string]any{"candidate_rank": 1}, cat.anchors["mfa"]},
		{"ORG-AC-02", map[string]any{"scf_id": cat.anchors["review"]}, cat.anchors["review"]},
	}
	decisions := map[string]controlimport.RowDecision{}
	for _, a := range approvals {
		status, raw := doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, rowByOrgID(t, b, a.orgID).ID, "approve"), a.body)
		if status != http.StatusOK {
			t.Fatalf("approve %s = %d; body=%s", a.orgID, status, raw)
		}
		d := decodeDecision(t, raw)
		decisions[d.BundleID] = d
	}
	// A rejected and a pending row are not exported.
	status, raw = doPost(t, env.testEnv, env.bearer, decisionPath(b.ID, rowByOrgID(t, b, "ORG-OP-01").ID, "reject"),
		map[string]any{"reason": "covered elsewhere"})
	if status != http.StatusOK {
		t.Fatalf("reject = %d; body=%s", status, raw)
	}

	req, _ := http.NewRequest(http.MethodGet, env.server.URL+exportPath, nil)
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET export: %v", err)
	}
	raw, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/gzip" ||
		resp.Header.Get("X-Bundle-Count") != "2" {
		t.Fatalf("export = %d %q count=%q; body=%s", resp.StatusCode, resp.Header.Get("Content-Type"),
			resp.Header.Get("X-Bundle-Count"), raw)
	}
	files, order := readExport(t, raw)
	if len(files) != 2 || order[0] != "org-ac-01" || order[1] != "org-ac-02" {
		t.Fatalf("export bundles = %v, want org-ac-01 and org-ac-02 in row order", order)
	}

	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("tenant context: %v", err)
	}
	store := control.NewStore(app)
	for _, a := range approvals {
		d := decisions[controlimport.BundleIDFor(a.orgID)]
		manifest := files[d.BundleID]
		bundle, err := control.FinalizeBundleForHTTP(manifest)
		if err != nil {
			t.Fatalf("%s: exported control.yaml does not validate: %v\n%s", d.BundleID, err, manifest)
		}
		if err := bundle.ValidateApplicabilityExpr(); err != nil {
			t.Errorf("%s: applicability: %v", d.BundleID, err)
		}
		m := bundle.Manifest
		if m.BundleID != d.BundleID || m.SCFAnchorID != a.scfID || m.SCFRelease != cat.scfRelease {
			t.Errorf("%s: manifest anchor %q release %q, want %q in %q", d.BundleID, m.SCFAnchorID, m.SCFRelease, a.scfID, cat.scfRelease)
		}
		if len(m.ExternalRefs) == 0 || m.ExternalRefs[0] != (control.ExternalRef{
			System: controlimport.OrgControlRefSystem, Kind: control.ExternalRefKindReference, ID: a.orgID,
		}) {
			t.Errorf("%s: external_refs = %+v, want the organisation control ID first", d.BundleID, m.ExternalRefs)
		}
		if len(m.ExternalRefs) != len(d.ExternalRefs) {
			t.Errorf("%s: external_refs = %+v, want the approved control's %+v", d.BundleID, m.ExternalRefs, d.ExternalRefs)
		}
		for i := range d.ExternalRefs {
			if i < len(m.ExternalRefs) && m.ExternalRefs[i] != d.ExternalRefs[i] {
				t.Errorf("%s: external_refs[%d] = %+v, want %+v", d.BundleID, i, m.ExternalRefs[i], d.ExternalRefs[i])
			}
		}

		// Re-uploading the exported bundle unchanged is a no-op: the same
		// control, the same version, nothing updated in place.
		res, err := store.Upload(ctx, bundle, env.user.String())
		if err != nil {
			t.Fatalf("%s: re-upload: %v", d.BundleID, err)
		}
		if res.IsNewBundle || res.UpdatedInPlace || res.ControlID.String() != d.ControlID || res.Version != 1 {
			t.Errorf("%s: re-upload = %+v, want a no-op on control %s version 1", d.BundleID, res, d.ControlID)
		}
		if res.AnchorID.String() != d.SCFAnchorID {
			t.Errorf("%s: re-upload re-bound the anchor to %s, want %s", d.BundleID, res.AnchorID, d.SCFAnchorID)
		}
		var rows int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM controls WHERE tenant_id = $1 AND bundle_id = $2`, tenant, d.BundleID).Scan(&rows); err != nil {
			t.Fatalf("count controls: %v", err)
		}
		if rows != 1 {
			t.Errorf("%s: %d control rows after re-upload, want 1", d.BundleID, rows)
		}
	}

	// Another tenant cannot export the batch.
	if status, raw := doGet(t, otherEnv.testEnv, exportPath); status != http.StatusNotFound {
		t.Errorf("tenant B export = %d %s, want 404", status, raw)
	}
	if status, _ := doGet(t, env.testEnv, "/v1/controls/imports/not-a-uuid/export"); status != http.StatusNotFound {
		t.Errorf("export of a malformed id = %d, want 404", status)
	}
}
