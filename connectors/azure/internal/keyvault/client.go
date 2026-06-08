package keyvault

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

// ARMScope is the OAuth2 resource scope for read-only Azure Resource Manager —
// the SAME scope the storage, AKS and NSG kinds use (no new Azure scope,
// P0-521-3).
const ARMScope = "https://management.azure.com/.default"

// armAPIVersion pins the Key-Vault Resource Provider API version the connector
// reads against.
const armAPIVersion = "2023-07-01"

// Client is a thin read-only HTTP client for the ARM vaults list endpoint. It
// holds a short-lived bearer token (never logged) and issues only GET requests
// against the management-plane list (config + access-policy) surface. It NEVER
// touches the Key-Vault DATA plane (vault.azure.net secret/key/certificate
// GET) (P0-521-2) and NEVER mutates a vault resource. v0 reads the first
// bounded page of vaults for one subscription.
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

// armAccessPolicy mirrors one entry of the ARM Vault accessPolicies array.
// Permission VERBS only (which operations the principal may perform) — NEVER a
// secret/key/certificate value.
type armAccessPolicy struct {
	ObjectID    string `json:"objectId"`
	Permissions struct {
		Keys         []string `json:"keys"`
		Secrets      []string `json:"secrets"`
		Certificates []string `json:"certificates"`
		Storage      []string `json:"storage"`
	} `json:"permissions"`
}

// armVault mirrors the ARM Vault resource (management-plane CONFIGURATION +
// access-policy METADATA only — no secret/key/certificate value).
type armVault struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		EnableRbacAuthorization *bool             `json:"enableRbacAuthorization"`
		EnablePurgeProtection   *bool             `json:"enablePurgeProtection"`
		EnableSoftDelete        *bool             `json:"enableSoftDelete"`
		PublicNetworkAccess     string            `json:"publicNetworkAccess"`
		AccessPolicies          []armAccessPolicy `json:"accessPolicies"`
		NetworkACLs             *struct {
			DefaultAction string `json:"defaultAction"`
		} `json:"networkAcls"`
	} `json:"properties"`
}

type armVaultPage struct {
	Value []armVault `json:"value"`
}

// ListVaults fetches the first page of Key Vaults in the subscription.
// Read-only (Vaults list, ARM Reader role). This is a GET against the
// management-plane list surface only — it never touches the data plane and
// never mutates a vault.
func (c *Client) ListVaults(ctx context.Context) ([]RawVault, error) {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.KeyVault/vaults?api-version=%s",
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
	var page armVaultPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode key vaults: %w", err)
	}
	out := make([]RawVault, 0, len(page.Value))
	for _, v := range page.Value {
		out = append(out, RawVault{
			ID:                  v.ID,
			Name:                v.Name,
			ResourceGroup:       resourceGroupFromID(v.ID),
			Location:            v.Location,
			RBACAuthorization:   derefBool(v.Properties.EnableRbacAuthorization),
			PurgeProtection:     derefBool(v.Properties.EnablePurgeProtection),
			SoftDeleteEnabled:   derefBool(v.Properties.EnableSoftDelete),
			PublicNetworkAccess: v.Properties.PublicNetworkAccess,
			NetworkACLDefault:   networkACLDefault(v),
			AccessEntries:       mapAccessPolicies(v.Properties.AccessPolicies),
		})
	}
	return out, nil
}

// mapAccessPolicies normalizes the legacy access-policy array into the
// connector's access-METADATA-only shape. Each entry carries the principal
// object id and the permission VERBS it was granted (keys/secrets/certificates/
// storage), namespaced as "<area>:<verb>". RBAC-mode vaults carry no access
// policies; their role assignments are a documented follow-on (the v0 read is
// the bounded vault-list page only).
func mapAccessPolicies(in []armAccessPolicy) []AccessEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]AccessEntry, 0, len(in))
	for _, p := range in {
		perms := make([]string, 0)
		perms = appendNamespaced(perms, "keys", p.Permissions.Keys)
		perms = appendNamespaced(perms, "secrets", p.Permissions.Secrets)
		perms = appendNamespaced(perms, "certificates", p.Permissions.Certificates)
		perms = appendNamespaced(perms, "storage", p.Permissions.Storage)
		out = append(out, AccessEntry{
			PrincipalID:   p.ObjectID,
			PrincipalType: "access_policy",
			Permissions:   perms,
		})
	}
	return out
}

// appendNamespaced appends "<area>:<verb>" for each verb. The verbs are
// permission NAMES (e.g. "get", "list") — never a secret value.
func appendNamespaced(dst []string, area string, verbs []string) []string {
	for _, v := range verbs {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		dst = append(dst, area+":"+strings.ToLower(v))
	}
	return dst
}

func networkACLDefault(v armVault) string {
	if v.Properties.NetworkACLs == nil {
		return ""
	}
	return v.Properties.NetworkACLs.DefaultAction
}

func derefBool(p *bool) bool { return p != nil && *p }

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
