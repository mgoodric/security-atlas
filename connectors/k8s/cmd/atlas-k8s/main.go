// Package main is the security-atlas Kubernetes connector binary. One
// subcommand per operation:
//
//	register    — announce this connector instance to the platform
//	run         — read RBAC + workload security contexts, push evidence records (pull profile)
//	subscribe   — consume a Kubernetes watch and push as changes happen (subscribe profile)
//	permissions — print the least-privilege read-only ClusterRole this connector needs
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// A signal context so the long-lived `subscribe` watch loop shuts down
	// gracefully on SIGINT / SIGTERM (cancelling ctx ends the watch consumers).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
