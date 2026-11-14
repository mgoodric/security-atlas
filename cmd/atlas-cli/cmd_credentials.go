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
			return resolveCommon()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ttl, err := parseDuration(f.ttl)
			if err != nil {
				return err
			}
			// Slice 073 — when --reset-bootstrap is set, we POST --force
			// to the admin reset-bootstrap endpoint, which resets
			// platform_status, mints a fresh ADMIN credential for
			// --tenant and writes it to the bootstrap-token file
			// (#1885). The endpoint refuses without --force when a real
			// user has already signed in (AC-8 / P0-A6 foot-gun gate).
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
			// The recovery key is a tenant admin key: --scope and --kinds
			// do not apply, and are refused rather than silently dropped
			// (#1892). --ttl sets its lifetime; the server defaults it to
			// 24h and caps it at 7d.
			if cmd.Flags().Changed("scope") || cmd.Flags().Changed("kinds") {
				return fmt.Errorf("--scope and --kinds do not apply to --reset-bootstrap (it mints a tenant admin key)")
			}
			return runResetBootstrap(f.tenant, f.force, f.httpEndpoint, ttl)
		},
	}
	cmd.Flags().StringVar(&f.tenant, "tenant", "", credentialTenantFlagHelp)
	cmd.Flags().StringVar(&f.scope, "scope", "", "scope predicate (JSON string)")
	cmd.Flags().StringVar(&f.kinds, "kinds", "", "comma-separated evidence_kind identifiers (empty = all)")
	cmd.Flags().StringVar(&f.ttl, "ttl", "0", "time-to-live (e.g., 24h, 30d; 0 = no expiry). With --reset-bootstrap: 0 = the server default of 24h, at most 168h")
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

// runResetBootstrap implements the slice-073 --reset-bootstrap flow: one
// POST /v1/admin/install/reset-bootstrap carrying --force and --tenant,
// authorized by the operator's --token. atlas resets platform_status
// (refuses without --force when first_signin_at is already set), mints a
// fresh admin credential for the tenant and writes it to the
// bootstrap-token file (#1885). The CLI prints the returned credential
// (id, tenant, bearer plaintext) once.
//
// Before #1885 the CLI minted the token itself over the admin gRPC Issue
// RPC, which mints only non-admin keys, so signing in with the recovered
// token gave a non-admin session.
//
// ttl is the recovery key's lifetime; 0 leaves the server default (#1892).
func runResetBootstrap(tenant string, force bool, httpEndpoint string, ttl time.Duration) error {
	endpoint := httpEndpoint
	if endpoint == "" {
		endpoint = deriveHTTPEndpoint(common.endpoint)
	}
	if endpoint == "" {
		return fmt.Errorf("--http-endpoint or derivable from --endpoint is required for --reset-bootstrap")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	respBody, err := postResetBootstrap(ctx, endpoint, common.token, tenant, force, ttl)
	if err != nil {
		return err
	}
	// Print the server's response (credential_id, tenant_id, bearer_token,
	// status flags) as indented JSON.
	var out bytes.Buffer
	if err := json.Indent(&out, respBody, "", "  "); err != nil {
		return fmt.Errorf("decode reset-bootstrap response: %w", err)
	}
	_, _ = fmt.Fprintln(cmdCredentialsStdout(), out.String())
	_, _ = fmt.Fprintln(cmdCredentialsStdout(), "reset-bootstrap: platform_status cleared and bootstrap-token file rewritten with a new admin credential")
	_, _ = fmt.Fprintln(cmdCredentialsStdout(), "reset-bootstrap: the key expires at expires_at; after you sign in another way, revoke it with `atlas-cli credentials revoke --id <credential_id>`")
	return nil
}

// postResetBootstrap sends the reset POST to endpoint with operatorToken as
// the bearer and returns the 200 response body.
func postResetBootstrap(ctx context.Context, endpoint, operatorToken, tenant string, force bool, ttl time.Duration) ([]byte, error) {
	if ttl < 0 || (ttl > 0 && ttl < time.Second) {
		return nil, fmt.Errorf("--ttl %s: must be at least 1s with --reset-bootstrap", ttl)
	}
	payload := map[string]any{"force": force}
	if tenant != "" {
		// Omitted, the server mints for the caller's own tenant.
		payload["tenant_id"] = tenant
	}
	if ttl > 0 {
		payload["ttl_seconds"] = int64(ttl / time.Second)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimRight(endpoint, "/") + "/v1/admin/install/reset-bootstrap"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	// The reset is authorized by the operator's own credential: a
	// super_admin JWT, or an admin API key on a single-tenant install
	// (#1851, ADR 0003 amendment).
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	req.Header.Set("Content-Type", "application/json")
	// Slice 088: explicit per-call timeout via cmdhttp.Client.
	// Credential bootstrap-reset may invoke cosign for issuance
	// signing on the server side, so 30s is the upper bound.
	// Q2 2026 security audit (slice 085) flagged the package-level
	// default client because it has no timeout — a hung server would
	// stall the CLI indefinitely. See cmd/atlas-cli/cmdhttp/client.go.
	httpResp, err := cmdhttp.Client(30 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("reset-bootstrap POST: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 8192))
	if httpResp.StatusCode == http.StatusConflict {
		return nil, fmt.Errorf("reset refused: a real user has already signed in; re-run with --force to override (status %d, body %s)", httpResp.StatusCode, string(respBody))
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reset-bootstrap failed: status %d, body %s", httpResp.StatusCode, string(respBody))
	}
	return respBody, nil
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
		PreRunE: func(*cobra.Command, []string) error { return resolveCommon() },
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminRPC(func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error) {
				return c.List(ctx, &adminv1.ListRequest{TenantId: tenant})
			})
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", credentialTenantFlagHelp)
	return cmd
}

// credentialTenantFlagHelp is the --tenant help on the API-backed
// credentials commands: the server fills an omitted tenant from the
// caller's own credential (#1762).
const credentialTenantFlagHelp = "tenant id; optional on a single-tenant install (default: the tenant of the --token credential)"

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
