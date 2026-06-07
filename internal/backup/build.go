package backup

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BuildTarget constructs the configured backup Target (D3). The local target
// (default) needs only the directory; the s3 target builds an S3 client from
// the standard AWS SDK credential chain (env: AWS_ACCESS_KEY_ID etc.) and the
// optional ATLAS_BACKUP_S3_ENDPOINT for MinIO/S3-compatible endpoints.
//
// The s3 endpoint is read here (not in Config) so the path-style + custom
// endpoint plumbing stays local to the s3 branch; a local-only deployment
// never touches the AWS SDK.
func BuildTarget(ctx context.Context, cfg Config, s3Endpoint string) (Target, error) {
	switch cfg.TargetKind {
	case "s3":
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("backup: load aws config: %w", err)
		}
		client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if s3Endpoint != "" {
				o.BaseEndpoint = aws.String(s3Endpoint)
				o.UsePathStyle = true // required for MinIO / most S3-compatibles
			}
		})
		return NewS3Target(client, cfg.S3Bucket, cfg.S3Prefix)
	default:
		return NewLocalTarget(cfg.Dir)
	}
}
