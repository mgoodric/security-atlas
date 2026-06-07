package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a thin read-only HTTP client for the Microsoft Graph Intune
// device-management API. It performs the OAuth2 client_credentials token
// exchange against the identity platform and then issues only GET requests
// against /deviceManagement/managedDevices with an explicit $select of
// posture-relevant properties. The client secret + bearer token are never
// logged. It deliberately does NOT depend on the Graph SDK — the connector
// mirrors the slice-486 (azure) thin-HTTP pattern to keep the dependency tree
// small.
//
// The $select asks ONLY for posture-relevant properties (id, deviceName,
// osVersion, operatingSystem, isEncrypted, passcodeStatePassed?,
// complianceState, managementAgent, userPrincipalName, userDisplayName). The
// detectedApps inventory, GPS location, and owner contact detail are never
// requested and never decoded, so they cannot leak into an evidence record
// (P0-490-3).
type Client struct {
	HTTP         *http.Client
	TokenURL     string
	GraphBaseURL string
	scope        string
	clientID     string
	clientSecret string

	token   string
	tokenAt time.Time
}

// ClientConfig configures NewClient. All fields are required except HTTP.
type ClientConfig struct {
	HTTP         *http.Client
	TokenURL     string
	GraphBaseURL string
	Scope        string
	ClientID     string
	ClientSecret string
}

// NewClient builds an Intune managed-devices client.
func NewClient(cfg ClientConfig) *Client {
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		HTTP:         hc,
		TokenURL:     cfg.TokenURL,
		GraphBaseURL: strings.TrimRight(cfg.GraphBaseURL, "/"),
		scope:        cfg.Scope,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
	}
}

const pageLimit = 200

// selectProps is the EXACT set of managed-device properties the connector
// requests. detectedApps, GPS location, phoneNumber, and emailAddress are
// deliberately excluded (P0-490-3 / threat-model I).
var selectProps = []string{
	"id", "deviceName", "osVersion", "operatingSystem",
	"isEncrypted", "complianceState", "managementAgent",
	"userPrincipalName", "userDisplayName",
}

// apiDevicePage is the minimal Graph managed-devices JSON shape — posture
// fields only. Properties not in selectProps are absent: json.Decode discards
// JSON keys with no matching struct field, so they never enter memory as
// connector data.
type apiDevicePage struct {
	Value []struct {
		ID                string `json:"id"`
		DeviceName        string `json:"deviceName"`
		OSVersion         string `json:"osVersion"`
		OperatingSystem   string `json:"operatingSystem"`
		IsEncrypted       bool   `json:"isEncrypted"`
		ComplianceState   string `json:"complianceState"`
		ManagementAgent   string `json:"managementAgent"`
		UserPrincipalName string `json:"userPrincipalName"`
		UserDisplayName   string `json:"userDisplayName"`
	} `json:"value"`
}

// ListManagedDevices reads the first bounded page of managed devices. Read-only:
// a single GET against /deviceManagement/managedDevices.
func (c *Client) ListManagedDevices(ctx context.Context) ([]RawDevice, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("$select", strings.Join(selectProps, ","))
	q.Set("$top", strconv.Itoa(pageLimit))
	var page apiDevicePage
	if err := c.getJSON(ctx, "/deviceManagement/managedDevices?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	out := make([]RawDevice, 0, len(page.Value))
	for _, d := range page.Value {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			continue
		}
		out = append(out, RawDevice{
			ID:        id,
			Name:      d.DeviceName,
			OSVersion: d.OSVersion,
			OS:        d.OperatingSystem,
			Encrypted: d.IsEncrypted,
			// Intune folds passcode/screen-lock into the overall complianceState;
			// a compliant device satisfies the configured passcode policy.
			PasscodeCompliant: strings.EqualFold(d.ComplianceState, "compliant"),
			ComplianceState:   d.ComplianceState,
			ManagementState:   d.ManagementAgent,
			Enrolled:          true, // present in managedDevices => enrolled
			OwnerAssignmentID: strings.TrimSpace(d.UserPrincipalName),
			OwnerDisplayName:  strings.TrimSpace(d.UserDisplayName),
		})
	}
	return out, nil
}

// tokenResponse is the client_credentials grant response from the identity
// platform.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func (c *Client) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Since(c.tokenAt) < 4*time.Minute {
		return nil
	}
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("grant_type", "client_credentials")
	form.Set("scope", c.scope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var tr tokenResponse
	if err := json.NewDecoder(res.Body).Decode(&tr); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("intune: empty access token in grant response")
	}
	c.token = tr.AccessToken
	c.tokenAt = time.Now()
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.GraphBaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// APIError carries Graph REST error context. The body is bounded; Graph error
// bodies do not echo the request credentials.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return "intune: HTTP " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("intune: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
