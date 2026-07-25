package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

func TestNewServer_HasTimeouts(t *testing.T) {
	rec := testReceiver(t, &recordingPusher{}, &staticFetcher{ok: true}, oneID("w1"))
	srv := NewServer(":0", "/hooks/hris", rec)
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout unset (gosec G112)")
	}
	if srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Error("server timeouts unset")
	}
}

// TestServe_RunsAndShutsDown binds a real listener, posts a signed delivery
// end-to-end through the bounded server, and asserts the record is pushed; then
// cancels the context and asserts Serve returns cleanly.
func TestServe_RunsAndShutsDown(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{WorkerID: "w1", Status: worker.StatusTerminated}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	srv := NewServer(addr, "/hooks/hris", rec)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv) }()

	// Wait for the server to accept connections.
	waitForServer(t, addr)

	body := `{"worker_id":"w1"}`
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(body))
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	if len(pusher.pushed) != 1 {
		t.Errorf("pushed %d; want 1", len(pusher.pushed))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v; want nil on graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

// TestServe_ListenError exercises the immediate-error return path: binding to a
// malformed address fails ListenAndServe synchronously, and Serve returns that
// error (not nil) before the context is ever cancelled.
func TestServe_ListenError(t *testing.T) {
	rec := testReceiver(t, &recordingPusher{}, &staticFetcher{ok: true}, oneID("w1"))
	srv := NewServer("256.256.256.256:99999", "/hooks/hris", rec)
	err := Serve(context.Background(), srv)
	if err == nil {
		t.Fatal("Serve on bad address returned nil; want bind error")
	}
}

// TestReceiver_FetchedWorkerMissingIDEmitsNothing drives the Normalize-drops
// branch: a re-read that returns ok but with a blank worker id is normalized
// away, so nothing is pushed (200, no record).
func TestReceiver_FetchedWorkerMissingIDEmitsNothing(t *testing.T) {
	pusher := &recordingPusher{}
	fetcher := &staticFetcher{raw: worker.RawWorker{WorkerID: "   "}, ok: true}
	rec := testReceiver(t, pusher, fetcher, oneID("w1"))
	body := `{"worker_id":"w1"}`
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(body))
	req := httptest.NewRequest(http.MethodPost, "/hooks/hris", strings.NewReader(body))
	req.Header.Set(testHeader, hex.EncodeToString(mac.Sum(nil)))
	rr := httptest.NewRecorder()
	rec.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rr.Code)
	}
	if len(pusher.pushed) != 0 {
		t.Errorf("pushed %d; want 0 (worker normalized away)", len(pusher.pushed))
	}
}

func waitForServer(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never came up", addr)
}
