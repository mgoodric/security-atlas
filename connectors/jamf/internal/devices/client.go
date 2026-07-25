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

// Client is a thin read-only HTTP client for the Jamf Pro computer-inventory
// API. It performs the OAuth client_credentials token exchange (POST
// /api/oauth/token) and then issues only GET requests against
// /api/v1/computers-inventory with a posture-relevant `section` filter. The
// client secret + bearer token are never logged. It deliberately does NOT depend
// on a Jamf Go SDK — the connector mirrors the slice-486/487/488 thin-HTTP
// pattern to keep the dependency tree small.
//
// The client requests and decodes ONLY the posture-relevant sections
// (GENERAL, OPERATING_SYSTEM, DISK_ENCRYPTION, SECURITY, USER_AND_LOCATION).
// The APPLICATIONS section, GPS location, and owner contact detail are never
// requested and never decoded, so they cannot leak into an evidence record
// (P0-490-3).
type Client struct {
	HTTP         *http.Client
	BaseURL      string
	clientID     string
	clientSecret string

	token   string
	tokenAt time.Time
}

// NewClient builds a Jamf computer-inventory client. clientID + clientSecret are
// the read-scoped Jamf API-client credentials (from jamfauth.Credential);
// baseURL is the instance URL.
func NewClient(httpClient *http.Client, baseURL, clientID, clientSecret string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		HTTP:         httpClient,
		BaseURL:      strings.TrimRight(baseURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

const pageLimit = 200

// postureSections is the EXACT set of inventory sections the connector requests.
// APPLICATIONS, ATTACHMENTS, FONTS, PLUGINS, and the GPS location are
// deliberately excluded (P0-490-3 / threat-model I).
var postureSections = []string{
	"GENERAL", "OPERATING_SYSTEM", "DISK_ENCRYPTION", "SECURITY", "USER_AND_LOCATION",
}

// apiInventoryPage is the minimal Jamf computers-inventory JSON shape —
// posture-relevant fields only. Every section not listed here (APPLICATIONS, GPS
// location, etc.) is absent: json.Decode discards JSON keys with no matching
// struct field, so they never enter memory as connector data.
type apiInventoryPage struct {
	Results []struct {
		ID      string `json:"id"`
		General struct {
			Name             string `json:"name"`
			Supervised       bool   `json:"supervised"`
			Managed          bool   `json:"managed"`
			RemoteManagement struct {
				Managed bool `json:"managed"`
			} `json:"remoteManagement"`
		} `json:"general"`
		OperatingSystem struct {
			Version string `json:"version"`
		} `json:"operatingSystem"`
		DiskEncryption struct {
			// FileVault2 status; "VALID"/"ENCRYPTED" indicates encryption is on.
			FileVault2State string `json:"individualRecoveryKeyValidityStatus"`
			BootEncrypted   string `json:"fileVault2Status"`
		} `json:"diskEncryption"`
		Security struct {
			// "ALWAYS"/"DELAYED" etc. — a non-"NOT_ENFORCED" value means a
			// screen-lock grace policy is enforced.
			ScreenLockEnforced string `json:"screenLockGracePeriodEnforced"`
			GatekeeperStatus   string `json:"gatekeeperStatus"`
		} `json:"security"`
		UserAndLocation struct {
			// The OPAQUE assigned-user id + display name only. The email / phone /
			// position / department / building fields are NOT decoded (P0-490-3).
			Username string `json:"username"`
			RealName string `json:"realname"`
		} `json:"userAndLocation"`
	} `json:"results"`
}

// ListComputers reads the first bounded page of computer inventory. Read-only:
// a single GET against /api/v1/computers-inventory.
func (c *Client) ListComputers(ctx context.Context) ([]RawComputer, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, s := range postureSections {
		q.Add("section", s)
	}
	q.Set("page", "0")
	q.Set("page-size", strconv.Itoa(pageLimit))
	var page apiInventoryPage
	if err := c.getJSON(ctx, "/api/v1/computers-inventory?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	out := make([]RawComputer, 0, len(page.Results))
	for _, r := range page.Results {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		out = append(out, RawComputer{
			ID:                id,
			Name:              r.General.Name,
			OSVersion:         r.OperatingSystem.Version,
			FileVaultEnabled:  isFileVaultOn(r.DiskEncryption.BootEncrypted, r.DiskEncryption.FileVault2State),
			PasscodeCompliant: r.Security.ScreenLockEnforced != "" && r.Security.ScreenLockEnforced != "NOT_ENFORCED",
			Managed:           r.General.Managed || r.General.RemoteManagement.Managed,
			Supervised:        r.General.Supervised,
			Enrolled:          true, // present in computers-inventory => enrolled
			Compliant:         isFileVaultOn(r.DiskEncryption.BootEncrypted, r.DiskEncryption.FileVault2State),
			HasCompliance:     r.DiskEncryption.BootEncrypted != "",
			OwnerAssignmentID: strings.TrimSpace(r.UserAndLocation.Username),
			OwnerDisplayName:  strings.TrimSpace(r.UserAndLocation.RealName),
		})
	}
	return out, nil
}

func isFileVaultOn(bootStatus, recoveryKeyStatus string) bool {
	b := strings.ToUpper(strings.TrimSpace(bootStatus))
	if b == "ENCRYPTED" || b == "BOOT_PARTITIONS_ENCRYPTED" || b == "ALL_PARTITIONS_ENCRYPTED" {
		return true
	}
	return strings.ToUpper(strings.TrimSpace(recoveryKeyStatus)) == "VALID"
}

// tokenResponse is the OAuth client_credentials grant response. The token is
// short-lived; the connector exchanges fresh credentials each run.
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/oauth/token", strings.NewReader(form.Encode()))
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
		return fmt.Errorf("jamf: empty access token in grant response")
	}
	c.token = tr.AccessToken
	c.tokenAt = time.Now()
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
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

// APIError carries Jamf REST error context. The body is bounded; Jamf error
// bodies do not echo the request credentials.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return "jamf: HTTP " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("jamf: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
