package azureauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenResponse is the OAuth2 client-credentials token response from the Entra
// token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// AcquireToken obtains a bearer access token for the given resource scope using
// the credential's auth mode.
//
//   - client-credentials: POST to the Entra token endpoint with the app
//     secret.
//   - managed-identity: NOT wired in v0 (the runtime IMDS endpoint differs per
//     host); returns a descriptive error so the operator knows to use
//     client-credentials in v0. Documented as a follow-on.
//
// scope is the resource scope, e.g. "https://graph.microsoft.com/.default" for
// Graph or "https://management.azure.com/.default" for ARM.
//
// The returned token is a short-lived bearer string; it is never logged.
func (c Credential) AcquireToken(ctx context.Context, httpClient *http.Client, scope string) (string, error) {
	switch c.mode {
	case ModeManagedIdentity:
		return "", fmt.Errorf("azureauth: managed-identity token acquisition is not wired in v0 — use client-credentials (set %s/%s/%s)",
			EnvTenantID, EnvClientID, EnvClientSecret)
	case ModeClientCredentials:
		return c.acquireClientCredentials(ctx, httpClient, scope)
	default:
		return "", fmt.Errorf("azureauth: unknown auth mode %q", c.mode)
	}
}

// tokenEndpointBase is the Entra login host. Overridable in tests so the
// client-credentials flow can be exercised against an httptest server without a
// live Azure tenant.
var tokenEndpointBase = "https://login.microsoftonline.com"

func (c Credential) acquireClientCredentials(ctx context.Context, httpClient *http.Client, scope string) (string, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", tokenEndpointBase, url.PathEscape(c.tenantID))
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.secret)
	form.Set("scope", scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		// Never echo the request body (it carries the secret); only the
		// response status + a bounded response body.
		return "", fmt.Errorf("azureauth: token endpoint HTTP %d: %s", res.StatusCode, drain(res.Body))
	}
	var tr tokenResponse
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("azureauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("azureauth: token endpoint returned empty access_token")
	}
	return tr.AccessToken, nil
}

func drain(r io.Reader) string {
	const max = 1 << 12
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
