package entra

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GraphScope is the OAuth2 resource scope for read-only Microsoft Graph.
const GraphScope = "https://graph.microsoft.com/.default"

// Client is a thin read-only HTTP client for the Microsoft Graph endpoints the
// connector reads. It holds a short-lived bearer token (never logged) and issues
// only GET requests. v0 reads the first bounded page of role assignments.
type Client struct {
	HTTP    *http.Client
	BaseURL string // default https://graph.microsoft.com/v1.0
	token   string
}

// NewClient builds a Graph client. token is a bearer access token (from
// azureauth.Credential.AcquireToken). baseURL empty defaults to the public
// Graph endpoint.
func NewClient(httpClient *http.Client, baseURL, token string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	if baseURL == "" {
		baseURL = "https://graph.microsoft.com/v1.0"
	}
	return &Client{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), token: token}
}

// graphRoleAssignment mirrors the Graph unifiedRoleAssignment resource (read-
// only fields only). expand=principal,roleDefinition resolves display names.
type graphRoleAssignment struct {
	ID               string `json:"id"`
	PrincipalID      string `json:"principalId"`
	RoleDefinitionID string `json:"roleDefinitionId"`
	DirectoryScopeID string `json:"directoryScopeId"`
	Principal        struct {
		ODataType   string `json:"@odata.type"`
		DisplayName string `json:"displayName"`
	} `json:"principal"`
	RoleDefinition struct {
		DisplayName string `json:"displayName"`
	} `json:"roleDefinition"`
}

type graphRoleAssignmentPage struct {
	Value []graphRoleAssignment `json:"value"`
}

// ListRoleAssignments fetches the first page of directory role assignments,
// expanding principal + roleDefinition so display names + principal type are
// available without per-id follow-up reads. Read-only.
func (c *Client) ListRoleAssignments(ctx context.Context) ([]RawAssignment, error) {
	u := c.BaseURL + "/roleManagement/directory/roleAssignments?$expand=principal,roleDefinition&$top=200"
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
	var page graphRoleAssignmentPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode role assignments: %w", err)
	}
	out := make([]RawAssignment, 0, len(page.Value))
	for _, a := range page.Value {
		out = append(out, RawAssignment{
			ID:                   a.ID,
			PrincipalID:          a.PrincipalID,
			PrincipalType:        principalTypeFromODataType(a.Principal.ODataType),
			PrincipalDisplayName: a.Principal.DisplayName,
			RoleDefinitionID:     a.RoleDefinitionID,
			RoleDisplayName:      a.RoleDefinition.DisplayName,
			DirectoryScopeID:     a.DirectoryScopeID,
		})
	}
	return out, nil
}

// principalTypeFromODataType maps the Graph @odata.type to our principal-type
// enum. e.g. "#microsoft.graph.user" → "user".
func principalTypeFromODataType(odata string) string {
	switch {
	case strings.HasSuffix(odata, ".user"):
		return PrincipalUser
	case strings.HasSuffix(odata, ".servicePrincipal"):
		return PrincipalServicePrincipal
	case strings.HasSuffix(odata, ".group"):
		return PrincipalGroup
	default:
		return PrincipalUnknown
	}
}

func (c *Client) applyAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
}

// APIError carries Graph REST error context.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("graph: HTTP %d", e.Status)
	}
	return fmt.Sprintf("graph: HTTP %d: %s", e.Status, e.Body)
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
