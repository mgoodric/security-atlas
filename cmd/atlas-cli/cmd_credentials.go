package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
)

func newCredentialsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "credentials",
		Short: "API-key lifecycle (issue / rotate / revoke / list)",
	}
	cmd.AddCommand(newCredentialsIssueCmd())
	cmd.AddCommand(newCredentialsRotateCmd())
	cmd.AddCommand(newCredentialsRevokeCmd())
	cmd.AddCommand(newCredentialsListCmd())
	return cmd
}

func newCredentialsIssueCmd() *cobra.Command {
	var f struct {
		tenant string
		scope  string
		kinds  string
		ttl    string
	}
	cmd := &cobra.Command{
		Use:     "issue",
		Short:   "issue a new API key (bearer returned once)",
		PreRunE: requireOneFlag(&f.tenant, "--tenant"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ttl, err := parseDuration(f.ttl)
			if err != nil {
				return err
			}
			return runAdminRPC(func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error) {
				return c.Issue(ctx, &adminv1.IssueRequest{
					TenantId:       f.tenant,
					ScopePredicate: f.scope,
					Kinds:          splitKinds(f.kinds),
					Ttl:            durationpb.New(ttl),
				})
			})
		},
	}
	cmd.Flags().StringVar(&f.tenant, "tenant", "", "tenant id [required]")
	cmd.Flags().StringVar(&f.scope, "scope", "", "scope predicate (JSON string)")
	cmd.Flags().StringVar(&f.kinds, "kinds", "", "comma-separated evidence_kind identifiers (empty = all)")
	cmd.Flags().StringVar(&f.ttl, "ttl", "0", "time-to-live (e.g., 24h, 30d; 0 = no expiry)")
	return cmd
}

func newCredentialsRotateCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:     "rotate",
		Short:   "rotate a credential (successor issued; predecessor valid until grace)",
		PreRunE: requireOneFlag(&id, "--id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminRPC(func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error) {
				return c.Rotate(ctx, &adminv1.RotateRequest{Id: id})
			})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "credential id [required]")
	return cmd
}

func newCredentialsRevokeCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:     "revoke",
		Short:   "revoke a credential immediately",
		PreRunE: requireOneFlag(&id, "--id"),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := runAdminRPC(func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error) {
				_, err := c.Revoke(ctx, &adminv1.RevokeRequest{Id: id})
				return nil, err
			})
			if err == nil {
				fmt.Printf("revoked %s\n", id)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "credential id [required]")
	return cmd
}

func newCredentialsListCmd() *cobra.Command {
	var tenant string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list active credentials for a tenant (metadata only)",
		PreRunE: requireOneFlag(&tenant, "--tenant"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminRPC(func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error) {
				return c.List(ctx, &adminv1.ListRequest{TenantId: tenant})
			})
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant id [required]")
	return cmd
}

// requireOneFlag returns a PreRunE that asserts *val != "" and resolves
// the shared --endpoint / --token / --insecure flags.
func requireOneFlag(val *string, name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if *val == "" {
			return fmt.Errorf("%s is required", name)
		}
		return resolveCommon()
	}
}

func splitKinds(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseDuration(s string) (time.Duration, error) {
	if s == "" || s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--ttl %q: %w", s, err)
	}
	return d, nil
}
