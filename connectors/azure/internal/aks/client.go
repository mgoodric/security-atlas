package aks

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
// the SAME scope the storage kind uses (no new Azure scope, P0-519-2).
const ARMScope = "https://management.azure.com/.default"

// armAPIVersion pins the Container Service (AKS) Resource Provider API version
// the connector reads against.
const armAPIVersion = "2024-02-01"

// Client is a thin read-only HTTP client for the ARM managed-clusters list
// endpoint. It holds a short-lived bearer token (never logged) and issues only
// GET requests against the list (config) surface. It NEVER POSTs to
// listClusterAdminCredential / listClusterUserCredential (those return
// kubeconfig credentials — P0-519-1). v0 reads the first bounded page of
// managed clusters for one subscription.
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

// armManagedCluster mirrors the ARM ManagedCluster resource (read-only config
// fields only — no admin kubeconfig, no service-principal secret, no node
// credentials, no workload manifests).
type armManagedCluster struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Identity *struct {
		Type string `json:"type"` // "SystemAssigned" | "UserAssigned" | "None"
	} `json:"identity"`
	Properties struct {
		KubernetesVersion    string `json:"kubernetesVersion"`
		EnableRBAC           bool   `json:"enableRBAC"`
		DisableLocalAccounts bool   `json:"disableLocalAccounts"`
		NetworkProfile       struct {
			NetworkPolicy string `json:"networkPolicy"`
		} `json:"networkProfile"`
		APIServerAccessProfile *struct {
			EnablePrivateCluster *bool    `json:"enablePrivateCluster"`
			AuthorizedIPRanges   []string `json:"authorizedIPRanges"`
		} `json:"apiServerAccessProfile"`
		OIDCIssuerProfile *struct {
			Enabled bool `json:"enabled"`
		} `json:"oidcIssuerProfile"`
		AgentPoolProfiles []struct {
			Name string `json:"name"`
		} `json:"agentPoolProfiles"`
	} `json:"properties"`
}

type armManagedClusterPage struct {
	Value []armManagedCluster `json:"value"`
}

// ListManagedClusters fetches the first page of AKS managed clusters in the
// subscription. Read-only (ManagedClusters list, ARM Reader role). This is a GET
// against the list surface only — it never calls listClusterAdminCredential.
func (c *Client) ListManagedClusters(ctx context.Context) ([]RawCluster, error) {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.ContainerService/managedClusters?api-version=%s",
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
	var page armManagedClusterPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode managed clusters: %w", err)
	}
	out := make([]RawCluster, 0, len(page.Value))
	for _, m := range page.Value {
		out = append(out, RawCluster{
			ID:                    m.ID,
			Name:                  m.Name,
			ResourceGroup:         resourceGroupFromID(m.ID),
			Location:              m.Location,
			KubernetesVersion:     m.Properties.KubernetesVersion,
			RBACEnabled:           m.Properties.EnableRBAC,
			NetworkPolicy:         m.Properties.NetworkProfile.NetworkPolicy,
			PrivateCluster:        privateCluster(m),
			AuthorizedIPRanges:    authorizedIPRanges(m),
			ManagedIdentity:       managedIdentity(m),
			LocalAccountsDisabled: m.Properties.DisableLocalAccounts,
			OIDCIssuerEnabled:     m.Properties.OIDCIssuerProfile != nil && m.Properties.OIDCIssuerProfile.Enabled,
			NodePoolCount:         len(m.Properties.AgentPoolProfiles),
		})
	}
	return out, nil
}

func privateCluster(m armManagedCluster) bool {
	p := m.Properties.APIServerAccessProfile
	return p != nil && p.EnablePrivateCluster != nil && *p.EnablePrivateCluster
}

func authorizedIPRanges(m armManagedCluster) bool {
	p := m.Properties.APIServerAccessProfile
	return p != nil && len(p.AuthorizedIPRanges) > 0
}

func managedIdentity(m armManagedCluster) bool {
	return m.Identity != nil && m.Identity.Type != "" && !strings.EqualFold(m.Identity.Type, "None")
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
