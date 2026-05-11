// Package main is the security-atlas platform server entrypoint.
//
// Slice 003 boots the gRPC server with Evidence + Admin services backed
// by in-memory stores. A bootstrap credential is minted at startup and
// printed to stderr — the first AdminCredentials.Issue call uses it.
// Slice 013 swaps in DB-backed evidence storage; slice 034 swaps the
// credential store and removes the stderr bootstrap.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mgoodric/security-atlas/internal/api"
)

const defaultAddr = ":50051"

func main() {
	addr := os.Getenv("ATLAS_GRPC_ADDR")
	if addr == "" {
		addr = defaultAddr
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

	fmt.Fprintf(os.Stderr, "atlas: listening on %s\n", addr)
	if err := srv.Run(ctx, addr); err != nil {
		fmt.Fprintf(os.Stderr, "atlas: server: %v\n", err)
		os.Exit(1)
	}
}
