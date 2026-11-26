package artifact_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/artifact"
)

// zeros is an io.ReaderAt over an endless run of zero bytes, so a test can
// hand PutReader a body larger than the cap without allocating it.
type zeros struct{}

func (zeros) ReadAt(p []byte, _ int64) (int, error) {
	clear(p)
	return len(p), nil
}

// countingSeeker counts the bytes read through it.
type countingSeeker struct {
	io.ReadSeeker
	n int64
}

func (c *countingSeeker) Read(p []byte) (int, error) {
	n, err := c.ReadSeeker.Read(p)
	c.n += int64(n)
	return n, err
}

// security-atlas#1939 — an over-cap body is rejected after at most cap+1
// bytes, before any tenant, DB or S3 work.
func TestStore_PutReader_RejectsOversizedAfterCapPlusOne(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	body := &countingSeeker{ReadSeeker: io.NewSectionReader(zeros{}, 0, 4*artifact.MaxUploadBytes)}
	_, err := store.PutReader(ctx, artifact.PutInput{
		ContentType: "application/octet-stream",
		UploadedBy:  "test-key",
	}, body)
	if !errors.Is(err, artifact.ErrOversized) {
		t.Fatalf("PutReader with oversized body: want ErrOversized, got %v", err)
	}
	if body.n > artifact.MaxUploadBytes+1 {
		t.Fatalf("PutReader read %d bytes, want at most %d", body.n, artifact.MaxUploadBytes+1)
	}
}

func TestStore_PutReader_AcceptsBodyAtCap(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.PutReader(ctx, artifact.PutInput{
		ContentType: "application/octet-stream",
		UploadedBy:  "test-key",
	}, io.NewSectionReader(zeros{}, 0, artifact.MaxUploadBytes))
	if errors.Is(err, artifact.ErrOversized) {
		t.Fatalf("body exactly at MaxUploadBytes must not be ErrOversized; got %v", err)
	}
	// Past the size check, Begin fails on the unreachable pool.
	if err == nil || !strings.Contains(err.Error(), "begin tx") {
		t.Fatalf("PutReader err: got %v, want a begin tx error", err)
	}
}

func TestStore_PutReader_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.PutReader(ctx, artifact.PutInput{
		ContentType: "text/plain",
		UploadedBy:  "test-key",
	}, bytes.NewReader(nil))
	if !errors.Is(err, artifact.ErrInvalidInput) || !strings.Contains(err.Error(), "body must be non-empty") {
		t.Fatalf("PutReader with empty body: want ErrInvalidInput 'body must be non-empty', got %v", err)
	}
}

func TestStore_PutReader_RejectsBodyField(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.PutReader(ctx, artifact.PutInput{
		ContentType: "text/plain",
		UploadedBy:  "test-key",
		Body:        []byte("alpha"),
	}, bytes.NewReader([]byte("alpha")))
	if !errors.Is(err, artifact.ErrInvalidInput) {
		t.Fatalf("PutReader with in.Body set: want ErrInvalidInput, got %v", err)
	}
}

func TestStore_PutReader_RejectsMissingMeta(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	for name, in := range map[string]artifact.PutInput{
		"content_type": {UploadedBy: "test-key"},
		"uploaded_by":  {ContentType: "text/plain"},
	} {
		_, err := store.PutReader(ctx, in, bytes.NewReader([]byte("alpha")))
		if !errors.Is(err, artifact.ErrInvalidInput) || !strings.Contains(err.Error(), name+" is required") {
			t.Fatalf("PutReader missing %s: want ErrInvalidInput '%s is required', got %v", name, name, err)
		}
	}
}

func TestStore_PutReader_RejectsHashMismatch(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.PutReader(ctx, artifact.PutInput{
		ContentType: "text/plain",
		ContentHash: strings.Repeat("0", 64),
		UploadedBy:  "test-key",
	}, bytes.NewReader([]byte("alpha")))
	if !errors.Is(err, artifact.ErrHashMismatch) {
		t.Fatalf("PutReader with mismatched hash: want ErrHashMismatch, got %v", err)
	}
}

// errReader fails every read.
type errReader struct{}

func (errReader) Read([]byte) (int, error)       { return 0, errors.New("disk gone") }
func (errReader) Seek(int64, int) (int64, error) { return 0, nil }

func TestStore_PutReader_WrapsReadError(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	ctx := unitTenantCtx(t)

	_, err := store.PutReader(ctx, artifact.PutInput{
		ContentType: "text/plain",
		UploadedBy:  "test-key",
	}, errReader{})
	if err == nil || !strings.Contains(err.Error(), "read body: disk gone") {
		t.Fatalf("PutReader with failing reader: got %v, want wrapped read error", err)
	}
}
