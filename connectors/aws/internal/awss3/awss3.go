// Package awss3 inspects S3 bucket encryption state, the load-bearing
// signal for the AWS connector's first evidence kind.
package awss3

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// EncryptionResult enumerates what the connector reports per bucket. Maps
// 1:1 onto the gRPC Result enum.
type EncryptionResult string

const (
	ResultPass         EncryptionResult = "pass"
	ResultFail         EncryptionResult = "fail"
	ResultInconclusive EncryptionResult = "inconclusive"
)

// BucketState is the per-bucket payload the connector emits.
type BucketState struct {
	BucketARN        string
	BucketName       string
	BucketRegion     string
	Result           EncryptionResult
	Algorithm        string // "AES256" | "aws:kms" | "" when fail/inconclusive
	KMSKeyID         string // populated when Algorithm == "aws:kms"
	BucketKeyEnabled bool
	Reason           string // human-readable inconclusive reason
}

// API is the narrow surface this package consumes from the S3 client.
type API interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketEncryption(ctx context.Context, params *s3.GetBucketEncryptionInput, optFns ...func(*s3.Options)) (*s3.GetBucketEncryptionOutput, error)
}

// RegionResolver returns the region a given bucket lives in.
// `feature/s3/manager.GetBucketRegion` is the canonical implementation; tests
// pass a fake.
type RegionResolver func(ctx context.Context, bucket string) (string, error)

// Inspect returns the encryption state for every visible bucket. region is
// the connector's primary region; per-bucket calls re-target via the
// resolver when the bucket lives elsewhere. clientFor returns an S3 API for
// the supplied region — typically `func(r string) API { return s3.NewFromConfig(cfg, withRegion(r)) }`.
func Inspect(ctx context.Context, api API, resolver RegionResolver, clientFor func(region string) API) ([]BucketState, error) {
	buckets, err := api.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("awss3: list buckets: %w", err)
	}
	out := make([]BucketState, 0, len(buckets.Buckets))
	for _, b := range buckets.Buckets {
		name := aws.ToString(b.Name)
		state := BucketState{
			BucketARN:  bucketARN(name),
			BucketName: name,
		}
		region, err := resolver(ctx, name)
		if err != nil {
			state.Result = ResultInconclusive
			state.Reason = fmt.Sprintf("resolve region: %v", err)
			out = append(out, state)
			continue
		}
		state.BucketRegion = region
		regionalAPI := clientFor(region)
		state = checkEncryption(ctx, regionalAPI, state)
		out = append(out, state)
	}
	return out, nil
}

func checkEncryption(ctx context.Context, api API, state BucketState) BucketState {
	out, err := api.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(state.BucketName)})
	if err != nil {
		if isEncryptionAbsent(err) {
			state.Result = ResultFail
			return state
		}
		state.Result = ResultInconclusive
		state.Reason = fmt.Sprintf("get-bucket-encryption: %v", err)
		return state
	}
	state = applyServerSideEncryption(state, out.ServerSideEncryptionConfiguration)
	if state.Result == "" {
		// Configuration present but no rules — treat as fail.
		state.Result = ResultFail
	}
	return state
}

func applyServerSideEncryption(state BucketState, conf *s3types.ServerSideEncryptionConfiguration) BucketState {
	if conf == nil {
		state.Result = ResultFail
		return state
	}
	for _, rule := range conf.Rules {
		state.BucketKeyEnabled = aws.ToBool(rule.BucketKeyEnabled)
		if rule.ApplyServerSideEncryptionByDefault == nil {
			continue
		}
		def := rule.ApplyServerSideEncryptionByDefault
		state.Algorithm = string(def.SSEAlgorithm)
		state.KMSKeyID = aws.ToString(def.KMSMasterKeyID)
		state.Result = ResultPass
		return state
	}
	return state
}

// isEncryptionAbsent matches the smithy APIError code AWS returns when a
// bucket has no encryption configuration. AWS doesn't expose this as a typed
// Go struct; the code string is the contract.
func isEncryptionAbsent(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorCode() == "ServerSideEncryptionConfigurationNotFoundError"
}

func bucketARN(name string) string {
	return "arn:aws:s3:::" + name
}
