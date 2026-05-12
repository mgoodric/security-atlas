package idem

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestLocalRowKey_Deterministic(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 5, 12, 4, 17, 33, 0, time.UTC)
	a := LocalRowKey("/data/findings.csv", 42, ts)
	b := LocalRowKey("/data/findings.csv", 42, ts)
	if a != b {
		t.Fatalf("LocalRowKey not deterministic: %q vs %q", a, b)
	}
	if len(a) != sha256.Size*2 {
		t.Fatalf("expected hex-encoded sha256 (64 chars), got %d", len(a))
	}
}

func TestLocalRowKey_HourTruncation(t *testing.T) {
	t.Parallel()
	t1 := time.Date(2026, 5, 12, 4, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 12, 4, 59, 59, 999_000_000, time.UTC)
	if LocalRowKey("/data/findings.csv", 0, t1) != LocalRowKey("/data/findings.csv", 0, t2) {
		t.Fatal("same hour must yield same key")
	}
	t3 := time.Date(2026, 5, 12, 5, 0, 0, 0, time.UTC)
	if LocalRowKey("/data/findings.csv", 0, t1) == LocalRowKey("/data/findings.csv", 0, t3) {
		t.Fatal("next hour must yield different key")
	}
}

func TestLocalRowKey_RowDistinguishes(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 5, 12, 4, 0, 0, 0, time.UTC)
	if LocalRowKey("/data/findings.csv", 0, ts) == LocalRowKey("/data/findings.csv", 1, ts) {
		t.Fatal("different rows must yield different keys")
	}
}

func TestLocalRowKey_PrefixedWithEvidenceKind(t *testing.T) {
	t.Parallel()
	// The contract: sha256("manual.upload|" + file_path + "|" + row + "|" + hour).
	// Pin the exact bytes that go into the hash so a future refactor can't
	// silently change the discriminator.
	ts := time.Date(2026, 5, 12, 4, 0, 0, 0, time.UTC)
	hour := ts.UTC().Truncate(time.Hour).Format(time.RFC3339)
	want := hash("manual.upload|/x.csv|7|" + hour)
	got := LocalRowKey("/x.csv", 7, ts)
	if got != want {
		t.Fatalf("unexpected key shape:\n got %q\nwant %q", got, want)
	}
}

func TestS3ObjectKey_Deterministic(t *testing.T) {
	t.Parallel()
	a := S3ObjectKey("evidence-bucket", "audits/q1.pdf", "etag-abc-123")
	b := S3ObjectKey("evidence-bucket", "audits/q1.pdf", "etag-abc-123")
	if a != b {
		t.Fatalf("S3ObjectKey not deterministic")
	}
}

func TestS3ObjectKey_EtagDistinguishes(t *testing.T) {
	t.Parallel()
	a := S3ObjectKey("b", "k", "etag-1")
	b := S3ObjectKey("b", "k", "etag-2")
	if a == b {
		t.Fatal("etag must enter the hash")
	}
}

func TestS3ObjectKey_Shape(t *testing.T) {
	t.Parallel()
	want := hash("manual.upload|evidence-bucket|audits/q1.pdf|etag-abc-123")
	got := S3ObjectKey("evidence-bucket", "audits/q1.pdf", "etag-abc-123")
	if got != want {
		t.Fatalf("unexpected key shape:\n got %q\nwant %q", got, want)
	}
}

func TestSFTPFileKey_Deterministic(t *testing.T) {
	t.Parallel()
	mtime := time.Date(2026, 5, 12, 4, 17, 33, 0, time.UTC)
	a := SFTPFileKey("sftp.example.com", "/inbox/q1.pdf", mtime)
	b := SFTPFileKey("sftp.example.com", "/inbox/q1.pdf", mtime)
	if a != b {
		t.Fatal("SFTPFileKey not deterministic")
	}
}

func TestSFTPFileKey_MtimeDistinguishes(t *testing.T) {
	t.Parallel()
	m1 := time.Date(2026, 5, 12, 4, 17, 33, 0, time.UTC)
	m2 := time.Date(2026, 5, 12, 4, 17, 34, 0, time.UTC)
	a := SFTPFileKey("h", "/p", m1)
	b := SFTPFileKey("h", "/p", m2)
	if a == b {
		t.Fatal("mtime must enter the hash")
	}
}

func TestSFTPFileKey_Shape(t *testing.T) {
	t.Parallel()
	mtime := time.Date(2026, 5, 12, 4, 17, 33, 0, time.UTC).UTC()
	want := hash("manual.upload|sftp.example.com|/inbox/q1.pdf|" + mtime.Format(time.RFC3339Nano))
	got := SFTPFileKey("sftp.example.com", "/inbox/q1.pdf", mtime)
	if got != want {
		t.Fatalf("unexpected key shape:\n got %q\nwant %q", got, want)
	}
}

// hash is a test-local helper to pin the exact input contract. Named to
// avoid shadowing the package-private sha256Hex.
func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
