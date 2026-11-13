package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunResetBootstrap_SendsOperatorTokenAsBearer pins #1851/#1885: the
// reset POST is authorized by the operator's --token, and the body carries
// --tenant and --force (no caller-minted token). The response's credential
// is printed.
func TestRunResetBootstrap_SendsOperatorTokenAsBearer(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"status_reset":true,"file_written":true,"credential_id":"key_1","tenant_id":"t-1","bearer_token":"minted-admin"}`))
	}))
	defer srv.Close()

	prevToken := common.token
	common.token = "operator-token"
	defer func() { common.token = prevToken }()
	var out bytes.Buffer
	prevStdout := cmdCredentialsStdout
	cmdCredentialsStdout = func() io.Writer { return &out }
	defer func() { cmdCredentialsStdout = prevStdout }()

	if err := runResetBootstrap("t-1", true, srv.URL, 0); err != nil {
		t.Fatalf("runResetBootstrap: %v", err)
	}
	if gotAuth != "Bearer operator-token" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer operator-token")
	}
	if gotPath != "/v1/admin/install/reset-bootstrap" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["tenant_id"] != "t-1" || gotBody["force"] != true {
		t.Fatalf("body = %v, want tenant_id t-1 and force true", gotBody)
	}
	if _, ok := gotBody["token"]; ok {
		t.Fatalf("body carries a caller token %v; the server mints it (#1885)", gotBody["token"])
	}
	if !strings.Contains(out.String(), "minted-admin") {
		t.Fatalf("stdout does not print the minted bearer: %s", out.String())
	}
}

// A 409 from the foot-gun gate surfaces as the --force hint.
func TestRunResetBootstrap_ConflictSuggestsForce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()
	err := runResetBootstrap("t-1", false, srv.URL, 0)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want the --force hint", err)
	}
}

// TestDeriveHTTPEndpoint covers the small heuristic that converts the
// gRPC --endpoint into the conventional HTTP endpoint for the
// reset-bootstrap admin call.
func TestDeriveHTTPEndpoint(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// :50051 -> :8080 swap, http scheme prepended.
		{in: "atlas:50051", want: "http://atlas:8080"},
		{in: "localhost:50051", want: "http://localhost:8080"},
		// Existing http:// scheme is passed through.
		{in: "http://atlas:8080", want: "http://atlas:8080"},
		// Existing https:// scheme is passed through.
		{in: "https://atlas.example.com", want: "https://atlas.example.com"},
		// Unrecognised port → empty (caller must pass --http-endpoint).
		{in: "atlas:1234", want: ""},
	}
	for _, c := range cases {
		got := deriveHTTPEndpoint(c.in)
		if got != c.want {
			t.Errorf("deriveHTTPEndpoint(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
