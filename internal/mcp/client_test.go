package mcp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// TestNewClient_RejectsBadInput exercises the constructor's input
// validation: empty bearer, non-http scheme, no host.
func TestNewClient_RejectsBadInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, base, bearer, wantSubstr string
	}{
		{"empty bearer", "http://localhost:8080", "", "bearer token is required"},
		{"bad scheme", "ftp://localhost", "test-token", "scheme must be http or https"},
		{"no host", "http:///", "test-token", "must include host"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := mcp.NewClient(tc.base, tc.bearer, "v0.0.0-test")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestClient_UserAgent verifies P0-A4 — every outbound request carries
// the canonical User-Agent template.
func TestClient_UserAgent(t *testing.T) {
	t.Parallel()

	var observed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.Header.Get("User-Agent")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c, err := mcp.NewClient(srv.URL, "test-bearer", "v1.2.3")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Get(context.Background(), "/v1/anything", url.Values{}, &struct{}{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := "atlas-mcp/v1.2.3 (mcp; ai_assisted=read-only)"
	if observed != want {
		t.Errorf("User-Agent = %q, want %q", observed, want)
	}
	if got := c.UserAgent(); got != want {
		t.Errorf("UserAgent() = %q, want %q", got, want)
	}
}

// TestClient_BearerHeader verifies the Authorization header carries
// the bearer prefix.
func TestClient_BearerHeader(t *testing.T) {
	t.Parallel()

	var observed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = r.Header.Get("Authorization")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c, _ := mcp.NewClient(srv.URL, "test-bearer-token", "v0.0.0-test")
	_ = c.Get(context.Background(), "/v1/x", url.Values{}, &struct{}{})
	if observed != "Bearer test-bearer-token" {
		t.Errorf("Authorization = %q, want %q", observed, "Bearer test-bearer-token")
	}
}

// TestClient_HTTPError_PreservesRetryAfter exercises P0-A8 — 429
// surfaces with Retry-After preserved.
func TestClient_HTTPError_PreservesRetryAfter(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	c, _ := mcp.NewClient(srv.URL, "test-bearer", "v0.0.0-test")
	err := c.Get(context.Background(), "/v1/x", url.Values{}, nil)
	var herr *mcp.HTTPError
	if !errors.As(err, &herr) {
		t.Fatalf("expected *mcp.HTTPError, got %T: %v", err, err)
	}
	if herr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", herr.StatusCode)
	}
	if herr.RetryAfter != 12 {
		t.Errorf("RetryAfter = %d, want 12", herr.RetryAfter)
	}
	if !strings.Contains(herr.Error(), "retry after 12s") {
		t.Errorf("error message missing retry-after: %s", herr.Error())
	}
}

// TestClient_RejectsAbsolutePath rejects callers passing a non-/-leading
// path (defense against an LLM-supplied arg flowing into an absolute
// URL).
func TestClient_RejectsAbsolutePath(t *testing.T) {
	t.Parallel()

	c, _ := mcp.NewClient("http://localhost:8080", "test-bearer", "v0.0.0-test")
	err := c.Get(context.Background(), "http://evil.example.com/", url.Values{}, nil)
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Errorf("expected path validation error, got: %v", err)
	}
}

// TestClient_BoundsResponseSize verifies the 1 MiB response cap.
func TestClient_BoundsResponseSize(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("a", (1<<20)+100) // 1 MiB + 100 bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"`)) // open quote
		_, _ = w.Write([]byte(huge))
		_, _ = w.Write([]byte(`"`)) // close quote (so it's valid JSON if it weren't truncated)
	}))
	defer srv.Close()

	c, _ := mcp.NewClient(srv.URL, "test-bearer", "v0.0.0-test")
	var sink string
	err := c.Get(context.Background(), "/v1/x", url.Values{}, &sink)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected size-cap error, got: %v", err)
	}
}
