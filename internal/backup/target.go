package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BackupObject describes one stored backup artifact in a Target.
type BackupObject struct {
	// Name is the artifact's logical name (the dump file name). It encodes
	// the backup timestamp so retention can select by recency without a
	// metadata round-trip.
	Name string
	// Size is the artifact size in bytes.
	Size int64
	// ModTime is when the artifact was written.
	ModTime time.Time
}

// Target is the pluggable backup destination (D3). One interface, two
// implementations: LocalTarget (default single-VM volume) and S3Target
// (off-host durability). Consistent with the evidence object-storage
// abstraction (internal/artifact).
type Target interface {
	// Kind reports "local" or "s3" for the status row (D8).
	Kind() string
	// Put writes an artifact (and its .sha256 sidecar) under name.
	Put(ctx context.Context, name string, body []byte) error
	// Get reads an artifact's bytes back.
	Get(ctx context.Context, name string) ([]byte, error)
	// List enumerates backup artifacts (excludes .sha256 sidecars).
	List(ctx context.Context) ([]BackupObject, error)
	// Delete removes an artifact and its sidecar.
	Delete(ctx context.Context, name string) error
}

// sha256Suffix is the integrity-sidecar extension (D7).
const sha256Suffix = ".sha256"

// ErrNotFound is returned by a Target when an artifact does not exist.
var ErrNotFound = errors.New("backup: artifact not found")

// HashBytes returns the lowercase-hex sha256 of b (D7).
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ----- LocalTarget -----

// LocalTarget writes backups to a directory on a mounted volume (the
// single-VM self-host default).
type LocalTarget struct {
	dir string
}

// NewLocalTarget constructs a LocalTarget, creating the directory if needed.
func NewLocalTarget(dir string) (*LocalTarget, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("backup: local target dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("backup: mkdir %s: %w", dir, err)
	}
	return &LocalTarget{dir: dir}, nil
}

// Kind implements Target.
func (t *LocalTarget) Kind() string { return "local" }

// Put writes the artifact and a sidecar holding its sha256.
func (t *LocalTarget) Put(_ context.Context, name string, body []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(t.dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("backup: write %s: %w", name, err)
	}
	sidecar := []byte(HashBytes(body) + "\n")
	if err := os.WriteFile(path+sha256Suffix, sidecar, 0o600); err != nil {
		return fmt.Errorf("backup: write sidecar %s: %w", name, err)
	}
	return nil
}

// Get reads the artifact bytes.
func (t *LocalTarget) Get(_ context.Context, name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(t.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("backup: read %s: %w", name, err)
	}
	return b, nil
}

// List enumerates backup artifacts (sidecars excluded).
func (t *LocalTarget) List(_ context.Context) ([]BackupObject, error) {
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		return nil, fmt.Errorf("backup: readdir: %w", err)
	}
	var out []BackupObject
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), sha256Suffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupObject{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	sortByNameDesc(out)
	return out, nil
}

// Delete removes the artifact and its sidecar (best-effort on the sidecar).
func (t *LocalTarget) Delete(_ context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	path := filepath.Join(t.dir, name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup: delete %s: %w", name, err)
	}
	_ = os.Remove(path + sha256Suffix)
	return nil
}

// ----- S3Target -----

// S3API is the narrow S3 surface this package needs. The concrete
// *s3.Client satisfies it; the integration test points it at MinIO. Mirrors
// internal/artifact's seam.
type S3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// S3Target writes backups to an S3-compatible bucket (off-host durability).
// Encryption at rest is the storage layer's responsibility (SSE / bucket
// policy) per the slice-432 runbook + threat-model I.
type S3Target struct {
	client S3API
	bucket string
	prefix string
}

// NewS3Target constructs an S3Target.
func NewS3Target(client S3API, bucket, prefix string) (*S3Target, error) {
	if client == nil {
		return nil, errors.New("backup: s3 client is required")
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, errors.New("backup: s3 bucket is required")
	}
	return &S3Target{client: client, bucket: bucket, prefix: strings.TrimSuffix(prefix, "/")}, nil
}

// Kind implements Target.
func (t *S3Target) Kind() string { return "s3" }

func (t *S3Target) key(name string) string {
	if t.prefix == "" {
		return name
	}
	return t.prefix + "/" + name
}

// Put writes the artifact + sidecar object.
func (t *S3Target) Put(ctx context.Context, name string, body []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	if _, err := t.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(t.bucket),
		Key:           aws.String(t.key(name)),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String("application/sql"),
	}); err != nil {
		return fmt.Errorf("backup: s3 put %s: %w", name, err)
	}
	sidecar := []byte(HashBytes(body) + "\n")
	if _, err := t.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(t.bucket),
		Key:           aws.String(t.key(name) + sha256Suffix),
		Body:          bytes.NewReader(sidecar),
		ContentLength: aws.Int64(int64(len(sidecar))),
		ContentType:   aws.String("text/plain"),
	}); err != nil {
		return fmt.Errorf("backup: s3 put sidecar %s: %w", name, err)
	}
	return nil
}

// Get reads the artifact bytes.
func (t *S3Target) Get(ctx context.Context, name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	out, err := t.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(t.key(name)),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("backup: s3 get %s: %w", name, err)
	}
	defer func() { _ = out.Body.Close() }()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("backup: s3 read %s: %w", name, err)
	}
	return b, nil
}

// List enumerates backup artifacts under the prefix (sidecars excluded).
func (t *S3Target) List(ctx context.Context) ([]BackupObject, error) {
	var prefix *string
	if t.prefix != "" {
		prefix = aws.String(t.prefix + "/")
	}
	var out []BackupObject
	var token *string
	for {
		page, err := t.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(t.bucket),
			Prefix:            prefix,
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("backup: s3 list: %w", err)
		}
		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			if strings.HasSuffix(key, sha256Suffix) {
				continue
			}
			name := key
			if t.prefix != "" {
				name = strings.TrimPrefix(key, t.prefix+"/")
			}
			bo := BackupObject{Name: name}
			if obj.Size != nil {
				bo.Size = *obj.Size
			}
			if obj.LastModified != nil {
				bo.ModTime = *obj.LastModified
			}
			out = append(out, bo)
		}
		if page.IsTruncated == nil || !*page.IsTruncated {
			break
		}
		token = page.NextContinuationToken
	}
	sortByNameDesc(out)
	return out, nil
}

// Delete removes the artifact + sidecar object.
func (t *S3Target) Delete(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if _, err := t.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(t.key(name)),
	}); err != nil {
		return fmt.Errorf("backup: s3 delete %s: %w", name, err)
	}
	_, _ = t.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(t.key(name) + sha256Suffix),
	})
	return nil
}

// ----- shared helpers -----

// validateName rejects names that could traverse out of the target (path
// traversal guard — the writer always supplies a generated name, but the
// guard makes the contract explicit; threat-model S/I).
func validateName(name string) error {
	if name == "" {
		return errors.New("backup: artifact name is required")
	}
	if strings.ContainsAny(name, "/\\") || name == "." || name == ".." || strings.Contains(name, "..") {
		return fmt.Errorf("backup: invalid artifact name %q", name)
	}
	return nil
}

func sortByNameDesc(objs []BackupObject) {
	sort.Slice(objs, func(i, j int) bool { return objs[i].Name > objs[j].Name })
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nosuchkey") || strings.Contains(msg, "notfound") || strings.Contains(msg, "404")
}
