package webhookrecv_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

func TestNewServer_HasGosecTimeouts(t *testing.T) {
	t.Parallel()
	srv := webhookrecv.NewServer(":0", "/hook", http.NotFoundHandler())
	if srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout unset (gosec G112)")
	}
	if srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Error("server timeouts unset")
	}
}

func TestServe_GracefulShutdownOnCancel(t *testing.T) {
	t.Parallel()
	srv := webhookrecv.NewServer("127.0.0.1:0", "/hook", http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- webhookrecv.Serve(ctx, srv) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v; want nil on graceful shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}

func TestServe_ListenErrorReturned(t *testing.T) {
	t.Parallel()
	srv := webhookrecv.NewServer("256.256.256.256:99999", "/hook", http.NotFoundHandler())
	if err := webhookrecv.Serve(context.Background(), srv); err == nil {
		t.Fatal("Serve on bad address returned nil; want bind error")
	}
}
