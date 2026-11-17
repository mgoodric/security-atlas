package controls

// #2110 — GET /v1/controls/{id}/bundle, pure-Go tier: the archive parses
// back through control.ParseTarball (the upload path's reader) to the same
// manifest, and an unknown control is 404.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/control"
)

type stubManifests struct {
	bundleID string
	manifest []byte
	err      error
}

func (s stubManifests) BundleManifest(context.Context, uuid.UUID) (string, []byte, error) {
	return s.bundleID, s.manifest, s.err
}

func getBundle(t *testing.T, h *BundleDownloadHandler, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/v1/controls/{id}/bundle", h.Download)
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/"+id+"/bundle", nil)
	req = req.WithContext(withAuthAndTenant(req.Context(), adminCred()))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestBundleDownload_RoundTripsThroughUploadParser(t *testing.T) {
	t.Parallel()
	h := &BundleDownloadHandler{store: stubManifests{bundleID: "gate_jsonpath_control", manifest: []byte(jsonpathManifest)}}
	rec := getBundle(t, h, uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="gate_jsonpath_control.tar.gz"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	b, err := control.ParseTarball(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("ParseTarball: %v", err)
	}
	if b.Manifest.BundleID != "gate_jsonpath_control" {
		t.Fatalf("bundle_id = %q", b.Manifest.BundleID)
	}
	if !bytes.Equal(b.ManifestYAMLRaw, []byte(jsonpathManifest)) {
		t.Fatalf("manifest bytes changed in the archive")
	}
}

func TestBundleDownload_NotFound(t *testing.T) {
	t.Parallel()
	h := &BundleDownloadHandler{store: stubManifests{err: control.ErrControlNotFound}}
	if rec := getBundle(t, h, uuid.NewString()); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestBundleDownload_NoStoredManifestIs404(t *testing.T) {
	t.Parallel()
	h := &BundleDownloadHandler{store: stubManifests{bundleID: "seeded_ctrl"}}
	if rec := getBundle(t, h, uuid.NewString()); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}

func TestBundleDownload_BadID(t *testing.T) {
	t.Parallel()
	h := &BundleDownloadHandler{store: stubManifests{}}
	if rec := getBundle(t, h, "not-a-uuid"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rec.Code)
	}
}
