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

// authzAPIVersion pins the Microsoft.Authorization Resource Provider API version
// the connector reads roleAssignments / roleDefinitions against (slice 615).
const authzAPIVersion = "2022-04-01"

// maxRoleAssignmentsPerVault bounds the per-vault roleAssignments collected
// across ALL cursor pages (DoS guard, threat-model D). Slice 623 added nextLink
// pagination, so the connector now follows the ARM nextLink cursor up to this
// many role assignments for one vault; a vault with more than this many
// assignments has its list truncated honestly at the cap.
const maxRoleAssignmentsPerVault = 200

// maxRoleAssignmentsPerRun caps the TOTAL role assignments enumerated across all
// RBAC vaults in one connector run (DoS guard, threat-model D). Once the run
// reaches the cap, further per-vault role-assignment reads are skipped (the
// vault still reports its config; its role-assignment list is simply truncated).
const maxRoleAssignmentsPerRun = 2000

// maxRoleDefinitionLookupsPerRun caps the per-run roleDefinitions name-resolution
// lookups (each unique role-definition guid is looked up at most once and cached;
// this is the cap on distinct guids resolved per run — also a DoS guard).
const maxRoleDefinitionLookupsPerRun = 100

// maxRoleAssignmentPages caps the per-vault roleAssignments nextLink walk
// (slice 623). Independent of the record caps above, it is the loop-termination
// backstop so a self-pointing or non-terminating roleAssignments nextLink chain
// cannot drive an unbounded read loop for a single vault — the walk stops after
// this many pages even if the record caps are never reached (e.g. empty pages
// with a recurring nextLink). Hitting the cap is not an error; the connector
// reports the assignments it gathered. Mirrors firewall.maxRuleCollectionGroupPages
// (slice 634, P0-634-2).
const maxRoleAssignmentPages = 100

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

// armRoleAssignment mirrors one Microsoft.Authorization/roleAssignments entry at
// a resource scope. METADATA ONLY: the principal object id and the
// roleDefinition resource id (which names the granted role). NEVER a secret,
// key, or certificate value — roleAssignments live wholly on the management
// plane (P0-615-2).
type armRoleAssignment struct {
	Properties struct {
		PrincipalID      string `json:"principalId"`
		RoleDefinitionID string `json:"roleDefinitionId"`
	} `json:"properties"`
}

type armRoleAssignmentPage struct {
	Value []armRoleAssignment `json:"value"`
	// NextLink is the ARM continuation cursor: an absolute URL carrying an opaque
	// skiptoken that must be requested verbatim (not reconstructed). Empty on the
	// last page (slice 623).
	NextLink string `json:"nextLink"`
}

// armRoleDefinition mirrors a Microsoft.Authorization/roleDefinitions entry. We
// read only the human-readable role NAME (e.g. "Key Vault Reader") — never any
// secret material.
type armRoleDefinition struct {
	Properties struct {
		RoleName string `json:"roleName"`
	} `json:"properties"`
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
	// Per-run state for the RBAC second read (slice 615): a role-name cache so a
	// given roleDefinition guid is resolved at most once, and a counter that caps
	// the total role assignments enumerated this run (DoS guards).
	roleNames := make(map[string]string)
	var assignmentsThisRun int
	for _, v := range page.Value {
		rv := RawVault{
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
		}
		// Symmetric least-privilege read (slice 615): an RBAC-authorization vault
		// carries no legacy access policies — its principals come from
		// Microsoft.Authorization/roleAssignments scoped to the vault resource id.
		// A legacy access-policy vault is fully described by accessPolicies above;
		// we do NOT issue the second read for it.
		if rv.RBACAuthorization && rv.ID != "" {
			entries, n, rerr := c.listVaultRoleAssignments(ctx, rv.ID, roleNames, &assignmentsThisRun)
			if rerr != "" {
				// A per-vault roleAssignments read error marks the vault
				// INCONCLUSIVE (the verdict() path) rather than dropping it — the
				// same fail-soft contract the access-policy path uses.
				rv.ReadError = rerr
			}
			assignmentsThisRun += n
			rv.AccessEntries = append(rv.AccessEntries, entries...)
		}
		out = append(out, rv)
	}
	return out, nil
}

// listVaultRoleAssignments reads Microsoft.Authorization/roleAssignments scoped
// to one vault resource id, following the ARM nextLink cursor across pages
// (slice 623), and maps each into a rbac_role_assignment AccessEntry (principal
// object id + resolved role definition NAME). METADATA ONLY — never a
// secret/key/certificate value (P0-615-2 / P0-623-3); ARM Reader suffices
// (P0-615-3 / P0-623-4).
//
// Bounded by construction (DoS guard, threat-model D), UNCHANGED in WHAT it
// collects by slice 623 — only HOW MANY pages it walks:
//   - the per-vault record cap (maxRoleAssignmentsPerVault) truncates the
//     collected assignments for one vault across all its pages;
//   - the run-wide cap (maxRoleAssignmentsPerRun) skips the read entirely once
//     the run total is reached and stops the walk mid-vault when crossed (the
//     DoS backstop slice 623 keeps, P0-623-2);
//   - the per-vault page cap (maxRoleAssignmentPages) is the loop-termination
//     backstop so a self-pointing / non-terminating nextLink terminates rather
//     than looping forever (P0-623-2).
//
// Every request — first page and every nextLink follow-up — is a GET. On read
// error it returns the error STRING (so the caller can mark the vault
// INCONCLUSIVE) rather than failing the whole run — one throttled vault must not
// blind the connector to the rest of the estate. Assignments gathered before an
// error on a later page are discarded so the vault is verdicted INCONCLUSIVE
// (partial-read honesty) rather than reported as a complete-but-truncated set.
func (c *Client) listVaultRoleAssignments(ctx context.Context, vaultID string, roleNames map[string]string, runTotal *int) ([]AccessEntry, int, string) {
	if *runTotal >= maxRoleAssignmentsPerRun {
		return nil, 0, ""
	}
	next := fmt.Sprintf("%s%s/providers/Microsoft.Authorization/roleAssignments?api-version=%s",
		c.BaseURL, vaultID, authzAPIVersion)

	entries := make([]AccessEntry, 0)
	for page := 0; page < maxRoleAssignmentPages; page++ {
		raPage, rerr := c.getRoleAssignmentPage(ctx, next)
		if rerr != "" {
			return nil, 0, rerr
		}
		for _, ra := range raPage.Value {
			if len(entries) >= maxRoleAssignmentsPerVault {
				break
			}
			if *runTotal+len(entries) >= maxRoleAssignmentsPerRun {
				break
			}
			if ra.Properties.PrincipalID == "" {
				continue
			}
			entries = append(entries, AccessEntry{
				PrincipalID:   ra.Properties.PrincipalID,
				PrincipalType: "rbac_role_assignment",
				RoleName:      c.resolveRoleName(ctx, ra.Properties.RoleDefinitionID, roleNames),
			})
		}
		// Stop following the cursor once a cap is reached or the cursor is spent.
		if len(entries) >= maxRoleAssignmentsPerVault ||
			*runTotal+len(entries) >= maxRoleAssignmentsPerRun ||
			strings.TrimSpace(raPage.NextLink) == "" {
			break
		}
		// The server-issued nextLink is an absolute URL carrying an opaque
		// skiptoken — follow it verbatim.
		next = raPage.NextLink
	}
	return entries, len(entries), ""
}

// getRoleAssignmentPage GETs one roleAssignments page (first page or a nextLink
// follow-up). It returns the error as a STRING so the caller can mark the vault
// INCONCLUSIVE rather than failing the whole run.
func (c *Client) getRoleAssignmentPage(ctx context.Context, u string) (*armRoleAssignmentPage, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err.Error()
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, (&APIError{Status: res.StatusCode, Body: drain(res.Body)}).Error()
	}
	var page armRoleAssignmentPage
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode role assignments: %w", err).Error()
	}
	return &page, ""
}

// resolveRoleName turns a roleDefinition resource id into its human-readable
// role NAME (e.g. "Key Vault Reader"), caching per run so each distinct guid is
// looked up at most once and capping the distinct-guid lookups per run (DoS
// guard). A failed or unresolved lookup falls back to the bare definition guid —
// the evidence still names the role unambiguously, just by its stable id. The
// name carries NO secret material.
func (c *Client) resolveRoleName(ctx context.Context, roleDefinitionID string, cache map[string]string) string {
	if roleDefinitionID == "" {
		return ""
	}
	if name, ok := cache[roleDefinitionID]; ok {
		return name
	}
	fallback := roleDefinitionGUID(roleDefinitionID)
	if len(cache) >= maxRoleDefinitionLookupsPerRun {
		return fallback
	}
	name := c.fetchRoleDefinitionName(ctx, roleDefinitionID)
	if name == "" {
		name = fallback
	}
	cache[roleDefinitionID] = name
	return name
}

// fetchRoleDefinitionName GETs a single roleDefinition and returns its roleName.
// Read-only (Reader suffices); returns "" on any error so the caller falls back
// to the definition guid.
func (c *Client) fetchRoleDefinitionName(ctx context.Context, roleDefinitionID string) string {
	u := fmt.Sprintf("%s%s?api-version=%s", c.BaseURL, roleDefinitionID, authzAPIVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	c.applyAuth(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	var def armRoleDefinition
	if err := json.NewDecoder(res.Body).Decode(&def); err != nil {
		return ""
	}
	return strings.TrimSpace(def.Properties.RoleName)
}

// roleDefinitionGUID extracts the trailing guid from a roleDefinitions resource
// id (.../roleDefinitions/<guid>). Returns the input unchanged when no segment
// is present.
func roleDefinitionGUID(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 && i+1 < len(id) {
		return id[i+1:]
	}
	return id
}

// mapAccessPolicies normalizes the legacy access-policy array into the
// connector's access-METADATA-only shape. Each entry carries the principal
// object id and the permission VERBS it was granted (keys/secrets/certificates/
// storage), namespaced as "<area>:<verb>". RBAC-mode vaults carry no access
// policies; their principals come from the Microsoft.Authorization/
// roleAssignments second read (slice 615, listVaultRoleAssignments) instead.
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
