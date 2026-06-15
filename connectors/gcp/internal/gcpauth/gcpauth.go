// Package gcpauth handles GCP authentication + identity resolution for the
// connector. The connector authenticates TO GCP with a least-privilege
// read-only identity — Application Default Credentials (ADC) or a
// service-account key — and resolves the GCP project id used as the
// `cloud_project` scope dimension.
//
// The GCP credential is source-side (invariant #3) and NEVER logged or
// transmitted to the platform (slice 442 threat-model I / P0-442-4). It is
// carried only on the Authorization header of outbound GCP API calls (in the
// gcpapi adapter). This package deliberately exposes no Stringer / Format
// method that could leak the credential into a log line.
package gcpauth

import (
	"context"
	"errors"
	"fmt"
)

// RequiredRoles is the minimal read-only GCP role set the connector needs —
// documented as the minimum (slice 442 AC-4 / threat-model E). It
// deliberately grants ONLY read-level roles: the connector must never be able
// to mutate a GCP resource or read object/secret contents.
//
//   - roles/iam.securityReviewer — read the project IAM policy + the
//     service-account inventory (no write, no key creation).
//   - roles/storage.bucketViewer — read bucket CONFIGURATION (location,
//     encryption, public-access, versioning, retention) WITHOUT read access
//     to any object's CONTENTS.
//
// roles/storage.objectViewer is deliberately EXCLUDED: it grants read access
// to object data, which the connector must never have.
var RequiredRoles = []string{
	"roles/iam.securityReviewer",
	"roles/storage.bucketViewer",
}

// BannedRoles are write/admin/data-read roles the connector must never be
// granted. A deployment that hands the connector any of these is
// misconfigured — the connector cannot enforce GCP's IAM grant, but it
// documents and asserts the boundary (slice 442 P0-442-2 / P0-442-3).
var BannedRoles = []string{
	"roles/owner",
	"roles/editor",
	"roles/iam.serviceAccountKeyAdmin",
	"roles/storage.admin",
	"roles/storage.objectViewer", // data-plane read — never granted
	"roles/storage.objectAdmin",
}

// Credential wraps the raw GCP OAuth2 access token the connector presents to
// GCP REST APIs. It is an opaque value: it has no String() method that
// returns the secret, so a stray `%v`/`%s` on a Credential in a log line
// prints a redaction marker, not the token. Read the raw value only via
// Value() at the single GCP-API call site (the gcpapi adapter).
type Credential struct {
	raw string
}

// NewCredential constructs a Credential from the raw OAuth2 access token.
// Returns an error on the empty string so the connector fails loudly rather
// than calling GCP unauthenticated.
func NewCredential(raw string) (Credential, error) {
	if raw == "" {
		return Credential{}, errors.New("gcpauth: a GCP access token is required (ADC or a service-account key)")
	}
	return Credential{raw: raw}, nil
}

// Value returns the raw access token. Call this ONLY at the GCP-API
// Authorization header. Never pass the result to a logger.
func (c Credential) Value() string { return c.raw }

// String implements fmt.Stringer to guarantee the secret never leaks through
// a `%v`/`%s` verb anywhere in the connector or its dependencies.
func (c Credential) String() string { return "Credential(***redacted***)" }

// GoString implements fmt.GoStringer so `%#v` (used by some loggers and by
// testify dumps) is also redacted.
func (c Credential) GoString() string { return "gcpauth.Credential{raw:\"***redacted***\"}" }

// Identity is the resolved GCP project context for one run.
type Identity struct {
	ProjectID string // GCP project id (e.g. "my-prod-project") — the scope value
}

// IdentityAPI is the narrow surface this package needs to resolve / confirm
// the project id. The concrete GCP client satisfies it; tests pass a fake.
type IdentityAPI interface {
	ResolveProject(ctx context.Context) (Identity, error)
}

// Resolve returns the project identity for scoping. Fails loudly when the
// project id cannot be resolved so the connector never emits un-scoped
// records (mirrors awsauth.ResolveIdentity / slackauth.Resolve).
func Resolve(ctx context.Context, api IdentityAPI) (Identity, error) {
	id, err := api.ResolveProject(ctx)
	if err != nil {
		return Identity{}, fmt.Errorf("gcpauth: resolve project: %w", err)
	}
	if id.ProjectID == "" {
		return Identity{}, errors.New("gcpauth: empty GCP project id — cannot scope evidence")
	}
	return id, nil
}
