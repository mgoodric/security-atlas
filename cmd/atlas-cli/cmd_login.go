package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// LoginCredentialsPath is the on-disk location the device-code flow
// stores the acquired JWT. The directory is created on demand;
// the file is chmod 0600 (P0-191-5 — world-readable credentials
// are unacceptable).
const (
	loginCredentialsDir  = ".config/atlas"
	loginCredentialsFile = "credentials.json"
)

// loginCredentials is the on-disk shape. Stable across CLI versions
// — adding fields requires backward-compat in the reader.
type loginCredentials struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   string `json:"expires_at"` // ISO 8601
	Issuer      string `json:"issuer"`
}

// newLoginCmd wires `atlas login`. Implements the RFC 8628 Device
// Authorization Grant client side:
//
//  1. POST /oauth/device_authorization with the configured
//     client_id to acquire a device_code + user_code +
//     verification_uri + interval.
//  2. Print user-facing instructions ("Visit <verification_uri>
//     and enter <user_code>"). The user authenticates via the
//     slice-034 OIDC RP, lands on the slice-191 approval UI,
//     and clicks Approve.
//  3. Poll POST /oauth/token with
//     grant_type=urn:ietf:params:oauth:grant-type:device_code at
//     the issuer-advertised interval (default 5s per RFC 8628
//     §3.5) until success or `expires_in` elapses.
//  4. Store the resulting JWT in ~/.config/atlas/credentials.json
//     with mode 0600 (P0-191-5).
//
// Honors the RFC 8628 §3.5 `slow_down` response by lengthening
// the poll interval.
func newLoginCmd() *cobra.Command {
	var (
		issuerURL string
		clientID  string
		timeout   time.Duration
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "authenticate via OAuth device-code (RFC 8628)",
		Long: `Run the OAuth 2.0 Device Authorization Grant (RFC 8628) flow.

The CLI prints a URL and a short code; visit the URL in a browser, sign
in via the configured IdP, and approve the code. The CLI's polling loop
detects the approval and stores the resulting JWT in
~/.config/atlas/credentials.json (mode 0600 — never world-readable).

Subsequent CLI commands present that JWT as the bearer for /v1/*
requests. Token lifetime is 1 hour (slice 188 default); re-run
'atlas login' on expiry.

Flags:
  --issuer URL   atlas issuer URL (env: ATLAS_ISSUER)
  --client-id ID OAuth client_id (env: ATLAS_OAUTH_CLIENT_ID) — typically
                 the per-CLI public client registered by the operator
  --timeout DUR  maximum time to wait for approval (default 15m)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if issuerURL == "" {
				issuerURL = os.Getenv("ATLAS_ISSUER")
			}
			if issuerURL == "" {
				return errors.New("--issuer or ATLAS_ISSUER is required")
			}
			if clientID == "" {
				clientID = os.Getenv("ATLAS_OAUTH_CLIENT_ID")
			}
			if clientID == "" {
				return errors.New("--client-id or ATLAS_OAUTH_CLIENT_ID is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return runDeviceCodeLogin(ctx, cmd.OutOrStdout(), issuerURL, clientID)
		},
	}
	cmd.Flags().StringVar(&issuerURL, "issuer", "", "atlas issuer URL (env: ATLAS_ISSUER)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client_id (env: ATLAS_OAUTH_CLIENT_ID)")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "maximum time to wait for approval")
	return cmd
}

// deviceAuthorizationResponse mirrors the RFC 8628 §3.2 response.
type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// tokenResponse mirrors RFC 6749 §5.1 + the RFC 8628 §3.5
// `error` field when the poll has not yet succeeded.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
}

// runDeviceCodeLogin runs steps 1-4 of the RFC 8628 device flow.
//
// The function is split out for testability — tests inject a
// custom HTTP transport via the context (not implemented here;
// integration tests exercise the full end-to-end path).
func runDeviceCodeLogin(ctx context.Context, w io.Writer, issuerURL, clientID string) error {
	issuerURL = strings.TrimRight(issuerURL, "/")
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Step 1: initiate the device-authorization flow.
	auth, err := postDeviceAuthorization(ctx, httpClient, issuerURL, clientID)
	if err != nil {
		return fmt.Errorf("initiate device flow: %w", err)
	}

	// Step 2: tell the user where to go and what to type.
	if auth.VerificationURIComplete != "" {
		fmt.Fprintf(w, "Visit %s\n", auth.VerificationURIComplete)
		fmt.Fprintf(w, "  (or go to %s and enter code %s)\n",
			auth.VerificationURI, auth.UserCode)
	} else {
		fmt.Fprintf(w, "Visit %s and enter code %s\n",
			auth.VerificationURI, auth.UserCode)
	}
	fmt.Fprintf(w, "Waiting for approval (up to %d seconds)...\n", auth.ExpiresIn)

	// Step 3: poll until success or until expires_in elapses.
	interval := auth.Interval
	if interval <= 0 {
		interval = 5 // RFC 8628 §3.5 default
	}
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
		}

		tok, err := postDeviceCodeRedemption(ctx, httpClient, issuerURL, clientID, auth.DeviceCode)
		if err != nil {
			return fmt.Errorf("poll token endpoint: %w", err)
		}
		switch tok.Error {
		case "":
			// Step 4: success — persist credentials.
			return persistLoginCredentials(loginCredentials{
				AccessToken: tok.AccessToken,
				ExpiresAt:   time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339),
				Issuer:      issuerURL,
			})
		case "authorization_pending":
			// Keep polling at the current interval.
			continue
		case "slow_down":
			// RFC 8628 §3.5 — lengthen the interval. Honor the
			// recommendation by adding the original interval.
			interval += auth.Interval
			fmt.Fprintf(w, "Server requested slow_down; new interval = %ds\n", interval)
			continue
		case "expired_token":
			return errors.New("device code expired; re-run 'atlas login'")
		case "access_denied":
			return errors.New("device code denied by user")
		default:
			return fmt.Errorf("token endpoint returned error: %s", tok.Error)
		}
	}
	return errors.New("device code expired before approval")
}

// postDeviceAuthorization implements step 1.
func postDeviceAuthorization(ctx context.Context, c *http.Client, issuerURL, clientID string) (*deviceAuthorizationResponse, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		issuerURL+"/oauth/device_authorization",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out deviceAuthorizationResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// postDeviceCodeRedemption implements step 3's single poll.
// Returns the parsed token response — non-empty `Error` field
// indicates the polling should continue or fail.
func postDeviceCodeRedemption(ctx context.Context, c *http.Client, issuerURL, clientID, deviceCode string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("client_id", clientID)
	form.Set("device_code", deviceCode)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		issuerURL+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out tokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		// 429 slow_down can come with an error body; the JSON parse
		// failure here is genuinely fatal. RFC 6749 §5.2 mandates
		// JSON.
		return nil, fmt.Errorf("parse response (status %d): %w", resp.StatusCode, err)
	}
	return &out, nil
}

// persistLoginCredentials writes the JWT to
// ~/.config/atlas/credentials.json with mode 0600 (P0-191-5). The
// containing directory is created with mode 0700.
func persistLoginCredentials(c loginCredentials) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	dir := filepath.Join(home, loginCredentialsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create credentials dir: %w", err)
	}
	path := filepath.Join(dir, loginCredentialsFile)
	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	// O_TRUNC ensures rotation overwrites; 0600 is the
	// world-unreadable mode P0-191-5 mandates.
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	return nil
}
