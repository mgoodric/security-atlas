package awss3_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awss3"
)

// fakeS3 implements awss3.API. Responses keyed on bucket name.
type fakeS3 struct {
	buckets    []string
	encryption map[string]*s3.GetBucketEncryptionOutput
	encErrors  map[string]error
}

func (f *fakeS3) ListBuckets(_ context.Context, _ *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	out := &s3.ListBucketsOutput{}
	for _, b := range f.buckets {
		out.Buckets = append(out.Buckets, s3types.Bucket{Name: aws.String(b)})
	}
	return out, nil
}

func (f *fakeS3) GetBucketEncryption(_ context.Context, in *s3.GetBucketEncryptionInput, _ ...func(*s3.Options)) (*s3.GetBucketEncryptionOutput, error) {
	name := aws.ToString(in.Bucket)
	if err, ok := f.encErrors[name]; ok {
		return nil, err
	}
	if out, ok := f.encryption[name]; ok {
		return out, nil
	}
	return nil, encryptionNotFound{}
}

// encryptionNotFound is a smithy.APIError with the magic code AWS returns
// for unencrypted buckets.
type encryptionNotFound struct{}

func (encryptionNotFound) Error() string                 { return "ServerSideEncryptionConfigurationNotFoundError" }
func (encryptionNotFound) ErrorCode() string             { return "ServerSideEncryptionConfigurationNotFoundError" }
func (encryptionNotFound) ErrorMessage() string          { return "no encryption configuration" }
func (encryptionNotFound) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = encryptionNotFound{}

func resolverConst(region string) awss3.RegionResolver {
	return func(context.Context, string) (string, error) { return region, nil }
}

func sameClient(api awss3.API) func(string) awss3.API {
	return func(string) awss3.API { return api }
}

func TestInspect_EncryptedBucketReturnsPass(t *testing.T) {
	t.Parallel()
	enc := &s3.GetBucketEncryptionOutput{
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{
				{
					ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
						SSEAlgorithm: s3types.ServerSideEncryptionAes256,
					},
				},
			},
		},
	}
	api := &fakeS3{
		buckets:    []string{"prod-vault"},
		encryption: map[string]*s3.GetBucketEncryptionOutput{"prod-vault": enc},
	}
	states, err := awss3.Inspect(context.Background(), api, resolverConst("us-east-1"), sameClient(api))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("got %d states; want 1", len(states))
	}
	s := states[0]
	if s.Result != awss3.ResultPass {
		t.Fatalf("result = %v; want pass", s.Result)
	}
	if s.Algorithm != "AES256" {
		t.Fatalf("algorithm = %q; want AES256", s.Algorithm)
	}
	if s.BucketARN != "arn:aws:s3:::prod-vault" {
		t.Fatalf("ARN = %q", s.BucketARN)
	}
}

func TestInspect_UnencryptedBucketReturnsFail(t *testing.T) {
	t.Parallel()
	api := &fakeS3{buckets: []string{"public-static"}}
	states, err := awss3.Inspect(context.Background(), api, resolverConst("us-west-2"), sameClient(api))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if states[0].Result != awss3.ResultFail {
		t.Fatalf("result = %v; want fail", states[0].Result)
	}
}

func TestInspect_TransportErrorReturnsInconclusive(t *testing.T) {
	t.Parallel()
	api := &fakeS3{
		buckets:   []string{"flaky"},
		encErrors: map[string]error{"flaky": errors.New("RequestTimeout: connection reset")},
	}
	states, err := awss3.Inspect(context.Background(), api, resolverConst("eu-west-1"), sameClient(api))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if states[0].Result != awss3.ResultInconclusive {
		t.Fatalf("result = %v; want inconclusive", states[0].Result)
	}
	if states[0].Reason == "" {
		t.Fatal("inconclusive reason should be populated")
	}
}

func TestInspect_RegionResolveErrorReturnsInconclusive(t *testing.T) {
	t.Parallel()
	api := &fakeS3{buckets: []string{"denied-region"}}
	failResolver := func(context.Context, string) (string, error) { return "", errors.New("AccessDenied: HeadBucket") }
	states, err := awss3.Inspect(context.Background(), api, failResolver, sameClient(api))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if states[0].Result != awss3.ResultInconclusive {
		t.Fatalf("result = %v; want inconclusive", states[0].Result)
	}
	if states[0].BucketRegion != "" {
		t.Fatalf("region should be empty when resolution fails; got %q", states[0].BucketRegion)
	}
}
