// Package artifact wraps an S3-compatible object store with per-tenant
// prefixes, server-side hashing, and an audit log. Closes the AC-6 partial
// from slice 013 (`payload > 1 MiB → S3 redirect`).
//
// Wire shape:
//
//   - Bucket is deployment-wide config (env `ARTIFACTS_BUCKET`, default
//     `atlas-artifacts` in docker-compose).
//   - Storage key is two flat UUID segments: `tenant-{tenant_uuid}/{artifact_uuid}`.
//     Server-generated; no user-controlled component.
//   - Content hash is the lowercase hex sha256 of the uploaded bytes,
//     computed server-side. Client-supplied hashes are never trusted.
//   - Signed URLs (downloads) have their TTL clamped server-side to the
//     configured `MaxDownloadTTL` (1 hour by default; never exceeds).
//   - Per-tenant prefix enforcement: the DB row's tenant_id is the source
//     of truth; the derived storage_key is defense in depth.
//   - Every upload + download appends one row to artifact_access_log.
package artifact

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// MaxDownloadTTL is the upper bound on presigned-URL validity, enforced
// server-side. AC-2 calls for "max 1h"; this is the wall.
const MaxDownloadTTL = time.Hour

// DefaultDownloadTTL is what Get returns when no explicit TTL is requested.
// 15 minutes balances "share a link with a teammate" against blast radius.
const DefaultDownloadTTL = 15 * time.Minute

// MaxUploadBytes caps individual artifact uploads. 100 MiB is large enough
// for typical SOC 2 audit-pack bundles and annual policy exports without
// inviting denial-of-storage.
const MaxUploadBytes = int64(100 * 1024 * 1024)

// Action is the audit-log verb.
type Action string

const (
	ActionUpload   Action = "upload"
	ActionDownload Action = "download"
)

// Artifact is the metadata row returned by Store.Put / Store.Get / Store.FindByHash.
// The blob itself lives in S3 under the storage_key, in the configured bucket.
type Artifact struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	StorageKey  string // relative S3 key under bucket; e.g., tenant-{uuid}/{uuid}
	ContentHash string // lowercase hex sha256 of the bytes
	SizeBytes   int64
	ContentType string
	UploadedBy  string // credential id from credstore (slice 003)
	UploadedAt  time.Time
}

// PayloadURI returns the canonical s3:// URI for this artifact, suitable
// for echoing back in API responses. The bucket is supplied externally
// because the deployment can move buckets without a data migration.
func (a Artifact) PayloadURI(bucket string) string {
	return "s3://" + bucket + "/" + a.StorageKey
}

// PutInput is the create-artifact payload.
type PutInput struct {
	// ContentType is the MIME type provided by the upload (e.g.,
	// "application/pdf"). Required and non-empty.
	ContentType string
	// SizeBytes is the size of the bytes about to be written. Required.
	// The handler computes this from the multipart reader so this is the
	// authoritative size — clients cannot lie about it without the bytes
	// themselves being out of sync.
	SizeBytes int64
	// ContentHash is the lowercase-hex sha256 of the bytes, computed by
	// the caller AFTER reading them. The caller MUST be the server (the
	// handler). Clients cannot supply a hash that bypasses the re-compute.
	ContentHash string
	// UploadedBy is the credential id of the actor.
	UploadedBy string
	// Body is the raw blob bytes. The Store writes them to S3 then
	// inserts the metadata row.
	Body []byte
}

// PresignedDownload is what Store.GetPresigned returns: the metadata
// (so callers can render filename/size/type) plus the time-bounded URL.
type PresignedDownload struct {
	Artifact   Artifact
	URL        string
	ExpiresAt  time.Time
	TTLSeconds int64
	PayloadURI string // canonical s3:// URI for reference
}

// Errors surfaced by the Store. Handlers map them to HTTP codes.
var (
	// ErrNotFound means the artifact does not exist for the caller's
	// tenant. RLS guarantees no cross-tenant leak; handlers translate to
	// HTTP 404 (NOT 403 — avoids existence-disclosure).
	ErrNotFound = errors.New("artifact: not found")
	// ErrInvalidInput covers handler-layer validation (zero-size body,
	// empty content type, missing credential).
	ErrInvalidInput = errors.New("artifact: invalid input")
	// ErrOversized fires when the body exceeds MaxUploadBytes.
	ErrOversized = errors.New("artifact: body exceeds size cap")
	// ErrHashMismatch fires when a re-computed hash does not match the
	// canonical hash (defense-in-depth; should never trip in production).
	ErrHashMismatch = errors.New("artifact: hash mismatch")
)

// StorageKeyForTenant derives the canonical relative S3 key from the
// tenant uuid + artifact uuid. Two flat UUID segments — no traversal
// surface, no user-controlled component, no shell metacharacters
// possible.
func StorageKeyForTenant(tenantID, artifactID uuid.UUID) string {
	return "tenant-" + tenantID.String() + "/" + artifactID.String()
}

// ClampTTL returns the bounded download TTL. Negative or zero requests
// resolve to DefaultDownloadTTL; anything over MaxDownloadTTL is clamped
// to MaxDownloadTTL. AC-2 / anti-criterion ISC-A2.
func ClampTTL(requested time.Duration) time.Duration {
	if requested <= 0 {
		return DefaultDownloadTTL
	}
	if requested > MaxDownloadTTL {
		return MaxDownloadTTL
	}
	return requested
}
