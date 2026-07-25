package pdhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetJSON_HappyPath(t *testing.T) {
	t.Parallel()
	var gotAuth, gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"value":"ok"}`))
	}))
	defer srv.Close()

	tr := New(srv.Client(), srv.URL+"/", "test-pagerduty-token")
	var out struct {
		Value string `json:"value"`
	}
	if err := tr.GetJSON(context.Background(), "/thing", &out); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if out.Value != "ok" {
		t.Errorf("value = %q", out.Value)
	}
	if gotAuth != "Token token=test-pagerduty-token" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotAccept, "pagerduty+json") {
		t.Errorf("accept = %q", gotAccept)
	}
	if gotPath != "/thing" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestGetJSON_HTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad token"}`))
	}))
	defer srv.Close()
	tr := New(srv.Client(), srv.URL, "test-pagerduty-token")
	var out map[string]any
	err := tr.GetJSON(context.Background(), "/x", &out)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("want 401 error; got %v", err)
	}
	if !strings.Contains(err.Error(), "bad token") {
		t.Errorf("error should carry bounded body; got %v", err)
	}
}

func TestGetJSON_DecodeError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	tr := New(srv.Client(), srv.URL, "test-pagerduty-token")
	var out map[string]any
	if err := tr.GetJSON(context.Background(), "/x", &out); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("want decode error; got %v", err)
	}
}

func TestAPIError_EmptyBody(t *testing.T) {
	t.Parallel()
	e := &APIError{Status: 503}
	if !strings.Contains(e.Error(), "503") {
		t.Errorf("error = %q", e.Error())
	}
}

func TestNew_DefaultsHTTPClient(t *testing.T) {
	t.Parallel()
	tr := New(nil, "https://api.pagerduty.com/", "test-pagerduty-token")
	if tr.HTTP == nil {
		t.Error("HTTP client should default")
	}
	if tr.BaseURL != "https://api.pagerduty.com" {
		t.Errorf("BaseURL trailing slash not trimmed: %q", tr.BaseURL)
	}
}
