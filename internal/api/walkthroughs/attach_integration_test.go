//go:build integration

// security-atlas#1939 — the attachment handler validates the walkthrough
// BEFORE it stores the blob, so a refused attach (missing or finalized
// walkthrough) leaves no artifact nothing references. Real Postgres + the
// real walkthrough.Store; the artifact store is a recording fake so the
// test can count uploads without MinIO.

package walkthroughs_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	walkthroughsapi "github.com/mgoodric/security-atlas/internal/api/walkthroughs"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// recordingUploader is an ArtifactUploader that hashes what it is handed
// and remembers each call.
type recordingUploader struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (u *recordingUploader) PutReader(_ context.Context, in artifact.PutInput, body io.ReadSeeker) (artifact.Artifact, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return artifact.Artifact{}, err
	}
	u.mu.Lock()
	u.bodies = append(u.bodies, b)
	u.mu.Unlock()
	sum := sha256.Sum256(b)
	return artifact.Artifact{
		ID:          uuid.New(),
		StorageKey:  "tenant-test/" + uuid.NewString(),
		ContentHash: hex.EncodeToString(sum[:]),
		SizeBytes:   int64(len(b)),
		ContentType: in.ContentType,
		UploadedBy:  in.UploadedBy,
	}, nil
}

func (u *recordingUploader) calls() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.bodies)
}

func attach(t *testing.T, h *walkthroughsapi.Handler, tenant, id string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "evidence.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	ctx := dbtest.WithTenantCtx(t, tenant)
	ctx = authctx.WithCredential(ctx, credstore.Credential{ID: "key_test_1939", TenantID: tenant, IsAdmin: true})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req := httptest.NewRequest(http.MethodPost, "/v1/walkthroughs/"+id+"/attachments", &buf).WithContext(ctx)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.AddAttachment(rec, req)
	return rec
}

func TestAttach_FinalizedWalkthroughStoresNoBlob(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	id := seedWalkthrough(t, app, tenant, ctrl)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	if _, err := store.Finalize(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(id), "key_test_1939"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	up := &recordingUploader{}
	rec := attach(t, walkthroughsapi.New(store, up), tenant, id, []byte("late evidence"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("attach to finalized walkthrough = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if n := up.calls(); n != 0 {
		t.Fatalf("artifact store received %d uploads for a refused attach, want 0", n)
	}
}

func TestAttach_MissingWalkthroughStoresNoBlob(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	up := &recordingUploader{}
	rec := attach(t, walkthroughsapi.New(store, up), tenant, uuid.NewString(), []byte("orphan evidence"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("attach to missing walkthrough = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if n := up.calls(); n != 0 {
		t.Fatalf("artifact store received %d uploads for a refused attach, want 0", n)
	}
}

func TestAttach_DraftWalkthroughStreamsBlobAndRecordsHash(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	id := seedWalkthrough(t, app, tenant, ctrl)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	up := &recordingUploader{}
	body := []byte("screenshot of the key rotation log")
	rec := attach(t, walkthroughsapi.New(store, up), tenant, id, body)

	if rec.Code != http.StatusCreated {
		t.Fatalf("attach to draft walkthrough = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if n := up.calls(); n != 1 {
		t.Fatalf("artifact store uploads = %d, want 1", n)
	}
	if !bytes.Equal(up.bodies[0], body) {
		t.Fatalf("artifact store received %q, want %q", up.bodies[0], body)
	}
	wt, err := store.Get(dbtest.WithTenantCtx(t, tenant), uuid.MustParse(id))
	if err != nil {
		t.Fatalf("get walkthrough: %v", err)
	}
	sum := sha256.Sum256(body)
	if len(wt.Attachments) != 1 || wt.Attachments[0].SHA256Hex != hex.EncodeToString(sum[:]) {
		t.Fatalf("attachments = %+v, want one with sha256 %x", wt.Attachments, sum)
	}
}
