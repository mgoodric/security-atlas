package manuals3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeS3 is a minimal httptest.Server that returns canned ListObjectsV2
// XML for one prefix. Lives only in the test file — production code talks
// to a real *s3.Client via the API interface.
func fakeS3(t *testing.T) (*httptest.Server, *recordedRequests) {
	t.Helper()
	rec := &recordedRequests{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		// Minimal LIST response with two objects.
		body := `<?xml version="1.0"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>evidence-bucket</Name>
  <Prefix>audits/</Prefix>
  <KeyCount>2</KeyCount>
  <IsTruncated>false</IsTruncated>
  <Contents>
    <Key>audits/q1.pdf</Key>
    <LastModified>2026-04-01T00:00:00.000Z</LastModified>
    <ETag>"etag-q1"</ETag>
    <Size>2048</Size>
  </Contents>
  <Contents>
    <Key>audits/q2.pdf</Key>
    <LastModified>2026-05-01T00:00:00.000Z</LastModified>
    <ETag>"etag-q2"</ETag>
    <Size>4096</Size>
  </Contents>
</ListBucketResult>`
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

type recordedRequests struct {
	paths []string
}

func (r *recordedRequests) add(req *http.Request) {
	r.paths = append(r.paths, req.URL.Path+"?"+req.URL.RawQuery)
}

func TestList_HappyPath(t *testing.T) {
	t.Parallel()
	srv, _ := fakeS3(t)
	api := newFakeAPI(srv.URL, "evidence-bucket", "audits/")

	objs, err := List(context.Background(), api, "evidence-bucket", "audits/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objs))
	}
	if objs[0].Key != "audits/q1.pdf" {
		t.Fatalf("unexpected key: %q", objs[0].Key)
	}
	if objs[0].ETag == "" {
		t.Fatal("etag must be populated for idempotency")
	}
	if strings.Contains(objs[0].ETag, `"`) {
		t.Fatalf("etag should be unquoted, got %q", objs[0].ETag)
	}
	if objs[0].Size != 2048 {
		t.Fatalf("size: got %d want 2048", objs[0].Size)
	}
	if objs[0].LastModified.IsZero() {
		t.Fatal("last_modified must be populated")
	}
	if !objs[0].LastModified.Equal(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("last_modified: got %v want 2026-04-01", objs[0].LastModified)
	}
}

func TestList_EmptyPrefixError(t *testing.T) {
	t.Parallel()
	_, err := List(context.Background(), nil, "evidence-bucket", "")
	if err == nil {
		t.Fatal("expected error on empty prefix")
	}
}

func TestList_EmptyBucketError(t *testing.T) {
	t.Parallel()
	_, err := List(context.Background(), nil, "", "audits/")
	if err == nil {
		t.Fatal("expected error on empty bucket")
	}
}

func TestList_APIError(t *testing.T) {
	t.Parallel()
	api := fakeAPIFunc(func(ctx context.Context, bucket, prefix string) ([]Object, error) {
		return nil, errors.New("boom")
	})
	_, err := List(context.Background(), api, "evidence-bucket", "audits/")
	if err == nil {
		t.Fatal("expected propagated error")
	}
}
