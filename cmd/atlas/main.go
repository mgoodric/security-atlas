// Package main is the security-atlas platform server entrypoint.
//
// Hosts the gRPC server (Evidence + Admin + Connectors) and an HTTP server
// (anchors browser API). Both share the in-memory stores; both stop on
// SIGINT/SIGTERM via a common context.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mgoodric/security-atlas/internal/api"
)

const (
	defaultGRPCAddr = ":50051"
	defaultHTTPAddr = ":8080"
)

func main() {
	grpcAddr := os.Getenv("ATLAS_GRPC_ADDR")
	if grpcAddr == "" {
		grpcAddr = defaultGRPCAddr
	}
	httpAddr := os.Getenv("ATLAS_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
	}

	srv := api.New(api.Config{})

	if bootstrapTenant := os.Getenv("ATLAS_BOOTSTRAP_TENANT"); bootstrapTenant != "" {
		cred, bearer, err := srv.IssueBootstrapCredential(bootstrapTenant)
		if err != nil {
			fmt.Fprintf(os.Stderr, "atlas: bootstrap issue: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "atlas: bootstrap credential issued: id=%s tenant=%s bearer=%s\n",
			cred.ID, cred.TenantID, bearer)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Fprintf(os.Stderr, "atlas: gRPC listening on %s\n", grpcAddr)
		if err := srv.Run(ctx, grpcAddr); err != nil {
			errCh <- fmt.Errorf("grpc: %w", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		fmt.Fprintf(os.Stderr, "atlas: HTTP listening on %s\n", httpAddr)
		if err := srv.RunHTTP(ctx, httpAddr); err != nil {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		fmt.Fprintf(os.Stderr, "atlas: %v\n", err)
		os.Exit(1)
	}
}
