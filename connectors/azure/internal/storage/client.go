package storage

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

// ARMScope is the OAuth2 resource scope for read-only Azure Resource Manager.
const ARMScope = "https://management.azure.com/.default"

// armAPIVersion pins the Storage Resource Provider API version the connector
// reads against.
const armAPIVersion = "2023-01-01"

// Client is a thin read-only HTTP client for the ARM Storage endpoints the
// connector reads. It holds a short-lived bearer token (never logged) and issues
// only GET requests. v0 reads the first bounded page of storage accounts for one
// subscription.
type Client struct {
	HTTP           *http.Client
	BaseURL        string // default https://management.azure.com
	SubscriptionID string
	token          string
}

// NewClient builds an ARM client. token is a bearer access token (from
// azureauth.Credential.AcquireToken). baseURL empty defaults to the public ARM
// endpoint.
func NewClient(httpClient *http.Client, baseURL, subscriptionID, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://management.azure.com"
	}
	return &Client{
		HTTP:           httpClient,
		BaseURL:        strings.TrimRight(baseURL, "/"),
		SubscriptionID: subscriptionID,
		token:          token,
	}
}

// armStorageAccount mirrors the ARM StorageAccount resource (read-only config
// fields only — no keys, no SAS, no blob contents).
type armStorageAccount struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		Encryption struct {
			KeySource string `json:"keySource"`
		} `json:"encryption"`
		SupportsHTTPSTrafficOnly bool   `json:"supportsHttpsTrafficOnly"`
		MinimumTLSVersion        string `json:"minimumTlsVersion"`
		AllowBlobPublicAccess    bool   `json:"allowBlobPublicAccess"`
	} `json:"properties"`
}

type armStorageAccountPage struct {
	Value []armStorageAccount `json:"value"`
}

// ListStorageAccounts fetches the first page of storage accounts in the
// subscription. Read-only (Storage Accounts list, ARM Reader role).
func (c *Client) ListStorageAccounts(ctx context.Context) ([]RawAccount, error) {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.Storage/storageAccounts?api-version=%s",
		c.BaseURL, url.PathEscape(c.SubscriptionID), armAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var page armStorageAccountPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode storage accounts: %w", err)
	}
	out := make([]RawAccount, 0, len(page.Value))
	for _, a := range page.Value {
		ks := a.Properties.Encryption.KeySource
		out = append(out, RawAccount{
			ID:                    a.ID,
			Name:                  a.Name,
			ResourceGroup:         resourceGroupFromID(a.ID),
			Location:              a.Location,
			EncryptionEnabled:     ks != "", // ARM always reports a key source when encryption is on
			EncryptionKeySource:   ks,
			HTTPSTrafficOnly:      a.Properties.SupportsHTTPSTrafficOnly,
			MinimumTLSVersion:     a.Properties.MinimumTLSVersion,
			AllowBlobPublicAccess: a.Properties.AllowBlobPublicAccess,
		})
	}
	return out, nil
}

// resourceGroupFromID extracts the resource-group segment from an ARM resource
// id (.../resourceGroups/<rg>/providers/...). Returns "" when absent.
func resourceGroupFromID(id string) string {
	parts := strings.Split(id, "/")
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "resourceGroups") {
			return parts[i+1]
		}
	}
	return ""
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
}

// APIError carries ARM REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("arm: HTTP %d", e.Status)
	}
	return fmt.Sprintf("arm: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
