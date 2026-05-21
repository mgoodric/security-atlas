package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/internal/control"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
	"github.com/mgoodric/security-atlas/pkg/sdk-go/oauth"
)

// controlsAuthState holds the slice-196 OAuth client_credentials
// authentication state for `atlas-cli controls upload`. When
// `--client-id` + `--client-secret` are both set, the upload routes
// through pkg/sdk-go/oauth.NewClient to acquire a JWT bearer;
// otherwise the legacy slice-037 `--token` path is used.
//
// Package-global so the cobra flag bindings can populate it. The
// resetControlsAuth helper in cmd_controls_test.go clears it between
// tests.
type controlsAuthState struct {
	clientID     string
	clientSecret string
	issuer       string
	useOAuth     bool // set by resolveControlsAuth when OAuth flags select the OAuth path
}

var controlsAuth controlsAuthState

// acquireControlsBearer returns the bearer string to present on the
// upload request. When controlsAuth.useOAuth is true (set by
// resolveControlsAuth when --client-id + --client-secret are paired),
// the bearer is a JWT acquired from /oauth/token via the slice 191
// pkg/sdk-go/oauth helper. Otherwise, the legacy --token / env value
// is returned verbatim.
//
// The acquire path establishes a short-lived oauth.Client per upload
// invocation — atlas-cli is single-shot so cache + refresh do not
// matter. The client carries the slice 188 60-second refresh leeway
// and 30-second HTTP timeout by default.
func acquireControlsBearer(ctx context.Context) (string, error) {
	if !controlsAuth.useOAuth {
		return common.token, nil
	}
	cli, err := oauth.NewClient(oauth.Config{
		ClientID:     controlsAuth.clientID,
		ClientSecret: controlsAuth.clientSecret,
		IssuerURL:    controlsAuth.issuer,
	})
	if err != nil {
		return "", fmt.Errorf("oauth client: %w", err)
	}
	tok, err := cli.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire oauth token: %w", err)
	}
	return tok, nil
}

// resolveControlsAuth picks the authentication strategy for
// `controls upload` based on the flags + env. Returns:
//
//   - nil error + controlsAuth.useOAuth=true   when --client-id +
//     --client-secret are both set (OAuth client_credentials path).
//   - nil error + controlsAuth.useOAuth=false  when --client-id +
//     --client-secret are both UNSET and --token / SECURITY_ATLAS_TOKEN
//     resolves successfully (legacy slice-037 path).
//   - non-nil error                            when --client-id is set
//     alone (or --client-secret alone), or when neither auth path
//     resolves.
//
// The OAuth issuer URL defaults to --endpoint when --issuer is not
// supplied — the bootstrap container always points both at the same
// in-network atlas URL.
func resolveControlsAuth() error {
	// AC-2 of slice 196: --client-id + --client-secret select the OAuth
	// path. Both must be set together — a partial pair is a
	// misconfiguration the CLI calls out explicitly rather than letting
	// the server return a confusing 401.
	if controlsAuth.clientID != "" || controlsAuth.clientSecret != "" {
		if controlsAuth.clientID == "" {
			return fmt.Errorf("--client-id is required when --client-secret is set")
		}
		if controlsAuth.clientSecret == "" {
			return fmt.Errorf("--client-secret is required when --client-id is set")
		}
		// --endpoint is still required for the upload URL itself even
		// in the OAuth path — the JWT is acquired from the issuer and
		// then presented to the same atlas /v1 upload endpoint.
		if common.endpoint == "" {
			common.endpoint = os.Getenv("SECURITY_ATLAS_ENDPOINT")
		}
		if common.endpoint == "" {
			return fmt.Errorf("--endpoint or SECURITY_ATLAS_ENDPOINT is required")
		}
		if controlsAuth.issuer == "" {
			controlsAuth.issuer = common.endpoint
		}
		controlsAuth.useOAuth = true
		return nil
	}
	// AC-4 transitional: --token still drives the legacy slice-037 path.
	// resolveCommon enforces --endpoint + --token presence.
	return resolveCommon()
}

// newControlsCmd registers `security-atlas-cli controls {validate,upload}`.
//
// `validate` is local-only: no network, no auth. It parses the bundle and
// runs the structural validator. AC-2.
//
// `upload` ships the bundle to POST /v1/controls:upload-bundle. AC-3.
func newControlsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controls",
		Short: "control-as-code bundle authoring (validate / upload)",
	}
	cmd.AddCommand(newControlsValidateCmd())
	cmd.AddCommand(newControlsUploadCmd())
	return cmd
}

func newControlsValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <path>",
		Short: "validate a control bundle locally (no network call)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			bundle, err := loadBundleFromPath(path)
			if err != nil {
				return err
			}
			fmt.Printf("OK bundle_id=%s title=%q implementation_type=%s manifest_hash=%s\n",
				bundle.Manifest.BundleID,
				bundle.Manifest.Title,
				bundle.Manifest.ImplementationType,
				bundle.ManifestHashHex,
			)
			if len(bundle.Manifest.EvidenceQueries) > 0 {
				fmt.Printf("    evidence_queries=%d\n", len(bundle.Manifest.EvidenceQueries))
			}
			if err := bundle.ValidateApplicabilityExpr(); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

func newControlsUploadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload <path>",
		Short: "upload a control bundle to the platform",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return resolveControlsAuth()
		},
		RunE: func(_ *cobra.Command, args []string) error {
			path := args[0]
			// Validate locally first so we never burn a network round trip
			// on a malformed bundle.
			if _, err := loadBundleFromPath(path); err != nil {
				return err
			}
			return uploadBundleHTTP(path)
		},
	}
	// Slice 196: OAuth client_credentials flags. When both --client-id +
	// --client-secret are set, the upload acquires a JWT via the slice
	// 191 oauth.NewClient (pkg/sdk-go/oauth) and presents that JWT to
	// /v1/controls:upload-bundle instead of the legacy slice-037
	// --token bearer. The bootstrap container uses these flags.
	cmd.Flags().StringVar(&controlsAuth.clientID, "client-id", "",
		"OAuth client_id (paired with --client-secret to select OAuth client_credentials)")
	cmd.Flags().StringVar(&controlsAuth.clientSecret, "client-secret", "",
		"OAuth client_secret (paired with --client-id to select OAuth client_credentials)")
	cmd.Flags().StringVar(&controlsAuth.issuer, "issuer", "",
		"OAuth issuer URL (defaults to --endpoint when unset)")
	return cmd
}

// loadBundleFromPath dispatches on path shape: directory -> ParseDirectory,
// tarball (.tar.gz / .tgz file) -> ParseTarball.
func loadBundleFromPath(path string) (*control.Bundle, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return control.ParseDirectory(path)
	}
	low := strings.ToLower(path)
	if strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz") {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		defer func() { _ = f.Close() }()
		return control.ParseTarball(f)
	}
	return nil, fmt.Errorf("bundle path must be a directory or *.tar.gz/*.tgz file: %s", path)
}

// uploadBundleHTTP sends the bundle to the platform. For tarballs we POST
// multipart with the file part; for directories we tar them in-process and
// POST the tarball. JSON inline mode is also available via the API but the
// CLI prefers tarballs so the manifest hash matches what the server stores.
func uploadBundleHTTP(path string) error {
	tarBytes, err := buildTarballForUpload(path)
	if err != nil {
		return err
	}

	endpoint := strings.TrimRight(common.endpoint, "/")
	url := endpoint + "/v1/controls:upload-bundle"
	body := &bytes.Buffer{}
	mp := multipart.NewWriter(body)
	part, err := mp.CreateFormFile("bundle.tar.gz", filepath.Base(path)+".tar.gz")
	if err != nil {
		return fmt.Errorf("multipart create: %w", err)
	}
	if _, err := part.Write(tarBytes); err != nil {
		return fmt.Errorf("multipart write: %w", err)
	}
	if err := mp.Close(); err != nil {
		return fmt.Errorf("multipart close: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())

	// Slice 196: when --client-id + --client-secret are set, acquire a
	// JWT bearer via pkg/sdk-go/oauth (the slice 191 cache-and-refresh
	// helper) and present that instead of the legacy --token bearer.
	bearer, err := acquireControlsBearer(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set(sdk.MetadataAuthorization, sdk.BearerPrefix+bearer)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rspBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("upload failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rspBody)))
	}

	// Pretty-print the JSON response.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, rspBody, "", "  "); err == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(rspBody))
	}
	return nil
}
