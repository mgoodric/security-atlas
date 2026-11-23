// Package s3tls reports whether each S3 bucket's policy denies requests
// not sent over TLS: a Deny on every principal, for every S3 action on the
// bucket and its objects, conditioned on aws:SecureTransport being false
// (#2993). That is the statement AWS documents under "Restrict access to
// only HTTPS requests" in the S3 user guide's bucket-policy examples.
package s3tls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// Result is the connector's verdict for one bucket.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// BucketState is one bucket's observation. PolicyRead is false when the
// policy could not be read; PolicyPresent and DeniesInsecureTransport mean
// nothing then, and the record omits them.
type BucketState struct {
	BucketARN               string
	BucketName              string
	BucketRegion            string
	Result                  Result
	PolicyRead              bool
	PolicyPresent           bool
	DeniesInsecureTransport bool
	Reason                  string
}

// API is the narrow surface this package consumes from the S3 client.
type API interface {
	ListBuckets(ctx context.Context, params *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketPolicy(ctx context.Context, params *s3.GetBucketPolicyInput, optFns ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error)
}

// RegionResolver returns the region a bucket lives in.
type RegionResolver func(ctx context.Context, bucket string) (string, error)

// Inspect returns the TLS-enforcement state of every visible bucket.
// Per-bucket calls go to the client clientFor returns for the bucket's
// region.
func Inspect(ctx context.Context, api API, resolver RegionResolver, clientFor func(region string) API) ([]BucketState, error) {
	buckets, err := api.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, fmt.Errorf("s3tls: list buckets: %w", err)
	}
	out := make([]BucketState, 0, len(buckets.Buckets))
	for _, b := range buckets.Buckets {
		name := aws.ToString(b.Name)
		state := BucketState{BucketARN: bucketARN(name), BucketName: name}
		region, err := resolver(ctx, name)
		if err != nil {
			state.Result = ResultInconclusive
			state.Reason = fmt.Sprintf("resolve region: %v", err)
			out = append(out, state)
			continue
		}
		state.BucketRegion = region
		out = append(out, checkPolicy(ctx, clientFor(region), state))
	}
	return out, nil
}

func checkPolicy(ctx context.Context, api API, state BucketState) BucketState {
	out, err := api.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(state.BucketName)})
	if err != nil {
		if isPolicyAbsent(err) {
			state.Result = ResultFail
			state.PolicyRead = true
			return state
		}
		state.Result = ResultInconclusive
		state.Reason = fmt.Sprintf("get-bucket-policy: %v", err)
		return state
	}
	denies, err := DeniesInsecureTransport(aws.ToString(out.Policy), state.BucketName)
	if err != nil {
		state.Result = ResultInconclusive
		state.Reason = fmt.Sprintf("parse bucket policy: %v", err)
		return state
	}
	state.PolicyRead = true
	state.PolicyPresent = true
	state.DeniesInsecureTransport = denies
	if denies {
		state.Result = ResultPass
	} else {
		state.Result = ResultFail
	}
	return state
}

// DeniesInsecureTransport reports whether policy, a bucket policy JSON
// document, denies non-TLS requests to bucket and to its objects. It counts
// only statements of the documented shape, and reads anything it does not
// recognise as not denying, so a policy it misreads fails rather than
// passes. A statement counts when it:
//
//   - has Effect "Deny" and no NotPrincipal, NotAction or NotResource;
//   - has Principal "*" or {"AWS": "*"} (equivalent for anonymous users,
//     per the IAM Principal reference);
//   - lists "s3:*" or "*" among its actions;
//   - has exactly one condition, Bool on aws:SecureTransport, whose values
//     include "false". A second condition narrows the deny, so it does not
//     count.
//
// The counting statements' resources together must name both the bucket
// ARN and its objects ("arn:aws:s3:::<bucket>/*"), each exactly or as "*".
// An error means the document is not valid JSON or has no statement list.
func DeniesInsecureTransport(policy, bucket string) (bool, error) {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		return false, err
	}
	statements, err := statementList(doc.Statement)
	if err != nil {
		return false, err
	}
	bucketRes, objectRes := bucketARN(bucket), bucketARN(bucket)+"/*"
	var coversBucket, coversObjects bool
	for _, st := range statements {
		if !deniesInsecure(st) {
			continue
		}
		resources := stringList(st["Resource"])
		coversBucket = coversBucket || slices.Contains(resources, bucketRes) || slices.Contains(resources, "*")
		coversObjects = coversObjects || slices.Contains(resources, objectRes) || slices.Contains(resources, "*")
	}
	return coversBucket && coversObjects, nil
}

// statementList accepts Statement as one object or an array of objects,
// both of which the IAM policy grammar allows.
func statementList(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return nil, errors.New("policy has no Statement")
	}
	var many []map[string]any
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	var one map[string]any
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, fmt.Errorf("policy Statement is neither an object nor a list of objects: %w", err)
	}
	return []map[string]any{one}, nil
}

func deniesInsecure(st map[string]any) bool {
	if st["Effect"] != "Deny" {
		return false
	}
	for _, negated := range []string{"NotPrincipal", "NotAction", "NotResource"} {
		if _, ok := st[negated]; ok {
			return false
		}
	}
	if !everyPrincipal(st["Principal"]) {
		return false
	}
	actions := stringList(st["Action"])
	if !slices.Contains(actions, "s3:*") && !slices.Contains(actions, "*") {
		return false
	}
	cond, ok := st["Condition"].(map[string]any)
	if !ok || len(cond) != 1 {
		return false
	}
	boolCond, ok := cond["Bool"].(map[string]any)
	if !ok || len(boolCond) != 1 {
		return false
	}
	return slices.Contains(stringList(boolCond["aws:SecureTransport"]), "false")
}

func everyPrincipal(p any) bool {
	switch v := p.(type) {
	case string:
		return v == "*"
	case map[string]any:
		if len(v) != 1 {
			return false
		}
		return slices.Equal(stringList(v["AWS"]), []string{"*"})
	}
	return false
}

// stringList reads a policy element that may be a string or a list of
// strings. Anything else is an empty list.
func stringList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// isPolicyAbsent matches the error code S3 returns for a bucket with no
// policy ("NoSuchBucketPolicy: The specified bucket does not have a bucket
// policy", per the S3 error-code list in the SDK's types package).
func isPolicyAbsent(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.ErrorCode() == "NoSuchBucketPolicy"
}

func bucketARN(name string) string {
	return "arn:aws:s3:::" + name
}
