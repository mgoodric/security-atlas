// Slice 059 — per-tenant feature flags CLI subcommand.
//
// Two actions:
//
//   atlas-cli features list           -- GET /v1/admin/features
//   atlas-cli features set <key> <on|off>
//                                     -- PATCH /v1/admin/features/{key}
//
// Both require an admin bearer token. The token is read from
// $ATLAS_ADMIN_TOKEN (preferred) or the persistent --token flag (which
// also sources from $SECURITY_ATLAS_TOKEN per the slice-014 convention).
//
// The CLI uses the HTTP API (not gRPC) because feature-flag toggles
// don't have a proto service shape -- they're admin REST endpoints with
// JSON bodies. The HTTP endpoint is derived from --endpoint by stripping
// the gRPC port and substituting the HTTP one when needed, but for v1
// we require the caller to supply --http-endpoint (or
// $ATLAS_HTTP_ENDPOINT) explicitly so there's no port-mapping
// surprises in production deployments.

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

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
)

func newFeaturesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "features",
		Short: "per-tenant feature flag toggles (admin only)",
		Long: `List and toggle per-tenant capability flags via the admin HTTP API.

Authentication: set $ATLAS_ADMIN_TOKEN to an admin bearer token issued via
'atlas-cli credentials issue --is-admin'.

Endpoint: set $ATLAS_HTTP_ENDPOINT or pass --http-endpoint (default
http://localhost:8080).`,
	}
	cmd.AddCommand(newFeaturesListCmd())
	cmd.AddCommand(newFeaturesSetCmd())
	return cmd
}

func newFeaturesListCmd() *cobra.Command {
	var httpEndpoint string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list all feature flags for the calling tenant",
		PreRunE: featuresPreRunE(&httpEndpoint),
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := featuresAdminToken()
			if err != nil {
				return err
			}
			body, status, err := featuresDoRequest(cmd.Context(), http.MethodGet, httpEndpoint+"/v1/admin/features", nil, token)
			if err != nil {
				return err
			}
			if status >= 400 {
				return fmt.Errorf("server returned %d: %s", status, string(body))
			}
			out := cmd.OutOrStdout()
			_, _ = out.Write(body)
			if !bytes.HasSuffix(body, []byte("\n")) {
				_, _ = io.WriteString(out, "\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&httpEndpoint, "http-endpoint", "", "platform HTTP endpoint (env: ATLAS_HTTP_ENDPOINT, default: http://localhost:8080)")
	return cmd
}

func newFeaturesSetCmd() *cobra.Command {
	var (
		httpEndpoint string
		reason       string
	)
	cmd := &cobra.Command{
		Use:     "set <flag_key> <on|off>",
		Short:   "enable or disable a feature flag for the calling tenant",
		Args:    cobra.ExactArgs(2),
		PreRunE: featuresPreRunE(&httpEndpoint),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			state := strings.ToLower(args[1])
			var enabled bool
			switch state {
			case "on", "true", "enable", "enabled":
				enabled = true
			case "off", "false", "disable", "disabled":
				enabled = false
			default:
				return fmt.Errorf("invalid state %q -- expected on|off|true|false", args[1])
			}
			token, err := featuresAdminToken()
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]any{"enabled": enabled, "reason": reason})
			respBody, status, err := featuresDoRequest(cmd.Context(), http.MethodPatch, httpEndpoint+"/v1/admin/features/"+key, body, token)
			if err != nil {
				return err
			}
			if status >= 400 {
				return fmt.Errorf("server returned %d: %s", status, string(respBody))
			}
			out := cmd.OutOrStdout()
			_, _ = out.Write(respBody)
			if !bytes.HasSuffix(respBody, []byte("\n")) {
				_, _ = io.WriteString(out, "\n")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&httpEndpoint, "http-endpoint", "", "platform HTTP endpoint (env: ATLAS_HTTP_ENDPOINT, default: http://localhost:8080)")
	cmd.Flags().StringVar(&reason, "reason", "", "human-readable reason recorded in the audit log")
	return cmd
}

// featuresPreRunE resolves the --http-endpoint flag from the environment
// and applies the default when unset. Returns a PreRunE function so the
// flag value is set before RunE.
func featuresPreRunE(endpoint *string) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		if *endpoint == "" {
			*endpoint = os.Getenv("ATLAS_HTTP_ENDPOINT")
		}
		if *endpoint == "" {
			*endpoint = "http://localhost:8080"
		}
		*endpoint = strings.TrimRight(*endpoint, "/")
		return nil
	}
}

// featuresAdminToken resolves the admin bearer token from
// $ATLAS_ADMIN_TOKEN. Falls back to the shared --token / $SECURITY_ATLAS_TOKEN
// path so a single env var can power both the gRPC and HTTP CLI surfaces.
func featuresAdminToken() (string, error) {
	if tok := os.Getenv("ATLAS_ADMIN_TOKEN"); tok != "" {
		return tok, nil
	}
	if common.token != "" {
		return common.token, nil
	}
	if tok := os.Getenv("SECURITY_ATLAS_TOKEN"); tok != "" {
		return tok, nil
	}
	return "", fmt.Errorf("admin bearer token required ($ATLAS_ADMIN_TOKEN or --token)")
}

// featuresDoRequest performs a single HTTP request with the bearer
// authorization header. Returns the response body, status code, and an
// error for network-level failures.
func featuresDoRequest(ctx context.Context, method, url string, body []byte, token string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Slice 088: explicit per-call timeout via cmdhttp.Client.
	// Feature-flag list/toggle are small admin reads; 10s is generous.
	// Q2 2026 security audit (slice 085) flagged the package-level
	// default client because it has no timeout — a hung server would
	// stall the CLI indefinitely. See cmd/atlas-cli/cmdhttp/client.go.
	resp, err := cmdhttp.Client(10 * time.Second).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}
	return out, resp.StatusCode, nil
}
