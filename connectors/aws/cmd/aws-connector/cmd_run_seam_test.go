// Unit tests for the doRun seams introduced by slice 305. Slice 299
// took the package from 9.0% → 65.8% by covering everything reachable
// without refactoring; the remaining gap sat entirely in doRun's
// post-ResolveIdentity body (the S3-inspect call and the push loop).
//
// Load-bearing branches this file covers in cmd_run.go's doRun:
//
//   - awsauthAssume returns error → doRun returns it verbatim (no
//     wrap; consistent with the slice-299 cancelled-context test).
//   - awsauthAssume ok, awsauthResolveID returns error → doRun
//     returns it verbatim.
//   - Assume + Resolve ok, awss3Inspect returns error → doRun wraps
//     it as "inspect: %w".
//   - Inspect ok but newSDKClient returns error → doRun wraps it as
//     "sdk client: %w".
//   - Inspect ok, sdk client ok, push succeeds → doRun returns nil
//     and prints the "pushed N records" line; pushed count == len(states).
//   - Inspect ok, sdk client ok, FIRST push errors → doRun returns
//     wrapped "push <bucket>: %w" and stops.
//   - Push-success branch with multiple states drives both iterations
//     of the for-loop, including buildRecord on a non-trivial state.
//
// Test seams are restored via t.Cleanup so the package-level vars are
// left untouched between tests. No vendor-prefixed tokens appear in
// fixtures — neutral "test-*" strings only.
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awsauth"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/awss3"
)

// fakeSDKClient is a minimal sdkPushClient. Push returns the queued
// error/receipt for each call so tests can drive success + failure
// branches.
type fakeSDKClient struct {
	pushErr     error
	pushed      int
	closeCalled bool
}

func (f *fakeSDKClient) Push(_ context.Context, _ *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	f.pushed++
	return &evidencev1.EvidenceReceipt{}, nil
}

func (f *fakeSDKClient) Close() error {
	f.closeCalled = true
	return nil
}

// installSeams swaps the package-level seams for the duration of the
// test. Returns a handle to the fake sdk client when one was installed
// (nil otherwise).
type seamOverrides struct {
	assume    func(ctx context.Context, roleARN, region string) (aws.Config, error)
	resolveID func(ctx context.Context, stsAPI awsauth.STSAPI, orgAPI awsauth.OrgAPI, envFlag string) (awsauth.Identity, error)
	inspect   func(ctx context.Context, api awss3.API, resolver awss3.RegionResolver, clientFor func(region string) awss3.API) ([]awss3.BucketState, error)
	newClient func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error)
}

func installSeams(t *testing.T, o seamOverrides) {
	t.Helper()
	if o.assume != nil {
		prev := awsauthAssume
		awsauthAssume = o.assume
		t.Cleanup(func() { awsauthAssume = prev })
	}
	if o.resolveID != nil {
		prev := awsauthResolveID
		awsauthResolveID = o.resolveID
		t.Cleanup(func() { awsauthResolveID = prev })
	}
	if o.inspect != nil {
		prev := awss3Inspect
		awss3Inspect = o.inspect
		t.Cleanup(func() { awss3Inspect = prev })
	}
	if o.newClient != nil {
		prev := newSDKClient
		newSDKClient = o.newClient
		t.Cleanup(func() { newSDKClient = prev })
	}
}

// okFlags returns runFlags wired with valid values; tests pass a copy.
func okFlags() runFlags {
	return runFlags{
		kind:        SupportedKind,
		roleARN:     "arn:aws:iam::111122223333:role/test",
		region:      "us-east-1",
		environment: "test",
		controlID:   "scf:CRY-04",
	}
}

// resolveIDNoop is a seam-friendly resolver that returns a fixed
// Identity without touching STS/Organizations.
func resolveIDNoop(_ context.Context, _ awsauth.STSAPI, _ awsauth.OrgAPI, env string) (awsauth.Identity, error) {
	return awsauth.Identity{AccountID: "111122223333", Environment: env, Source: "flag"}, nil
}

// assumeNoop returns a zero aws.Config; downstream awsauth.STSClient/
// OrgClient still get called but their outputs go straight back into
// the seamed resolveID (which ignores them).
func assumeNoop(_ context.Context, _, _ string) (aws.Config, error) {
	return aws.Config{Region: "us-east-1"}, nil
}

// TestDoRun_AssumeError: awsauthAssume returns an error; doRun returns
// it verbatim (not wrapped).
func TestDoRun_AssumeError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: assume failed")
	installSeams(t, seamOverrides{
		assume: func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, sentinel },
	})

	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) {
		t.Fatalf("doRun err = %v; want sentinel chain", err)
	}
}

// TestDoRun_ResolveIdentityError: awsauthAssume succeeds, but
// awsauthResolveID returns an error; doRun returns it verbatim.
func TestDoRun_ResolveIdentityError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: resolve failed")
	installSeams(t, seamOverrides{
		assume: assumeNoop,
		resolveID: func(_ context.Context, _ awsauth.STSAPI, _ awsauth.OrgAPI, _ string) (awsauth.Identity, error) {
			return awsauth.Identity{}, sentinel
		},
	})

	err := doRun(context.Background(), okFlags())
	if !errors.Is(err, sentinel) {
		t.Fatalf("doRun err = %v; want sentinel chain", err)
	}
}

// TestDoRun_InspectError: assume + resolve succeed; awss3Inspect
// errors. doRun wraps the error with "inspect: " prefix.
func TestDoRun_InspectError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: list buckets refused")
	installSeams(t, seamOverrides{
		assume:    assumeNoop,
		resolveID: resolveIDNoop,
		inspect: func(_ context.Context, _ awss3.API, _ awss3.RegionResolver, _ func(string) awss3.API) ([]awss3.BucketState, error) {
			return nil, sentinel
		},
	})

	err := doRun(context.Background(), okFlags())
	if err == nil {
		t.Fatal("doRun: want error from Inspect")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "inspect: ") {
		t.Errorf("doRun err = %q; want 'inspect: ' prefix", err.Error())
	}
}

// TestDoRun_SDKClientError: assume + resolve + inspect succeed; the
// sdk client constructor errors. doRun wraps with "sdk client: ".
func TestDoRun_SDKClientError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: bad endpoint")
	installSeams(t, seamOverrides{
		assume:    assumeNoop,
		resolveID: resolveIDNoop,
		inspect: func(_ context.Context, _ awss3.API, _ awss3.RegionResolver, _ func(string) awss3.API) ([]awss3.BucketState, error) {
			return []awss3.BucketState{
				{BucketARN: "arn:aws:s3:::bucket-a", BucketName: "bucket-a", BucketRegion: "us-east-1", Result: awss3.ResultPass, Algorithm: "AES256"},
			}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) {
			return nil, sentinel
		},
	})

	err := doRun(context.Background(), okFlags())
	if err == nil {
		t.Fatal("doRun: want error from newSDKClient")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "sdk client: ") {
		t.Errorf("doRun err = %q; want 'sdk client: ' prefix", err.Error())
	}
}

// TestDoRun_PushSuccess: full happy path through doRun. Two bucket
// states drive two iterations of the for-loop; the fake sdk client
// records two Push calls; doRun returns nil.
func TestDoRun_PushSuccess(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	fakeClient := &fakeSDKClient{}
	installSeams(t, seamOverrides{
		assume:    assumeNoop,
		resolveID: resolveIDNoop,
		inspect: func(_ context.Context, _ awss3.API, _ awss3.RegionResolver, _ func(string) awss3.API) ([]awss3.BucketState, error) {
			return []awss3.BucketState{
				{BucketARN: "arn:aws:s3:::bucket-a", BucketName: "bucket-a", BucketRegion: "us-east-1", Result: awss3.ResultPass, Algorithm: "AES256"},
				{BucketARN: "arn:aws:s3:::bucket-b", BucketName: "bucket-b", BucketRegion: "us-west-2", Result: awss3.ResultFail, Reason: "no SSE config"},
			}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	if err := doRun(context.Background(), okFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if fakeClient.pushed != 2 {
		t.Errorf("pushed = %d; want 2", fakeClient.pushed)
	}
	if !fakeClient.closeCalled {
		t.Error("Close not called via defer")
	}
}

// TestDoRun_PushError: first Push fails; doRun wraps "push <bucket>: "
// and stops without attempting the second record.
func TestDoRun_PushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	sentinel := errors.New("sentinel: push rejected")
	fakeClient := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seamOverrides{
		assume:    assumeNoop,
		resolveID: resolveIDNoop,
		inspect: func(_ context.Context, _ awss3.API, _ awss3.RegionResolver, _ func(string) awss3.API) ([]awss3.BucketState, error) {
			return []awss3.BucketState{
				{BucketARN: "arn:aws:s3:::bucket-a", BucketName: "bucket-a", BucketRegion: "us-east-1", Result: awss3.ResultPass, Algorithm: "AES256"},
				{BucketARN: "arn:aws:s3:::bucket-b", BucketName: "bucket-b", BucketRegion: "us-west-2", Result: awss3.ResultPass, Algorithm: "AES256"},
			}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return fakeClient, nil },
	})

	err := doRun(context.Background(), okFlags())
	if err == nil {
		t.Fatal("doRun: want push error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("doRun err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push bucket-a: ") {
		t.Errorf("doRun err = %q; want 'push bucket-a: ' prefix", err.Error())
	}
	if fakeClient.pushed != 0 {
		t.Errorf("pushed = %d; want 0 (push errored before counter incremented)", fakeClient.pushed)
	}
}

// TestBuildRecord_StablePayloadShape pins the canonical EvidenceRecord
// shape buildRecord emits for a representative BucketState. Covers the
// non-error branch of buildRecord (structpb.NewStruct succeeds for the
// usual scalar/string payload). The error branch is theoretically
// unreachable because every field passed to structpb.NewStruct is a
// supported scalar — Go's type system precludes a runtime error here —
// so we cover the success branch and leave the error branch documented.
func TestBuildRecord_StablePayloadShape(t *testing.T) {
	state := awss3.BucketState{
		BucketARN:        "arn:aws:s3:::bucket-x",
		BucketName:       "bucket-x",
		BucketRegion:     "us-east-1",
		Result:           awss3.ResultPass,
		Algorithm:        "aws:kms",
		KMSKeyID:         "alias/test-key",
		BucketKeyEnabled: true,
		Reason:           "",
	}
	identity := awsauth.Identity{AccountID: "111122223333", Environment: "prod", Source: "flag"}
	rec, err := buildRecord(state, identity, "scf:CRY-04")
	if err != nil {
		t.Fatalf("buildRecord: %v", err)
	}
	if rec.EvidenceKind != SupportedKind {
		t.Errorf("EvidenceKind = %q; want %q", rec.EvidenceKind, SupportedKind)
	}
	if rec.ControlId != "scf:CRY-04" {
		t.Errorf("ControlId = %q; want scf:CRY-04", rec.ControlId)
	}
	if rec.IdempotencyKey == "" {
		t.Error("IdempotencyKey empty")
	}
	if rec.SourceAttribution == nil || rec.SourceAttribution.ActorType != "connector" {
		t.Errorf("SourceAttribution wrong: %+v", rec.SourceAttribution)
	}
	if !strings.HasPrefix(rec.SourceAttribution.ActorId, "connector:aws:s3@") {
		t.Errorf("ActorId = %q; want connector:aws:s3@ prefix", rec.SourceAttribution.ActorId)
	}
	if rec.Result != evidencev1.Result_RESULT_PASS {
		t.Errorf("Result = %v; want RESULT_PASS", rec.Result)
	}
	if rec.Payload == nil || rec.Payload.Fields["bucket_arn"] == nil {
		t.Error("Payload missing bucket_arn field")
	}
}
