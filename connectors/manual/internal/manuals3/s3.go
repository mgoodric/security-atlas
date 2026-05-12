// Package manuals3 lists objects under an S3 prefix for the manual.upload
// connector's `s3` subcommand.
//
// Security posture:
//   - Credentials come from the standard AWS chain (env, profile, IMDS,
//     IRSA). The package never accepts a flag-passed access key.
//   - The package never logs credentials, the SDK config, or signed URLs.
//   - The prefix is required; empty prefix is rejected to prevent a
//     full-bucket scan by accident.
package manuals3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Object is the per-object payload List emits.
type Object struct {
	Bucket       string
	Key          string
	ETag         string // unquoted; AWS wraps it in literal " characters
	Size         int64
	LastModified time.Time
}

// API is the narrow surface this package consumes from the S3 client.
// The concrete *s3.Client satisfies it; tests inject fakes.
type API interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// List enumerates every object under (bucket, prefix), walking
// continuation tokens. Empty bucket or prefix is rejected — operators
// must opt into the prefix scope explicitly to avoid full-bucket scans.
func List(ctx context.Context, api API, bucket, prefix string) ([]Object, error) {
	if bucket == "" {
		return nil, errors.New("manuals3: bucket is required")
	}
	if prefix == "" {
		return nil, errors.New("manuals3: prefix is required (refusing to scan entire bucket)")
	}
	if api == nil {
		return nil, errors.New("manuals3: API is nil")
	}

	var (
		out   []Object
		token *string
	)
	for {
		resp, err := api.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("manuals3: list bucket=%q prefix=%q: %w", bucket, prefix, err)
		}
		for _, c := range resp.Contents {
			obj := Object{
				Bucket: bucket,
				Key:    aws.ToString(c.Key),
				ETag:   strings.Trim(aws.ToString(c.ETag), `"`),
				Size:   aws.ToInt64(c.Size),
			}
			if c.LastModified != nil {
				obj.LastModified = c.LastModified.UTC()
			}
			out = append(out, obj)
		}
		if !aws.ToBool(resp.IsTruncated) {
			break
		}
		token = resp.NextContinuationToken
	}
	return out, nil
}
