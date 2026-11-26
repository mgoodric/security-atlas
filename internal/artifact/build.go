package artifact

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Environment variables the platform binary reads to wire the store
// (security-atlas#1928). Credentials and region are deliberately NOT
// listed: they come from the AWS SDK default chain (AWS_ACCESS_KEY_ID /
// AWS_SECRET_ACCESS_KEY / AWS_REGION, shared config, IRSA, instance
// role), never from an atlas-specific variable.
const (
	EnvBucket         = "ARTIFACTS_BUCKET"
	EnvEndpoint       = "ARTIFACTS_S3_ENDPOINT"
	EnvPublicEndpoint = "ARTIFACTS_S3_PUBLIC_ENDPOINT"
	EnvForcePathStyle = "ARTIFACTS_S3_FORCE_PATH_STYLE"
)

// EnvConfig is the deployment wiring for the store, read from the
// environment by ConfigFromEnv.
type EnvConfig struct {
	// Bucket is the S3 bucket. Empty means the store is not configured.
	Bucket string
	// Endpoint overrides the S3 endpoint the server talks to (MinIO,
	// B2, ...). Empty uses the SDK's AWS endpoint resolution.
	Endpoint string
	// PublicEndpoint is the endpoint baked into presigned download URLs.
	// Browsers and API clients follow those URLs, so it must be reachable
	// from outside the server's network (http://minio:9000 is not).
	// Empty falls back to Endpoint.
	PublicEndpoint string
	// UsePathStyle selects path-style addressing (bucket in the path,
	// not the host). MinIO and most S3-compatibles need it.
	UsePathStyle bool
}

// ConfigFromEnv reads the store wiring. ok is false when ARTIFACTS_BUCKET
// is unset or blank: the store stays off and the artifact routes stay
// unmounted. ARTIFACTS_S3_FORCE_PATH_STYLE defaults to true when an
// endpoint override is set (the MinIO shape) and false otherwise (AWS
// virtual-hosted style); an unparseable value is an error rather than a
// silent default.
func ConfigFromEnv(lookup func(string) (string, bool)) (cfg EnvConfig, ok bool, err error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	cfg.Bucket = get(EnvBucket)
	if cfg.Bucket == "" {
		return EnvConfig{}, false, nil
	}
	cfg.Endpoint = get(EnvEndpoint)
	cfg.PublicEndpoint = get(EnvPublicEndpoint)
	if cfg.PublicEndpoint == "" {
		cfg.PublicEndpoint = cfg.Endpoint
	}
	cfg.UsePathStyle = cfg.Endpoint != ""
	if raw := get(EnvForcePathStyle); raw != "" {
		b, perr := strconv.ParseBool(raw)
		if perr != nil {
			return EnvConfig{}, false, fmt.Errorf("artifact: %s=%q is not a boolean", EnvForcePathStyle, raw)
		}
		cfg.UsePathStyle = b
	}
	return cfg, true, nil
}

// Build constructs a Store from cfg with an S3 client from the AWS SDK
// default credential chain, then probes the bucket with HeadBucket. A
// configured-but-unreachable store is an error, so the binary fails at
// boot the way it does for an unreachable NATS_URL rather than serving
// upload routes that 500 on first use. ctx bounds the probe.
func Build(ctx context.Context, pool *pgxpool.Pool, cfg EnvConfig) (*Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("artifact: load aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, s3Options(cfg.Endpoint, cfg.UsePathStyle))
	presignClient := client
	if cfg.PublicEndpoint != cfg.Endpoint {
		presignClient = s3.NewFromConfig(awsCfg, s3Options(cfg.PublicEndpoint, cfg.UsePathStyle))
	}
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.Bucket)}); err != nil {
		return nil, fmt.Errorf("artifact: bucket %q unreachable: %w", cfg.Bucket, err)
	}
	return NewStore(Config{
		Pool: pool,
		S3:   client,
		Presigner: &S3Presigner{
			Bucket: cfg.Bucket,
			Client: s3.NewPresignClient(presignClient),
		},
		Bucket: cfg.Bucket,
	})
}

func s3Options(endpoint string, pathStyle bool) func(*s3.Options) {
	return func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.UsePathStyle = pathStyle
	}
}
