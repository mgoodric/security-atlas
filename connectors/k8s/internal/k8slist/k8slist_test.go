package k8slist

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// item is a neutral decode target for the tests — it models nothing real, just
// proves accumulation across pages.
type item struct {
	Name string `json:"name"`
}

const listPath = "/apis/example.test/v1/widgets"

// TestListAll_AccumulatesAcrossPages is the AC-2 multi-page proof: page 1 returns
// a non-empty metadata.continue token, page 2 returns an empty one, and the
// reader accumulates every item across both pages. It also asserts the
// `continue` query parameter is propagated verbatim on the follow-up request.
func TestListAll_AccumulatesAcrossPages(t *testing.T) {
	t.Parallel()
	var sawContinue string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != listPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("limit") == "" {
			t.Errorf("missing limit param on %s", r.URL.String())
		}
		switch q.Get("continue") {
		case "":
			// First page: two items + a continue token pointing at page 2.
			_, _ = w.Write([]byte(`{"metadata":{"continue":"TOKEN-PAGE-2"},"items":[{"name":"alpha"},{"name":"bravo"}]}`))
		case "TOKEN-PAGE-2":
			sawContinue = "TOKEN-PAGE-2"
			// Last page: one item + empty continue.
			_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[{"name":"charlie"}]}`))
		default:
			t.Errorf("unexpected continue token %q", q.Get("continue"))
		}
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "test-k8s-token")
	got, err := ListAll[item](context.Background(), r, listPath)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3 (2 from page 1 + 1 from page 2)", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("item[%d] = %q; want %q", i, got[i].Name, w)
		}
	}
	if sawContinue != "TOKEN-PAGE-2" {
		t.Errorf("page 2 was never requested with the continue token")
	}
}

// TestListAll_SinglePageEmptyContinue covers the common case: one page, empty
// continue token, walk terminates after a single request.
func TestListAll_SinglePageEmptyContinue(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[{"name":"solo"}]}`))
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "")
	got, err := ListAll[item](context.Background(), r, listPath)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(got) != 1 || got[0].Name != "solo" {
		t.Fatalf("got %+v; want one item 'solo'", got)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server hit %d times; want exactly 1 (empty continue terminates)", n)
	}
}

// TestListAll_PageCapTerminates is the AC-3 backstop proof: a server that ALWAYS
// returns a non-empty continue token must terminate at MaxListPages, not loop
// forever. We assert the walk stops after exactly MaxListPages requests and
// returns the items gathered up to the cap.
func TestListAll_PageCapTerminates(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		// Always hand back a fresh non-empty continue token — a pathological /
		// hostile API server that never signals the last page.
		_, _ = fmt.Fprintf(w, `{"metadata":{"continue":"never-ending-%d"},"items":[{"name":"x"}]}`, calls.Load())
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "test-k8s-token")
	got, err := ListAll[item](context.Background(), r, listPath)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if n := calls.Load(); n != int32(MaxListPages) {
		t.Fatalf("server hit %d times; want exactly MaxListPages=%d (cap terminates the walk)", n, MaxListPages)
	}
	if len(got) != MaxListPages {
		t.Fatalf("len = %d; want MaxListPages=%d (one item per capped page)", len(got), MaxListPages)
	}
}

// TestListAll_ContextCancelStops proves the run timeout (the caller's context
// deadline) caps the walk independently of the page cap: a cancelled context
// stops the walk with an error.
func TestListAll_ContextCancelStops(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"metadata":{"continue":"more"},"items":[{"name":"x"}]}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the first request
	r := NewReader(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := ListAll[item](ctx, r, listPath); err == nil {
		t.Fatal("ListAll: want error from cancelled context, got nil")
	}
}

// TestListAll_HTTPError surfaces a non-200 as a typed APIError and discards any
// partial accumulation.
func TestListAll_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "test-k8s-token")
	got, err := ListAll[item](context.Background(), r, listPath)
	if err == nil {
		t.Fatal("ListAll: want error on HTTP 403, got nil")
	}
	if got != nil {
		t.Errorf("got %+v; want nil items on error", got)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err type = %T; want *APIError", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Errorf("status = %d; want 403", apiErr.Status)
	}
}

// TestListAll_ErrorOnSecondPageDiscardsPartial proves a list that fails on a
// later page is reported as an error (not a silently-truncated partial list).
func TestListAll_ErrorOnSecondPageDiscardsPartial(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("continue") == "" {
			_, _ = w.Write([]byte(`{"metadata":{"continue":"PAGE-2"},"items":[{"name":"alpha"}]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "test-k8s-token")
	got, err := ListAll[item](context.Background(), r, listPath)
	if err == nil {
		t.Fatal("ListAll: want error when page 2 fails, got nil")
	}
	if got != nil {
		t.Errorf("got %+v; want nil items (partial list discarded)", got)
	}
}

// TestReader_SendsBearerToken pins that the bearer token reaches the
// Authorization header and is never widened beyond a GET.
func TestReader_SendsBearerToken(t *testing.T) {
	t.Parallel()
	var gotAuth, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"metadata":{"continue":""},"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "test-k8s-token")
	if _, err := ListAll[item](context.Background(), r, listPath); err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if gotAuth != "Bearer test-k8s-token" {
		t.Errorf("Authorization = %q; want Bearer test-k8s-token", gotAuth)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q; want GET (read-only)", gotMethod)
	}
}

// TestListAll_DecodeError surfaces malformed JSON as an error.
func TestListAll_DecodeError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	t.Cleanup(srv.Close)

	r := NewReader(srv.Client(), srv.URL, "")
	if _, err := ListAll[item](context.Background(), r, listPath); err == nil {
		t.Fatal("ListAll: want decode error, got nil")
	}
}

// TestAPIError_Error covers both error-string branches.
func TestAPIError_Error(t *testing.T) {
	t.Parallel()
	if got := (&APIError{Status: 401}).Error(); got != "k8s: HTTP 401" {
		t.Errorf("no-body error = %q", got)
	}
	if got := (&APIError{Status: 500, Body: "boom"}).Error(); got != "k8s: HTTP 500: boom" {
		t.Errorf("with-body error = %q", got)
	}
}

// TestNewReader_NilClientGetsDefault proves the nil-client fallback path.
func TestNewReader_NilClientGetsDefault(t *testing.T) {
	t.Parallel()
	r := NewReader(nil, "https://kube-api.test:6443/", "tok")
	if r.HTTP == nil {
		t.Fatal("nil http client was not defaulted")
	}
	if r.BaseURL != "https://kube-api.test:6443" {
		t.Errorf("BaseURL = %q; want trailing slash trimmed", r.BaseURL)
	}
}
