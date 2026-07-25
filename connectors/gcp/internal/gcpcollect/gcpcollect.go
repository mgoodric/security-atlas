// Package gcpcollect reads the two high-signal GCP evidence surfaces the
// slice-442 connector emits: project IAM policy bindings + service-account
// inventory (access evidence), and Cloud Storage bucket configuration
// (encryption / public-access / versioning / retention evidence).
//
// Over-collection is the defining risk for a cloud connector (slice 442
// threat-model I): a GCP project holds extremely sensitive data this package
// must NEVER touch. Every collector here reads IAM-binding / service-account
// and bucket CONFIGURATION metadata only. There is no code path that reads a
// stored object's contents, a secret value, or a service-account key
// material, and the result structs physically have no field that could carry
// one. A reflection guard (gcpcollect_test.go) fails the build if such a
// field is ever added.
package gcpcollect

import (
	"context"
	"fmt"
)

// Result enumerates the per-record verdict. Maps 1:1 onto the gRPC Result
// enum at the cmd layer (mirrors awss3.EncryptionResult / slackcollect.Result).
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// IAMBinding is one (role, member) project-IAM grant plus the
// service-account inventory facts needed to interpret it. Binding METADATA
// only: the role name, the member identifier (an opaque principal id /
// service-account email — not personal contact PII), and connector-side
// heuristic flags. Never a service-account key, never object contents.
//
// The connector emits these as descriptive (INCONCLUSIVE) records: the
// platform evaluator owns the pass/fail decision per (control, binding),
// mirroring azure.entra_role_assignment. Whether a given role is "too
// privileged" is policy that belongs in the evaluator, not the connector.
type IAMBinding struct {
	Member       string // e.g. "serviceAccount:svc@proj.iam.gserviceaccount.com", "user:...", "group:..."
	MemberType   string // "user" | "serviceAccount" | "group" | "domain" | "specialGroup" | "unknown"
	Role         string // e.g. "roles/owner", "roles/storage.admin"
	IsPrivileged bool   // connector-side heuristic: a known high-privilege/primitive role
	IsServiceAcc bool   // the member is a service account (vs a human/group)
	Disabled     bool   // for serviceAccount members: the SA is disabled (deprovisioned)
	Result       Result
	Reason       string // human-readable inconclusive reason
}

// StorageBucket is the per-bucket hardening posture. Bucket CONFIGURATION
// only — encryption, public-access state, versioning, retention. Never the
// contents of any stored object, never an ACL entry that names a person's
// private data, never a signed-URL secret.
type StorageBucket struct {
	Name              string // globally-unique bucket name (the stable anchor)
	Location          string // bucket location (e.g. "US", "EUROPE-WEST1")
	DefaultKMSKeyName string // CMEK key resource name; empty == Google-managed key
	UniformAccess     bool   // uniform bucket-level access enabled (IAM-only, no per-object ACLs)
	PublicAccessFlag  string // public-access-prevention: "enforced" | "inherited" | "unspecified"
	VersioningEnabled bool   // object versioning enabled
	RetentionSeconds  int64  // retention-policy duration in seconds; 0 == no retention policy
	Result            Result
	Reason            string
}

// IAMAPI is the narrow surface this package consumes for the project IAM +
// service-account read. The concrete GCP client satisfies it; tests pass a
// fake. Paginated to bound a large project (threat-model D).
type IAMAPI interface {
	ListIAMBindings(ctx context.Context, pageToken string) (bindings []IAMBinding, nextPageToken string, err error)
}

// StorageAPI is the narrow surface for the Cloud Storage bucket-config read.
type StorageAPI interface {
	ListBuckets(ctx context.Context, pageToken string) (buckets []StorageBucket, nextPageToken string, err error)
}

// MaxPages bounds every paginated read so a pathologically large project
// (thousands of bindings, thousands of buckets) can never make a run
// unbounded (slice 442 threat-model D — denial of service).
const MaxPages = 100

// CollectIAMBindings walks the paginated project IAM policy + service-account
// inventory and assigns a per-binding descriptive verdict. Each binding is
// emitted INCONCLUSIVE (the evaluator owns the policy decision), except a
// disabled service-account grant which is `pass` (correctly deprovisioned).
func CollectIAMBindings(ctx context.Context, api IAMAPI) ([]IAMBinding, error) {
	out := make([]IAMBinding, 0, 64)
	pageToken := ""
	for page := 0; page < MaxPages; page++ {
		bindings, next, err := api.ListIAMBindings(ctx, pageToken)
		if err != nil {
			return nil, fmt.Errorf("gcpcollect: list IAM bindings: %w", err)
		}
		for _, b := range bindings {
			out = append(out, scoreBinding(b))
		}
		if next == "" {
			return out, nil
		}
		pageToken = next
	}
	return out, fmt.Errorf("gcpcollect: IAM bindings exceeded MaxPages (%d)", MaxPages)
}

// scoreBinding assigns the descriptive verdict. A disabled service-account
// grant is `pass` (deprovisioned, off the hot path); every live binding is
// INCONCLUSIVE so the evaluator — not the connector — decides whether the
// grant violates least-privilege.
func scoreBinding(b IAMBinding) IAMBinding {
	switch {
	case b.IsServiceAcc && b.Disabled:
		b.Result = ResultPass
		b.Reason = "service account disabled — grant inert"
	default:
		b.Result = ResultInconclusive
		if b.IsPrivileged {
			b.Reason = "high-privilege role binding — evaluator decides least-privilege fit"
		} else {
			b.Reason = "role binding — evaluator decides least-privilege fit"
		}
	}
	return b
}

// CollectBuckets walks the paginated Cloud Storage bucket inventory and
// assigns a per-bucket hardening verdict: a bucket that enforces
// public-access prevention AND uniform bucket-level access is `pass`; a
// bucket that allows public access or per-object ACLs is `fail`.
func CollectBuckets(ctx context.Context, api StorageAPI) ([]StorageBucket, error) {
	out := make([]StorageBucket, 0, 64)
	pageToken := ""
	for page := 0; page < MaxPages; page++ {
		buckets, next, err := api.ListBuckets(ctx, pageToken)
		if err != nil {
			return nil, fmt.Errorf("gcpcollect: list buckets: %w", err)
		}
		for _, b := range buckets {
			out = append(out, scoreBucket(b))
		}
		if next == "" {
			return out, nil
		}
		pageToken = next
	}
	return out, fmt.Errorf("gcpcollect: buckets exceeded MaxPages (%d)", MaxPages)
}

// scoreBucket assigns the bucket hardening verdict. A bucket is `pass` only
// when public access is explicitly prevented (enforced) and uniform
// bucket-level access is on (no legacy per-object ACL surface). Anything
// else fails with a specific reason so the operator sees why.
func scoreBucket(b StorageBucket) StorageBucket {
	switch {
	case b.PublicAccessFlag != "enforced":
		b.Result = ResultFail
		b.Reason = "public-access-prevention is not enforced"
	case !b.UniformAccess:
		b.Result = ResultFail
		b.Reason = "uniform bucket-level access is disabled (per-object ACLs permitted)"
	default:
		b.Result = ResultPass
	}
	return b
}
