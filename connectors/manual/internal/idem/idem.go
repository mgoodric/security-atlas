// Package idem derives idempotency keys for the manual.upload connector.
//
// Three emitters, three key shapes — all prefixed with the evidence kind so
// keys never collide across modes:
//
//   - LocalRowKey: sha256("manual.upload|" + file_path + "|" + row + "|" + hour)
//   - S3ObjectKey: sha256("manual.upload|" + bucket + "|" + key + "|" + etag)
//   - SFTPFileKey: sha256("manual.upload|" + host + "|" + path + "|" + mtime)
//
// Each is deterministic on its inputs: re-running the connector against the
// same source must produce byte-identical keys so the ledger dedupes.
package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

const evidenceKind = "manual.upload"

// LocalRowKey returns the idempotency key for a (file_path, row_index,
// observed_at) tuple. observed_at is truncated to the hour in UTC so two
// runs within the same hour produce identical keys (matches the
// connectors/aws + connectors/github convention).
func LocalRowKey(filePath string, rowIndex int, observedAt time.Time) string {
	hour := observedAt.UTC().Truncate(time.Hour).Format(time.RFC3339)
	return sha256Hex(evidenceKind + "|" + filePath + "|" + strconv.Itoa(rowIndex) + "|" + hour)
}

// S3ObjectKey returns the idempotency key for an S3 object. The etag is the
// freshness discriminator: when the object is re-uploaded with new content,
// AWS rotates the etag and the connector emits a new record. Unchanged
// objects across runs return the same key (dedup).
func S3ObjectKey(bucket, key, etag string) string {
	return sha256Hex(evidenceKind + "|" + bucket + "|" + key + "|" + etag)
}

// SFTPFileKey returns the idempotency key for an SFTP file. mtime is the
// freshness discriminator: when the remote file is rewritten with a newer
// modification time, the connector emits a new record.
func SFTPFileKey(host, path string, mtime time.Time) string {
	return sha256Hex(evidenceKind + "|" + host + "|" + path + "|" + mtime.UTC().Format(time.RFC3339Nano))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
