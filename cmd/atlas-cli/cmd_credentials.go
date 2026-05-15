package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
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
		tenant         string
		scope          string
		kinds          string
		ttl            string
		resetBootstrap bool
		force          bool
		httpEndpoint   string
	}
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "issue a new API key (bearer returned once)",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if f.tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ttl, err := parseDuration(f.ttl)
			if err != nil {
				return err
			}
			// Slice 073 — when --reset-bootstrap is set, we (1) call
			// Issue normally to mint a fresh admin bearer, (2) parse
			// the response to extract the bearer plaintext, (3) POST
			// the bearer + --force to the admin reset-bootstrap endpoint
			// so atlas resets platform_status AND writes the new token
			// to the bootstrap-token file. The endpoint refuses without
			// --force when a real user has already signed in (AC-8 /
			// P0-A6 foot-gun gate).
			if !f.resetBootstrap {
				return runAdminRPC(func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error) {
					return c.Issue(ctx, &adminv1.IssueRequest{
						TenantId:       f.tenant,
						ScopePredicate: f.scope,
						Kinds:          splitKinds(f.kinds),
						Ttl:            durationpb.New(ttl),
					})
				})
			}
			return runResetBootstrap(f.tenant, f.scope, splitKinds(f.kinds), ttl, f.force, f.httpEndpoint)
		},
	}
	cmd.Flags().StringVar(&f.tenant, "tenant", "", "tenant id [required]")
	cmd.Flags().StringVar(&f.scope, "scope", "", "scope predicate (JSON string)")
	cmd.Flags().StringVar(&f.kinds, "kinds", "", "comma-separated evidence_kind identifiers (empty = all)")
	cmd.Flags().StringVar(&f.ttl, "ttl", "0", "time-to-live (e.g., 24h, 30d; 0 = no expiry)")
	// Slice 073 — recovery flags. --reset-bootstrap is the discoverable
	// flag; --force is the foot-gun gate. See docs/issues/073-*.md AC-8.
	cmd.Flags().BoolVar(&f.resetBootstrap, "reset-bootstrap", false,
		"re-issue the bootstrap admin token AND re-arm the platform_status marker (recovery)")
	cmd.Flags().BoolVar(&f.force, "force", false,
		"with --reset-bootstrap: allow the reset after first sign-in has occurred")
	cmd.Flags().StringVar(&f.httpEndpoint, "http-endpoint", "",
		"with --reset-bootstrap: atlas HTTP endpoint (e.g. http://localhost:8080); defaults to deriving from --endpoint")
	return cmd
}

// runResetBootstrap implements the slice-073 --reset-bootstrap flow:
//  1. gRPC AdminCredentialsService.Issue to mint a fresh admin bearer.
//  2. POST /v1/admin/install/reset-bootstrap with the new token + force
//     flag. atlas resets platform_status (refuses without --force when
//     first_signin_at is already set) AND writes the new token to the
//     bootstrap-token file.
//
// Returns the printed response from step 1 (the credential metadata +
// bearer plaintext, exactly as `credentials issue` would).
func runResetBootstrap(tenant, scope string, kinds []string, ttl time.Duration, force bool, httpEndpoint string) error {
	// Step 1 — issue.
	ctx, cancel := newAdminContext(10 * time.Second)
	defer cancel()
	client, conn, err := newAdminClient()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	resp, err := client.Issue(ctx, &adminv1.IssueRequest{
		TenantId:       tenant,
		ScopePredicate: scope,
		Kinds:          kinds,
		Ttl:            durationpb.New(ttl),
	})
	if err != nil {
		return fmt.Errorf("issue: %w", err)
	}
	bearer := resp.GetBearerToken()
	if bearer == "" {
		return fmt.Errorf("issue: response missing bearer plaintext")
	}

	// Step 2 — reset platform_status + write file.
	endpoint := httpEndpoint
	if endpoint == "" {
		endpoint = deriveHTTPEndpoint(common.endpoint)
	}
	if endpoint == "" {
		return fmt.Errorf("--http-endpoint or derivable from --endpoint is required for --reset-bootstrap")
	}
	body, err := json.Marshal(map[string]any{
		"token": bearer,
		"force": force,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimRight(endpoint, "/") + "/v1/admin/install/reset-bootstrap"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	// Slice 088: explicit per-call timeout via cmdhttp.Client.
	// Credential bootstrap-reset may invoke cosign for issuance
	// signing on the server side, so 30s is the upper bound.
	// Q2 2026 security audit (slice 085) flagged the package-level
	// default client because it has no timeout — a hung server would
	// stall the CLI indefinitely. See cmd/atlas-cli/cmdhttp/client.go.
	httpResp, err := cmdhttp.Client(30 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("reset-bootstrap POST: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 8192))
	if httpResp.StatusCode == http.StatusConflict {
		return fmt.Errorf("reset refused: a real user has already signed in; re-run with --force to override (status %d, body %s)", httpResp.StatusCode, string(respBody))
	}
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("reset-bootstrap failed: status %d, body %s", httpResp.StatusCode, string(respBody))
	}

	// Print the bearer like a normal `issue` would — same shape so
	// scripts that piped from `credentials issue` keep working.
	if err := printJSON(resp); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmdCredentialsStdout(), "reset-bootstrap: platform_status cleared and bootstrap-token file rewritten")
	return nil
}

// deriveHTTPEndpoint takes a gRPC endpoint like "localhost:50051" and
// returns the conventional HTTP endpoint on :8080. When the endpoint
// already contains a scheme (http:// or https://) the host:port is used
// as-is with the appropriate scheme. Best-effort heuristic; the operator
// can always pass --http-endpoint explicitly.
func deriveHTTPEndpoint(grpcEndpoint string) string {
	switch {
	case strings.HasPrefix(grpcEndpoint, "http://"), strings.HasPrefix(grpcEndpoint, "https://"):
		return grpcEndpoint
	case strings.Contains(grpcEndpoint, ":50051"):
		return "http://" + strings.Replace(grpcEndpoint, ":50051", ":8080", 1)
	}
	return ""
}

// cmdCredentialsStdout returns os.Stdout. Indirected for testability.
var cmdCredentialsStdout = func() io.Writer { return os.Stdout }

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
