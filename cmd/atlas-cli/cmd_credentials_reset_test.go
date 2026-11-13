package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #1892 (d): --ttl reaches the server as ttl_seconds; with no --ttl the
// field is absent and the server's 24h default applies.
func TestRunResetBootstrap_SendsTTL(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = nil
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"credential_id":"key_1"}`))
	}))
	defer srv.Close()
	prevStdout := cmdCredentialsStdout
	cmdCredentialsStdout = func() io.Writer { return io.Discard }
	defer func() { cmdCredentialsStdout = prevStdout }()

	if err := runResetBootstrap("t-1", true, srv.URL, 48*time.Hour); err != nil {
		t.Fatalf("runResetBootstrap: %v", err)
	}
	if gotBody["ttl_seconds"] != float64(172800) {
		t.Fatalf("ttl_seconds = %v, want 172800", gotBody["ttl_seconds"])
	}
	if err := runResetBootstrap("t-1", true, srv.URL, 0); err != nil {
		t.Fatalf("runResetBootstrap: %v", err)
	}
	if _, ok := gotBody["ttl_seconds"]; ok {
		t.Fatalf("ttl_seconds sent for --ttl 0: %v", gotBody)
	}
	if err := runResetBootstrap("t-1", true, srv.URL, time.Millisecond); err == nil {
		t.Fatalf("sub-second --ttl: want an error")
	}
}

// #1892 (d): --scope and --kinds do not apply to the recovery admin key
// and are refused instead of silently dropped. Nothing is sent.
func TestCredentialsIssue_ResetBootstrapRejectsScopeAndKinds(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()
	prevEndpoint, prevToken := common.endpoint, common.token
	common.endpoint, common.token = "localhost:50051", "operator-token"
	defer func() { common.endpoint, common.token = prevEndpoint, prevToken }()

	for _, flag := range []string{"--scope={}", "--kinds=sast.scan_result.v1"} {
		cmd := newCredentialsIssueCmd()
		cmd.SetArgs([]string{"--tenant=t-1", "--reset-bootstrap", "--http-endpoint=" + srv.URL, flag})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "do not apply to --reset-bootstrap") {
			t.Fatalf("%s: err = %v, want the refusal", flag, err)
		}
	}
	if called {
		t.Fatalf("a refused invocation reached the server")
	}
}
