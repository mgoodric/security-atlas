// Package idem derives idempotency keys for the manual.upload connector.
//
// Three emitters, three key shapes — all prefixed with the evidence kind so
// keys never collide across modes:
//
//   - LocalRowKey: sha256("manual.upload|" + file_path + "|" + row + "|" + sha256(row_json))
//   - S3ObjectKey: sha256("manual.upload|" + bucket + "|" + key + "|" + etag)
//   - SFTPFileKey: sha256("manual.upload|" + host + "|" + path + "|" + mtime + "|" + size)
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

// LocalRowKey returns the idempotency key for a CSV row: its file path,
// its index and the sha256 of its encoded content. The row's content is
// the freshness discriminator, the way the etag is for S3 and mtime for
// SFTP: a re-run over an unchanged file reproduces every key, so the
// first push's observed_at stands, and an edited row gets a new key.
// The key carried the run hour before #2940, which made every hourly
// re-run write each row again with a fresh observed_at.
func LocalRowKey(filePath string, rowIndex int, rowJSON []byte) string {
	return sha256Hex(evidenceKind + "|" + filePath + "|" + strconv.Itoa(rowIndex) + "|" + sha256Hex(string(rowJSON)))
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
// modification time, the connector emits a new record. size covers what
// mtime misses (#2940): a rewrite within the server's mtime resolution,
// often one second, or a copy that keeps the source's mtime.
func SFTPFileKey(host, path string, mtime time.Time, size int64) string {
	return sha256Hex(evidenceKind + "|" + host + "|" + path + "|" + mtime.UTC().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(size, 10))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
