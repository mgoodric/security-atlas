package api

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// TestServeGRPC_StuckStreamDoesNotHoldShutdown opens a health Watch stream,
// which stays open until the client or server ends it, then cancels the run
// context. GracefulStop alone would wait on that stream forever; serveGRPC
// must hard-stop once the deadline passes (#2805).
func TestServeGRPC_StuckStreamDoesNotHoldShutdown(t *testing.T) {
	srv := New(Config{RotationGrace: time.Hour})
	healthpb.RegisterHealthServer(srv.GRPC, health.NewServer())

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const timeout = 300 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- srv.serveGRPC(ctx, lis, timeout) }()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	stream, err := healthpb.NewHealthClient(conn).Watch(streamCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	// The first response proves the stream is open on the server.
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("recv: %v", err)
	}

	start := time.Now()
	cancel()

	const epsilon = 2 * time.Second
	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("serveGRPC returned %v, want nil", err)
		}
		// Returning well before the deadline would mean the open stream was
		// not waited on at all: the drain must be attempted first.
		if elapsed < timeout {
			t.Fatalf("serveGRPC returned after %v, before the %v drain deadline", elapsed, timeout)
		}
	case <-time.After(timeout + epsilon):
		t.Fatalf("serveGRPC did not return within %v of cancel; the open stream held shutdown", timeout+epsilon)
	}
}

// TestServeGRPC_IdleStopsWithoutWaiting checks that with nothing in flight
// the graceful path completes and serveGRPC does not sit out the deadline.
func TestServeGRPC_IdleStopsWithoutWaiting(t *testing.T) {
	srv := New(Config{RotationGrace: time.Hour})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.serveGRPC(ctx, lis, time.Minute) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveGRPC returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle serveGRPC did not return promptly after cancel")
	}
}
