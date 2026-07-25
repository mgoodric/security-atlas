package nsg

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
// the SAME scope the storage and AKS kinds use (no new Azure scope, P0-520-1).
const ARMScope = "https://management.azure.com/.default"

// armAPIVersion pins the Network Resource Provider API version the connector
// reads against.
const armAPIVersion = "2024-01-01"

// Client is a thin read-only HTTP client for the ARM network-security-groups
// list endpoint. It holds a short-lived bearer token (never logged) and issues
// only GET requests against the list (rule-config) surface. It NEVER POSTs and
// NEVER mutates a network resource (P0-520-3); it NEVER reads flow logs or
// packet captures (P0-520-2). v0 reads the first bounded page of NSGs for one
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

// armSecurityRule mirrors the ARM SecurityRule resource (rule metadata only — no
// flow logs, no packet captures, no traffic contents).
type armSecurityRule struct {
	Name       string `json:"name"`
	Properties struct {
		Direction                string `json:"direction"`
		Access                   string `json:"access"`
		Protocol                 string `json:"protocol"`
		Priority                 int    `json:"priority"`
		SourceAddressPrefix      string `json:"sourceAddressPrefix"`
		DestinationAddressPrefix string `json:"destinationAddressPrefix"`
		SourcePortRange          string `json:"sourcePortRange"`
		DestinationPortRange     string `json:"destinationPortRange"`
	} `json:"properties"`
}

// armNSG mirrors the ARM NetworkSecurityGroup resource (security-rule config +
// association COUNTS only — no flow logs, no traffic contents).
type armNSG struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		SecurityRules        []armSecurityRule `json:"securityRules"`
		DefaultSecurityRules []armSecurityRule `json:"defaultSecurityRules"`
		Subnets              []struct {
			ID string `json:"id"`
		} `json:"subnets"`
		NetworkInterfaces []struct {
			ID string `json:"id"`
		} `json:"networkInterfaces"`
	} `json:"properties"`
}

type armNSGPage struct {
	Value []armNSG `json:"value"`
}

// ListNetworkSecurityGroups fetches the first page of NSGs in the subscription.
// Read-only (NetworkSecurityGroups list, ARM Reader role). This is a GET against
// the list surface only — it never mutates and never reads flow logs.
func (c *Client) ListNetworkSecurityGroups(ctx context.Context) ([]RawGroup, error) {
	u := fmt.Sprintf("%s/subscriptions/%s/providers/Microsoft.Network/networkSecurityGroups?api-version=%s",
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
	var page armNSGPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode network security groups: %w", err)
	}
	out := make([]RawGroup, 0, len(page.Value))
	for _, g := range page.Value {
		out = append(out, RawGroup{
			ID:                g.ID,
			Name:              g.Name,
			ResourceGroup:     resourceGroupFromID(g.ID),
			Location:          g.Location,
			Rules:             mapRules(g.Properties.SecurityRules),
			AssociatedSubnets: len(g.Properties.Subnets),
			AssociatedNICs:    len(g.Properties.NetworkInterfaces),
		})
	}
	return out, nil
}

// mapRules normalizes the ARM SecurityRule list into the connector's RULE-only
// shape. Only the operator-authored securityRules are emitted; the ARM
// defaultSecurityRules (platform boilerplate) are deliberately not reported —
// they are identical across every NSG and add no compliance signal.
func mapRules(in []armSecurityRule) []SecurityRule {
	if len(in) == 0 {
		return nil
	}
	out := make([]SecurityRule, 0, len(in))
	for _, r := range in {
		out = append(out, SecurityRule{
			Name:                     r.Name,
			Direction:                strings.ToLower(r.Properties.Direction),
			Access:                   strings.ToLower(r.Properties.Access),
			Protocol:                 strings.ToLower(r.Properties.Protocol),
			Priority:                 r.Properties.Priority,
			SourceAddressPrefix:      r.Properties.SourceAddressPrefix,
			DestinationAddressPrefix: r.Properties.DestinationAddressPrefix,
			SourcePortRange:          r.Properties.SourcePortRange,
			DestinationPortRange:     r.Properties.DestinationPortRange,
		})
	}
	return out
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
