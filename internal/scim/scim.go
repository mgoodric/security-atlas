// Package scim implements an inbound SCIM 2.0 (RFC 7643 / RFC 7644)
// user-lifecycle provisioning surface for security-atlas (slice 508).
//
// SCIM is the push-from-IdP automation that closes the offboard→revocation
// window: when an employee is deprovisioned in the org's IdP (Okta / Entra),
// the IdP issues a SCIM `PATCH active=false` (or DELETE) and the user's
// security-atlas access is disabled and their sessions revoked — without the
// operator manually removing them.
//
// SCOPE (slice 508): the `User` resource (Create / Get / List(filter) /
// Replace / Patch / Delete) + the discovery endpoints
// (ServiceProviderConfig / ResourceTypes / Schemas). The `Group` resource and
// group→role mapping are slice 509; SCIM here NEVER assigns an atlas role
// (P0-508-3).
//
// Security model (the load-bearing part — see docs/issues/508-*.md STRIDE):
//
//   - Deprovision is DISABLE, not delete (P0-508-1): active=false sets the
//     user inactive + revokes sessions; the row is retained so the actor's
//     historical records survive (invariant #2). DELETE soft-disables too.
//   - The SCIM bearer credential authenticates ONLY the /scim/v2 endpoints
//     (P0-508-2): it cannot call /v1 platform APIs or mint a human session.
//     Enforced at the auth-middleware boundary (a distinct credential type).
//   - SCIM never assigns/escalates a role (P0-508-3): the attribute allow-list
//     is identity + active only; a role sent in a Patch/Create is ignored.
//   - Every read/write is RLS-confined to the credential's tenant (P0-508-4).
//
// This file holds the pure-Go wire marshaling + filter/PatchOp parsing (the
// fast unit-test surface). The DB-backed provisioning store lives in store.go;
// the revocable SCIM credential store in credentials.go.
package scim

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SCIM URN schema identifiers (RFC 7643 §8, RFC 7644 §3.12).
const (
	SchemaUser                  = "urn:ietf:params:scim:schemas:core:2.0:User"
	SchemaListResponse          = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	SchemaError                 = "urn:ietf:params:scim:api:messages:2.0:Error"
	SchemaPatchOp               = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	SchemaServiceProviderConfig = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	SchemaResourceType          = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	SchemaSchema                = "urn:ietf:params:scim:schemas:core:2.0:Schema"
)

// ContentType is the SCIM media type (RFC 7644 §3.1).
const ContentType = "application/scim+json"

// ResourceTypeUser is the SCIM resource-type name for users.
const ResourceTypeUser = "User"

// Meta is the common SCIM resource metadata (RFC 7643 §3.1).
type Meta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Location     string `json:"location,omitempty"`
	Version      string `json:"version,omitempty"`
}

// Email is a SCIM multi-valued email entry (RFC 7643 §4.1.2).
type Email struct {
	Value   string `json:"value"`
	Type    string `json:"type,omitempty"`
	Primary bool   `json:"primary,omitempty"`
}

// Name is the SCIM complex `name` attribute (RFC 7643 §4.1.1). We surface
// only the formatted form; the platform's user model carries a single
// display_name.
type Name struct {
	Formatted string `json:"formatted,omitempty"`
}

// User is the SCIM core User resource (RFC 7643 §4.1) — the subset slice 508
// maps. `Active` is the deprovision signal. `ExternalID` is the IdP's stable
// id. Roles are deliberately absent (P0-508-3).
type User struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id"`
	ExternalID  string   `json:"externalId,omitempty"`
	UserName    string   `json:"userName"`
	Name        *Name    `json:"name,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Emails      []Email  `json:"emails,omitempty"`
	Active      bool     `json:"active"`
	Meta        *Meta    `json:"meta,omitempty"`
}

// ListResponse is the SCIM list envelope (RFC 7644 §3.4.2).
type ListResponse struct {
	Schemas      []string `json:"schemas"`
	TotalResults int      `json:"totalResults"`
	StartIndex   int      `json:"startIndex"`
	ItemsPerPage int      `json:"itemsPerPage"`
	Resources    []any    `json:"Resources"`
}

// Error is the SCIM error response (RFC 7644 §3.12).
type Error struct {
	Schemas  []string `json:"schemas"`
	Status   string   `json:"status"`
	SCIMType string   `json:"scimType,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// NewError builds a SCIM error body. scimType is optional (RFC 7644 §3.12
// table) — pass "" to omit.
func NewError(status int, scimType, detail string) Error {
	return Error{
		Schemas:  []string{SchemaError},
		Status:   fmt.Sprintf("%d", status),
		SCIMType: scimType,
		Detail:   detail,
	}
}

// PatchOp is a SCIM PATCH request body (RFC 7644 §3.5.2).
type PatchOp struct {
	Schemas    []string         `json:"schemas"`
	Operations []PatchOperation `json:"Operations"`
}

// PatchOperation is one operation within a PatchOp. `Value` is left as raw
// JSON so a caller can interpret it per-path (a bare bool for `active`, a
// string for `displayName`, or an object for a no-path replace).
type PatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

// --- userName filter parsing (RFC 7644 §3.4.2.2, the AC-1 minimum) ---

// ErrUnsupportedFilter is returned for any filter the slice-508 minimum does
// not implement (anything other than `userName eq "x"`). The handler maps it
// to a 400 invalidFilter rather than silently returning the full list (which
// would be an information-disclosure footgun).
var ErrUnsupportedFilter = errors.New("scim: unsupported filter (only `userName eq \"value\"` is supported)")

// ParseUserNameFilter extracts the value from a `userName eq "value"` filter.
// Returns ("", false, nil) when filter is empty (no filter → full list).
// Returns ErrUnsupportedFilter for any other shape. The attribute name match
// is case-insensitive per RFC 7644 §3.4.2.2; the value is returned verbatim
// (the caller does the case-insensitive email comparison in SQL).
func ParseUserNameFilter(filter string) (value string, present bool, err error) {
	f := strings.TrimSpace(filter)
	if f == "" {
		return "", false, nil
	}
	// Expected shape: userName eq "value"  (whitespace-tolerant).
	const attr = "username"
	const op = "eq"
	// Find the quoted value first; everything before it must be `userName eq`.
	open := strings.Index(f, "\"")
	if open < 0 || !strings.HasSuffix(f, "\"") || len(f) < open+2 {
		return "", false, ErrUnsupportedFilter
	}
	prefix := strings.Fields(strings.ToLower(strings.TrimSpace(f[:open])))
	if len(prefix) != 2 || prefix[0] != attr || prefix[1] != op {
		return "", false, ErrUnsupportedFilter
	}
	val := f[open+1 : len(f)-1]
	if strings.Contains(val, "\"") {
		// A second quote inside means a compound/multi-clause filter we do
		// not support.
		return "", false, ErrUnsupportedFilter
	}
	return val, true, nil
}

// --- discovery documents (AC-2) ---

// ServiceProviderConfig is the SCIM /ServiceProviderConfig document
// (RFC 7644 §5). It advertises exactly the capabilities slice 508 ships:
// patch yes, bulk no, filter yes (the userName-eq minimum), changePassword
// no, sort no, etag no. authenticationSchemes advertises the OAuth bearer
// token scheme (the SCIM credential).
func ServiceProviderConfig() map[string]any {
	return map[string]any{
		"schemas":          []string{SchemaServiceProviderConfig},
		"documentationUri": "https://github.com/mgoodric/security-atlas",
		"patch":            map[string]any{"supported": true},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":           map[string]any{"supported": true, "maxResults": 200},
		"changePassword":   map[string]any{"supported": false},
		"sort":             map[string]any{"supported": false},
		"etag":             map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{
			{
				"type":        "oauthbearertoken",
				"name":        "OAuth Bearer Token",
				"description": "Authentication via a per-tenant SCIM-scoped bearer token issued by an atlas admin.",
				"primary":     true,
			},
		},
		"meta": map[string]any{"resourceType": "ServiceProviderConfig"},
	}
}

// ResourceTypes is the SCIM /ResourceTypes document (RFC 7643 §6). Only the
// User resource type is exposed (Group is slice 509).
func ResourceTypes(baseURL string) []map[string]any {
	return []map[string]any{
		{
			"schemas":          []string{SchemaResourceType},
			"id":               ResourceTypeUser,
			"name":             ResourceTypeUser,
			"endpoint":         "/Users",
			"schema":           SchemaUser,
			"meta":             map[string]any{"resourceType": "ResourceType", "location": baseURL + "/ResourceTypes/User"},
			"schemaExtensions": []any{},
		},
	}
}

// Schemas is the SCIM /Schemas document (RFC 7643 §7). It describes the core
// User schema attributes slice 508 supports. Kept intentionally minimal — the
// attributes we actually read/write.
func Schemas() []map[string]any {
	return []map[string]any{
		{
			"id":          SchemaUser,
			"name":        "User",
			"description": "User Account",
			"meta":        map[string]any{"resourceType": "Schema", "location": "/scim/v2/Schemas/" + SchemaUser},
			"attributes": []map[string]any{
				attr("userName", "string", true, "server", "always"),
				attr("displayName", "string", false, "none", "default"),
				attr("active", "boolean", false, "none", "default"),
				attr("externalId", "string", false, "none", "default"),
				attrComplex("emails"),
			},
		},
	}
}

func attr(name, typ string, required bool, uniqueness, returned string) map[string]any {
	return map[string]any{
		"name":        name,
		"type":        typ,
		"multiValued": false,
		"required":    required,
		"caseExact":   false,
		"mutability":  "readWrite",
		"returned":    returned,
		"uniqueness":  uniqueness,
	}
}

func attrComplex(name string) map[string]any {
	return map[string]any{
		"name":        name,
		"type":        "complex",
		"multiValued": true,
		"required":    false,
		"mutability":  "readWrite",
		"returned":    "default",
		"subAttributes": []map[string]any{
			{"name": "value", "type": "string", "multiValued": false, "required": false},
			{"name": "type", "type": "string", "multiValued": false, "required": false},
			{"name": "primary", "type": "boolean", "multiValued": false, "required": false},
		},
	}
}

// --- helpers for building a wire User from domain state ---

// formatRFC3339 renders a time for SCIM meta. Zero time → "".
func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// WireUser renders a DomainUser into the SCIM wire User. baseLocation is the
// resource collection URL (e.g. "https://host/scim/v2/Users") used to build
// meta.location. userName maps from the platform email; displayName from
// display_name; active from the boolean deprovision flag. Roles are NEVER
// emitted (P0-508-3 — there is no role attribute on the wire shape).
func WireUser(u DomainUser, baseLocation string) User {
	out := User{
		Schemas:     []string{SchemaUser},
		ID:          u.ID.String(),
		ExternalID:  u.ExternalID,
		UserName:    u.Email,
		DisplayName: u.DisplayName,
		Active:      u.Active,
		Meta: &Meta{
			ResourceType: ResourceTypeUser,
			Created:      formatRFC3339(u.CreatedAt),
			LastModified: formatRFC3339(u.UpdatedAt),
			Location:     strings.TrimRight(baseLocation, "/") + "/" + u.ID.String(),
		},
	}
	if u.Email != "" {
		out.Emails = []Email{{Value: u.Email, Primary: true, Type: "work"}}
	}
	if u.DisplayName != "" {
		out.Name = &Name{Formatted: u.DisplayName}
	}
	return out
}
