package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// S3API is the narrow surface this package needs from an S3 client.
// The concrete *s3.Client satisfies it; integration tests pass a real
// client pointed at MinIO. Unit tests can inject a fake.
type S3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// Presigner is the narrow surface for presigning download URLs. The
// concrete *s3.PresignClient.PresignGetObject satisfies it via a thin
// adapter; tests inject a fake.
type Presigner interface {
	PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Store persists artifact metadata in Postgres and the blob in S3. The
// pool must be connected as the application role so RLS is enforced.
type Store struct {
	pool      *pgxpool.Pool
	s3        S3API
	presigner Presigner
	bucket    string
	clock     func() time.Time
}

// Config wires the Store.
type Config struct {
	Pool      *pgxpool.Pool
	S3        S3API
	Presigner Presigner
	Bucket    string
	// Clock is optional; defaults to time.Now. Tests pin it for
	// deterministic ExpiresAt calculations.
	Clock func() time.Time
}

// NewStore constructs a Store. Returns an error on missing required fields.
func NewStore(cfg Config) (*Store, error) {
	if cfg.Pool == nil {
		return nil, errors.New("artifact: Pool is required")
	}
	if cfg.S3 == nil {
		return nil, errors.New("artifact: S3 client is required")
	}
	if cfg.Presigner == nil {
		return nil, errors.New("artifact: Presigner is required")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("artifact: Bucket is required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Store{
		pool:      cfg.Pool,
		s3:        cfg.S3,
		presigner: cfg.Presigner,
		bucket:    cfg.Bucket,
		clock:     clock,
	}, nil
}

// Bucket returns the configured bucket name (used by handlers to render
// payload_uri in responses without leaking the Store internals).
func (s *Store) Bucket() string { return s.bucket }

// Put writes the blob to S3 and inserts the metadata row. The hash and
// size in PutInput are inputs; the Store *recomputes* the sha256 from
// in.Body to guarantee the persisted hash matches the persisted bytes
// (anti-criterion ISC-A4 — never trust a client-supplied hash).
//
// Idempotency-by-content: if (tenant, content_hash) already exists for
// this tenant, the existing artifact is returned WITHOUT writing the
// blob again. This is a soft optimization, not a security feature; the
// re-upload still appears in the audit log on the caller side.
func (s *Store) Put(ctx context.Context, in PutInput) (Artifact, error) {
	if err := validatePut(in); err != nil {
		return Artifact{}, err
	}

	// Server-side hash re-compute. Whatever the caller supplied is
	// IGNORED on the persisted row; we overwrite with this value.
	sum := sha256.Sum256(in.Body)
	canonicalHash := hex.EncodeToString(sum[:])

	// Defence-in-depth: if the caller passed a hash AND it doesn't
	// match the re-computed one, surface ErrHashMismatch. This should
	// never trip in production (the handler always supplies its own
	// re-compute), but it makes the contract explicit.
	if in.ContentHash != "" && !strings.EqualFold(in.ContentHash, canonicalHash) {
		return Artifact{}, ErrHashMismatch
	}

	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return Artifact{}, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifact: parse tenant id: %w", err)
	}

	// Dedup lookup happens INSIDE the same transaction as the insert so
	// we don't have a check-then-write race. Read with the tenant GUC
	// applied — RLS keeps tenants honest at the DB level.
	var out Artifact
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		existing, lookupErr := q.FindArtifactByHash(ctx, dbx.FindArtifactByHashParams{
			TenantID:    pgUUID(tenantID),
			ContentHash: &canonicalHash,
		})
		if lookupErr == nil {
			out = fromRow(existing)
			return nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return fmt.Errorf("artifact: dedup lookup: %w", lookupErr)
		}

		// Fresh artifact: derive key, write to S3 first, then DB row.
		// If the DB insert fails we *delete* the S3 object so we don't
		// leak orphan blobs; if the S3 write fails we never touched DB.
		artifactID := uuid.New()
		key := StorageKeyForTenant(tenantID, artifactID)
		body := bytes.NewReader(in.Body)
		_, putErr := s.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(s.bucket),
			Key:           aws.String(key),
			Body:          body,
			ContentType:   aws.String(in.ContentType),
			ContentLength: aws.Int64(int64(len(in.Body))),
		})
		if putErr != nil {
			return fmt.Errorf("artifact: s3 put: %w", putErr)
		}

		row, insertErr := q.CreateArtifact(ctx, dbx.CreateArtifactParams{
			ID:          pgUUID(artifactID),
			TenantID:    pgUUID(tenantID),
			StorageKey:  key,
			ContentHash: &canonicalHash,
			SizeBytes:   int64(len(in.Body)),
			ContentType: in.ContentType,
			UploadedBy:  in.UploadedBy,
		})
		if insertErr != nil {
			// Best-effort cleanup of the orphaned blob. Errors here are
			// logged via the returned error message but cannot be acted
			// on transactionally.
			_, _ = s.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(s.bucket),
				Key:    aws.String(key),
			})
			return fmt.Errorf("artifact: insert metadata: %w", insertErr)
		}
		out = fromRow(row)
		return nil
	})
	return out, err
}

// Get returns the metadata for an artifact owned by the calling tenant.
// Cross-tenant lookups return ErrNotFound — RLS hides the row and we
// translate pgx.ErrNoRows accordingly (no existence-disclosure).
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Artifact, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return Artifact{}, err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifact: parse tenant id: %w", err)
	}
	var out Artifact
	err = s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		row, lookupErr := q.GetArtifact(ctx, dbx.GetArtifactParams{
			TenantID: pgUUID(tenantID),
			ID:       pgUUID(id),
		})
		if lookupErr != nil {
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("artifact: get: %w", lookupErr)
		}
		out = fromRow(row)
		return nil
	})
	return out, err
}

// Presign returns a time-bounded download URL for an artifact. The TTL
// is clamped server-side via ClampTTL; callers cannot extend it past
// MaxDownloadTTL no matter what they ask for.
//
// Order of operations matters for security: we look up the row FIRST
// (RLS enforces tenant boundary), and ONLY then build the presign. We
// never call S3 HeadObject for the existence check — that would leak
// presence via timing and is wasteful when Postgres already knows.
func (s *Store) Presign(ctx context.Context, id uuid.UUID, requestedTTL time.Duration) (PresignedDownload, error) {
	art, err := s.Get(ctx, id)
	if err != nil {
		return PresignedDownload{}, err
	}
	ttl := ClampTTL(requestedTTL)
	url, err := s.presigner.PresignGetObject(ctx, art.StorageKey, ttl)
	if err != nil {
		return PresignedDownload{}, fmt.Errorf("artifact: presign: %w", err)
	}
	return PresignedDownload{
		Artifact:   art,
		URL:        url,
		ExpiresAt:  s.clock().Add(ttl),
		TTLSeconds: int64(ttl / time.Second),
		PayloadURI: art.PayloadURI(s.bucket),
	}, nil
}

// LogAccess appends one row to artifact_access_log. Handlers MUST call
// this after every successful upload (action="upload") and every
// successful presign (action="download"). Failure here is logged but
// does not fail the request — the audit log is best-effort downstream
// of the actual operation.
//
// Note: this is *not* called from inside Put/Presign because the caller
// (the HTTP handler) is the one with the most context about the actor
// and the request shape, and the brief asks the audit log to record the
// *handler-level* event.
func (s *Store) LogAccess(ctx context.Context, artifactID uuid.UUID, action Action, actor string) error {
	if action != ActionUpload && action != ActionDownload {
		return fmt.Errorf("%w: action must be upload|download", ErrInvalidInput)
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("artifact: parse tenant id: %w", err)
	}
	return s.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		return q.LogArtifactAccess(ctx, dbx.LogArtifactAccessParams{
			ID:         pgUUID(uuid.New()),
			TenantID:   pgUUID(tenantID),
			ArtifactID: pgUUID(artifactID),
			Action:     string(action),
			Actor:      actor,
		})
	})
}

// inTx mirrors the pattern used in internal/vendor and internal/scope:
// open a transaction, apply the tenancy GUC, run fn, commit on success.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("artifact: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("artifact: commit: %w", err)
	}
	return nil
}

func validatePut(in PutInput) error {
	if strings.TrimSpace(in.ContentType) == "" {
		return fmt.Errorf("%w: content_type is required", ErrInvalidInput)
	}
	if strings.TrimSpace(in.UploadedBy) == "" {
		return fmt.Errorf("%w: uploaded_by is required", ErrInvalidInput)
	}
	if len(in.Body) == 0 {
		return fmt.Errorf("%w: body must be non-empty", ErrInvalidInput)
	}
	if int64(len(in.Body)) > MaxUploadBytes {
		return ErrOversized
	}
	return nil
}

func fromRow(r dbx.Artifact) Artifact {
	a := Artifact{
		ID:          r.ID.Bytes,
		TenantID:    r.TenantID.Bytes,
		StorageKey:  r.StorageKey,
		SizeBytes:   r.SizeBytes,
		ContentType: r.ContentType,
		UploadedBy:  r.UploadedBy,
	}
	if r.ContentHash != nil {
		a.ContentHash = *r.ContentHash
	}
	if r.UploadedAt.Valid {
		a.UploadedAt = r.UploadedAt.Time
	}
	return a
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// ----- Presigner adapter for the real AWS SDK -----

// S3Presigner adapts *s3.PresignClient to the Presigner interface.
type S3Presigner struct {
	Bucket string
	Client *s3.PresignClient
}

// PresignGetObject implements Presigner.
func (p *S3Presigner) PresignGetObject(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := p.Client.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}
