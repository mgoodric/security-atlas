package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/controlimport"
)

func exportManifest(t *testing.T, bundleID, orgID string) controlimport.ExportedBundle {
	t.Helper()
	raw, err := yaml.Marshal(control.Manifest{
		BundleSchemaVersion: control.BundleSchemaVersion,
		BundleID:            bundleID,
		Title:               "Imported: " + orgID,
		Description:         "From the sheet",
		SCFAnchorID:         "IAC-06",
		SCFRelease:          "2025.2",
		ImplementationType:  controlimport.DefaultImplementationType,
		ExternalRefs: []control.ExternalRef{
			{System: controlimport.OrgControlRefSystem, Kind: control.ExternalRefKindReference, ID: orgID},
			{System: "soc2", Kind: control.ExternalRefKindTestedUnder, ID: "CC6.1"},
		},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return controlimport.ExportedBundle{BundleID: bundleID, ManifestYAML: raw}
}

func exportServer(t *testing.T, status int, body []byte, gotPath *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.Method + " " + r.URL.Path
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestControlsImportExport_WritesBundlesThatValidate(t *testing.T) {
	resetControlsAuth(t)
	bundles := []controlimport.ExportedBundle{
		exportManifest(t, "org-ac-01", "ORG-AC-01"),
		exportManifest(t, "org-ac-02", "ORG-AC-02"),
	}
	var archive bytes.Buffer
	if err := controlimport.WriteBundleArchive(&archive, bundles); err != nil {
		t.Fatalf("WriteBundleArchive: %v", err)
	}
	var gotPath string
	srv := exportServer(t, http.StatusOK, archive.Bytes(), &gotPath)
	dir := filepath.Join(t.TempDir(), "controls")

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-123",
		"controls", "import", "export", "batch-1", "--out", dir)
	if err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}
	if gotPath != "GET /v1/controls/imports/batch-1/export" {
		t.Errorf("request = %q", gotPath)
	}
	if !strings.Contains(out, "wrote 2 bundles under "+dir) {
		t.Errorf("output = %q", out)
	}
	for _, b := range bundles {
		got, err := os.ReadFile(filepath.Join(dir, b.BundleID, "control.yaml"))
		if err != nil {
			t.Fatalf("read %s: %v", b.BundleID, err)
		}
		if !bytes.Equal(got, b.ManifestYAML) {
			t.Errorf("%s/control.yaml differs from the exported manifest", b.BundleID)
		}
	}

	// `controls validate` passes on the directory the export wrote, and on
	// one bundle of it.
	out, err = runImportCmd(t, "controls", "validate", dir)
	if err != nil {
		t.Fatalf("validate %s: %v\n%s", dir, err, out)
	}
	for _, want := range []string{"OK bundle_id=org-ac-01", "OK bundle_id=org-ac-02", "scf_release=2025.2"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "WARN") {
		t.Errorf("validate warned on an exported bundle:\n%s", out)
	}
	if out, err := runImportCmd(t, "controls", "validate", filepath.Join(dir, "org-ac-01")); err != nil {
		t.Errorf("validate one bundle: %v\n%s", err, out)
	}
}

func TestControlsImportExport_SurfacesAPIError(t *testing.T) {
	resetControlsAuth(t)
	var gotPath string
	srv := exportServer(t, http.StatusNotFound, []byte(`{"error":"import batch not found"}`), &gotPath)
	dir := filepath.Join(t.TempDir(), "controls")
	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok",
		"controls", "import", "export", "nope", "--out", dir)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "import batch not found") {
		t.Fatalf("err = %v, want the API's 404", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the target directory was created for a failed export")
	}
}

func TestControlsImportExport_NeedsOut(t *testing.T) {
	resetControlsAuth(t)
	_, err := runImportCmd(t, "--token", "tok", "controls", "import", "export", "batch-1")
	if err == nil || !strings.Contains(err.Error(), "--out is required") {
		t.Fatalf("err = %v, want --out is required", err)
	}
}

func TestControlsValidate_DirectoryWithoutBundles(t *testing.T) {
	resetControlsAuth(t)
	_, err := runImportCmd(t, "controls", "validate", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no bundle directory under it") {
		t.Fatalf("err = %v, want a no-bundles refusal", err)
	}
}

func TestControlsValidate_DirectoryNamesFailingBundle(t *testing.T) {
	resetControlsAuth(t)
	dir := t.TempDir()
	good := exportManifest(t, "org-ac-01", "ORG-AC-01")
	for id, raw := range map[string][]byte{"org-ac-01": good.ManifestYAML, "zz-bad": []byte("bundle_id: Not Valid\n")} {
		if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id, "control.yaml"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := runImportCmd(t, "controls", "validate", dir)
	if err == nil || !strings.Contains(err.Error(), "zz-bad") {
		t.Fatalf("err = %v, want the failing bundle named", err)
	}
}
