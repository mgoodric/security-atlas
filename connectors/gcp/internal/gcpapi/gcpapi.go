// Package gcpapi adapts the real GCP REST APIs to the narrow interfaces the
// connector consumes (gcpauth.IdentityAPI + gcpcollect.IAMAPI /
// gcpcollect.StorageAPI). It is the ONLY package that touches the network and
// the ONLY place the GCP credential is read (via gcpauth.Credential.Value, at
// the Authorization header — never logged).
//
// Endpoint discipline (slice 442 threat-model I / P0-442-1 / P0-442-3): this
// adapter calls ONLY read endpoints on the resource-manager, IAM, and Cloud
// Storage management APIs. It has NO code path that calls the Storage JSON
// `objects` resource, the `objects.get`/media download, or any data-plane /
// secret-read endpoint. The struct decoders deliberately decode only the
// CONFIGURATION fields the connector emits — an object body, a service-account
// key, or an ACL secret in a GCP response is simply never read into any field.
package gcpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpauth"
	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpcollect"
)

// API host roots. All three are read-management surfaces; none is a data
// plane. The defensive guard below asserts every base is https.
const (
	// ResourceManagerBaseURL serves the project IAM policy (getIamPolicy).
	ResourceManagerBaseURL = "https://cloudresourcemanager.googleapis.com/v1"
	// IAMBaseURL serves the service-account inventory (serviceAccounts.list).
	IAMBaseURL = "https://iam.googleapis.com/v1"
	// StorageBaseURL serves bucket CONFIGURATION (buckets.list). The connector
	// never touches the sibling `/b/<bucket>/o` objects (data-plane) resource.
	StorageBaseURL = "https://storage.googleapis.com/storage/v1"
)

// privilegedRolePrefixes are the role names/prefixes the connector flags as
// high-privilege via a connector-side heuristic. The platform evaluator owns
// the authoritative policy decision; this is only a hint on the record.
var privilegedRolePrefixes = []string{
	"roles/owner",
	"roles/editor",
	"roles/iam.",             // any IAM-admin family role
	"roles/resourcemanager.", // org/project admin family
	"roles/storage.admin",
	"roles/storage.objectAdmin",
}

// Client is the GCP REST API adapter. Construct with New.
type Client struct {
	cred       gcpauth.Credential
	projectID  string
	httpClient *http.Client
	rmURL      string
	iamURL     string
	storageURL string
}

// New returns a Client bound to a single GCP project. insecureTLS is honored
// only for loopback test servers; production always verifies TLS.
func New(cred gcpauth.Credential, projectID string, insecureTLS bool) *Client {
	transport := &http.Transport{}
	if insecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback test endpoints only
	}
	return &Client{
		cred:       cred,
		projectID:  projectID,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: transport},
		rmURL:      ResourceManagerBaseURL,
		iamURL:     IAMBaseURL,
		storageURL: StorageBaseURL,
	}
}

// get issues an authenticated GET against a GCP API. The credential is read
// here and ONLY here, onto the Authorization header — never logged.
func (c *Client) get(ctx context.Context, fullURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("gcpapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cred.Value())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The error from Do can include the request URL but never the
		// Authorization header value; the credential cannot leak here.
		return fmt.Errorf("gcpapi: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gcpapi: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("gcpapi: decode response: %w", err)
	}
	return nil
}

// post issues an authenticated POST (used by getIamPolicy, which is a POST in
// the resource-manager v1 API). Body is nil-safe for the empty-policy request.
func (c *Client) post(ctx context.Context, fullURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("gcpapi: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cred.Value())
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("gcpapi: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gcpapi: unexpected status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("gcpapi: decode response: %w", err)
	}
	return nil
}

// --- gcpauth.IdentityAPI ---

// ResolveProject confirms the project id the client was constructed with. The
// connector is bound to a single project per run; this returns that id for
// scoping (no extra API call — the project id is the run input, mirroring how
// the AWS connector resolves the account it assumed into).
func (c *Client) ResolveProject(_ context.Context) (gcpauth.Identity, error) {
	if c.projectID == "" {
		return gcpauth.Identity{}, fmt.Errorf("gcpapi: no project id configured")
	}
	return gcpauth.Identity{ProjectID: c.projectID}, nil
}

// --- gcpcollect.IAMAPI ---

type iamPolicyResponse struct {
	Bindings []struct {
		Role    string   `json:"role"`
		Members []string `json:"members"`
	} `json:"bindings"`
}

type serviceAccountsResponse struct {
	Accounts []struct {
		Email    string `json:"email"`
		Disabled bool   `json:"disabled"`
	} `json:"accounts"`
	NextPageToken string `json:"nextPageToken"`
}

// ListIAMBindings reads the project IAM policy (getIamPolicy, a single
// unpaginated POST) on the first call, fanned out to one record per
// (role, member), enriched with service-account disabled-state from the
// paginated service-account inventory. Subsequent calls return the next page
// of nothing (the policy is unpaginated), so the collector's page loop exits.
//
// pageToken is the service-account inventory cursor. On the first call
// (empty token) the IAM policy is fetched and emitted; on every call the SA
// inventory page is fetched to fill the disabled map. To keep this stateless
// and simple, the whole SA inventory is walked on the first call and the
// disabled map applied to every binding — so this method emits the full
// binding set on the first call and an empty next-token.
func (c *Client) ListIAMBindings(ctx context.Context, pageToken string) ([]gcpcollect.IAMBinding, string, error) {
	// The IAM policy is a single document; emit it only on the first page.
	if pageToken != "" {
		return nil, "", nil
	}

	disabled, err := c.serviceAccountDisabledMap(ctx)
	if err != nil {
		return nil, "", err
	}

	var policy iamPolicyResponse
	if err := c.post(ctx, c.rmURL+"/projects/"+url.PathEscape(c.projectID)+":getIamPolicy", &policy); err != nil {
		return nil, "", err
	}

	out := make([]gcpcollect.IAMBinding, 0, 64)
	for _, b := range policy.Bindings {
		for _, m := range b.Members {
			mt, isSA, email := classifyMember(m)
			out = append(out, gcpcollect.IAMBinding{
				Member:       m,
				MemberType:   mt,
				Role:         b.Role,
				IsPrivileged: isPrivilegedRole(b.Role),
				IsServiceAcc: isSA,
				Disabled:     isSA && disabled[email],
			})
		}
	}
	return out, "", nil
}

// serviceAccountDisabledMap walks the paginated service-account inventory and
// returns email -> disabled. Inventory METADATA only (email + disabled flag);
// it NEVER reads a service-account key (keys.list / keys.get are not called).
func (c *Client) serviceAccountDisabledMap(ctx context.Context) (map[string]bool, error) {
	disabled := make(map[string]bool)
	pageToken := ""
	for page := 0; page < gcpcollect.MaxPages; page++ {
		q := url.Values{}
		q.Set("pageSize", "100")
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var r serviceAccountsResponse
		if err := c.get(ctx, c.iamURL+"/projects/"+url.PathEscape(c.projectID)+"/serviceAccounts?"+q.Encode(), &r); err != nil {
			return nil, err
		}
		for _, a := range r.Accounts {
			disabled[a.Email] = a.Disabled
		}
		if r.NextPageToken == "" {
			return disabled, nil
		}
		pageToken = r.NextPageToken
	}
	return disabled, fmt.Errorf("gcpapi: service-account inventory exceeded MaxPages (%d)", gcpcollect.MaxPages)
}

// classifyMember splits a GCP IAM member string ("type:value") into its
// member type, whether it is a service account, and the bare email (for SA
// disabled-map lookup). It reads only the membership identifier — never PII
// beyond the opaque principal id GCP itself uses on the binding.
func classifyMember(member string) (memberType string, isServiceAccount bool, email string) {
	idx := strings.IndexByte(member, ':')
	if idx < 0 {
		return "unknown", false, ""
	}
	prefix := member[:idx]
	value := member[idx+1:]
	switch prefix {
	case "user":
		return "user", false, value
	case "serviceAccount":
		return "serviceAccount", true, value
	case "group":
		return "group", false, value
	case "domain":
		return "domain", false, value
	case "allUsers", "allAuthenticatedUsers":
		return "specialGroup", false, ""
	default:
		return "unknown", false, value
	}
}

func isPrivilegedRole(role string) bool {
	for _, p := range privilegedRolePrefixes {
		if role == p || strings.HasPrefix(role, p) {
			return true
		}
	}
	return false
}

// --- gcpcollect.StorageAPI ---

type bucketsResponse struct {
	Items []struct {
		Name       string `json:"name"`
		Location   string `json:"location"`
		Encryption struct {
			DefaultKMSKeyName string `json:"defaultKmsKeyName"`
		} `json:"encryption"`
		IamConfiguration struct {
			UniformBucketLevelAccess struct {
				Enabled bool `json:"enabled"`
			} `json:"uniformBucketLevelAccess"`
			PublicAccessPrevention string `json:"publicAccessPrevention"`
		} `json:"iamConfiguration"`
		Versioning struct {
			Enabled bool `json:"enabled"`
		} `json:"versioning"`
		RetentionPolicy struct {
			RetentionPeriod string `json:"retentionPeriod"` // seconds, as a string
		} `json:"retentionPolicy"`
	} `json:"items"`
	NextPageToken string `json:"nextPageToken"`
}

// ListBuckets reads one page of the project's Cloud Storage bucket
// CONFIGURATION via buckets.list. Only bucket-config METADATA is decoded —
// the response's object lists are never requested and no object body is read.
func (c *Client) ListBuckets(ctx context.Context, pageToken string) ([]gcpcollect.StorageBucket, string, error) {
	q := url.Values{}
	q.Set("project", c.projectID)
	q.Set("maxResults", "100")
	// projection=noAcl: deliberately request the LEAST detail — never the ACL
	// entries (which can name individuals); only the config fields we decode.
	q.Set("projection", "noAcl")
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	var r bucketsResponse
	if err := c.get(ctx, c.storageURL+"/b?"+q.Encode(), &r); err != nil {
		return nil, "", err
	}
	out := make([]gcpcollect.StorageBucket, 0, len(r.Items))
	for _, b := range r.Items {
		flag := b.IamConfiguration.PublicAccessPrevention
		if flag == "" {
			flag = "unspecified"
		}
		out = append(out, gcpcollect.StorageBucket{
			Name:              b.Name,
			Location:          b.Location,
			DefaultKMSKeyName: b.Encryption.DefaultKMSKeyName,
			UniformAccess:     b.IamConfiguration.UniformBucketLevelAccess.Enabled,
			PublicAccessFlag:  flag,
			VersioningEnabled: b.Versioning.Enabled,
			RetentionSeconds:  parseRetentionSeconds(b.RetentionPolicy.RetentionPeriod),
		})
	}
	return out, r.NextPageToken, nil
}

// parseRetentionSeconds converts GCP's string retentionPeriod to seconds.
// Bounded by int64; a malformed or absent value yields 0 (no policy). The
// value is a duration, not a count narrowed to int32, so no overflow-narrowing
// guard is needed (the field is int64 end-to-end).
func parseRetentionSeconds(s string) int64 {
	if s == "" {
		return 0
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0 // malformed — treat as no policy rather than guessing
		}
		// Bound-check before the multiply/add to avoid int64 overflow on a
		// pathological response (defensive; GCP retention maxes at ~100 years).
		if n > (1<<62)/10 {
			return 1 << 62 // clamp absurd values; still signals "a policy exists"
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// assertReadOnlyHost is a defensive constant-fold guard kept here to make the
// endpoint discipline explicit to a reader: every URL this package builds is
// rooted at one of these read hosts.
var _ = func() bool {
	for _, base := range []string{ResourceManagerBaseURL, IAMBaseURL, StorageBaseURL} {
		if !strings.HasPrefix(base, "https://") {
			panic("gcpapi: base URLs must be https")
		}
	}
	return true
}()
