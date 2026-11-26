package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awsauth"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/awss3"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/s3tls"
)

// Package-level seams (slice 305): doRun reaches through these function
// variables so tests can swap in fakes for awsauth + awss3 + sdk without
// hitting real AWS or a real platform endpoint. Production code paths are
// byte-for-byte unchanged; only the call-site indirection moved.
var (
	awsauthAssume    = awsauth.Assume
	awsauthResolveID = awsauth.ResolveIdentity
	awss3Inspect     = awss3.Inspect
	s3tlsInspect     = s3tls.Inspect
	newSDKClient     = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
// Decoupling here lets tests pass a fake without constructing a real
// grpc.ClientConn. *sdk.Client satisfies this interface today.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	kind        string
	roleARN     string
	region      string
	environment string
	controlID   string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "assume the role, query AWS, push evidence records",
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if !slices.Contains(SupportedKinds, f.kind) {
				return fmt.Errorf("--kind must be one of %q", SupportedKinds)
			}
			if f.roleARN == "" {
				return fmt.Errorf("--role-arn is required (no static access keys supported)")
			}
			if f.region == "" {
				return fmt.Errorf("--region is required")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.kind, "kind", SupportedKind, "evidence kind to pull: "+SupportedKind+" or "+TLSKind)
	cmd.Flags().StringVar(&f.roleARN, "role-arn", "", "IAM role ARN to assume [required]")
	cmd.Flags().StringVar(&f.region, "region", "", "primary AWS region [required]")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag; fallback when Organizations:DescribeAccount is not available")
	cmd.Flags().StringVar(&f.controlID, "control-id", "", "control_id to attach to each record as provenance; empty (the default) routes each record to every control that queries its kind (#2924)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cfg, err := awsauthAssume(ctx, f.roleARN, f.region)
	if err != nil {
		return err
	}
	identity, err := awsauthResolveID(ctx, awsauth.STSClient(cfg), awsauth.OrgClient(cfg), f.environment)
	if err != nil {
		return err
	}

	s3Client := s3.NewFromConfig(cfg)
	resolver := func(ctx context.Context, bucket string) (string, error) {
		return manager.GetBucketRegion(ctx, s3Client, bucket)
	}
	clientFor := func(region string) awss3.API {
		return s3.NewFromConfig(cfg, func(o *s3.Options) { o.Region = region })
	}
	var records []bucketRecord
	if f.kind == TLSKind {
		tlsClientFor := func(region string) s3tls.API {
			return s3.NewFromConfig(cfg, func(o *s3.Options) { o.Region = region })
		}
		states, err := s3tlsInspect(ctx, s3Client, s3tls.RegionResolver(resolver), tlsClientFor)
		if err != nil {
			return fmt.Errorf("inspect: %w", err)
		}
		for _, state := range states {
			record, buildErr := buildTLSRecord(state, identity, f.controlID)
			if buildErr != nil {
				return fmt.Errorf("build record for %s: %w", state.BucketName, buildErr)
			}
			records = append(records, bucketRecord{state.BucketName, record})
		}
	} else {
		states, err := awss3Inspect(ctx, s3Client, resolver, clientFor)
		if err != nil {
			return fmt.Errorf("inspect: %w", err)
		}
		for _, state := range states {
			record, buildErr := buildRecord(state, identity, f.controlID)
			if buildErr != nil {
				return fmt.Errorf("build record for %s: %w", state.BucketName, buildErr)
			}
			records = append(records, bucketRecord{state.BucketName, record})
		}
	}

	client, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = client.Close() }()

	pushed := 0
	for _, r := range records {
		ctxPush, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := client.Push(ctxPush, r.record)
		cancel()
		if err != nil {
			return fmt.Errorf("push %s: %w", r.bucket, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records (account=%s environment=%s source=%s)\n",
		pushed, identity.AccountID, identity.Environment, identity.Source)
	return nil
}

// bucketRecord is one record ready to push, with the bucket it describes.
type bucketRecord struct {
	bucket string
	record *evidencev1.EvidenceRecord
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

func buildRecord(state awss3.BucketState, identity awsauth.Identity, controlID string) (*evidencev1.EvidenceRecord, error) {
	// Truncate observed_at to the hour so two runs within the same hour
	// produce byte-identical records. The idempotency_key also keys on
	// hour (see idem.Key); without this truncation, ObservedAt drift in
	// sub-second precision would force hash mismatch on dedup.
	now := time.Now().UTC().Truncate(time.Hour)
	// Keys follow the registered schema (#2815); ingest rejects any other
	// key. Empty strings are omitted rather than sent as "".
	fields := map[string]any{
		"bucket_name":        state.BucketName,
		"encryption_enabled": state.Result == awss3.ResultPass,
		"bucket_key_enabled": state.BucketKeyEnabled,
		"account_id":         identity.AccountID,
	}
	for key, value := range map[string]string{
		"region":        state.BucketRegion,
		"sse_algorithm": state.Algorithm,
		"kms_key_id":    state.KMSKeyID,
		"reason":        state.Reason,
	} {
		if value != "" {
			fields[key] = value
		}
	}
	payload, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.Key(state.BucketARN, now),
		EvidenceKind:   SupportedKind,
		SchemaVersion:  SchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"aws:" + identity.AccountID}},
			{Key: "environment", Values: []string{identity.Environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapResult(state.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID(),
			// SessionId intentionally left empty: a per-call UUID would make
			// the record's canonical hash differ between dedup retries,
			// turning AC-6 idempotency into AlreadyExists errors.
		},
	}, nil
}

// buildTLSRecord builds one TLSKind record. policy_present and
// denies_insecure_transport are set only when the connector read the
// policy or found it absent; an unread bucket omits both, so a missing
// value means "not observed", never false.
func buildTLSRecord(state s3tls.BucketState, identity awsauth.Identity, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := time.Now().UTC().Truncate(time.Hour)
	fields := map[string]any{
		"bucket_name": state.BucketName,
		"account_id":  identity.AccountID,
	}
	if state.PolicyRead {
		fields["policy_present"] = state.PolicyPresent
		fields["denies_insecure_transport"] = state.DeniesInsecureTransport
	}
	for key, value := range map[string]string{
		"region": state.BucketRegion,
		"reason": state.Reason,
	} {
		if value != "" {
			fields[key] = value
		}
	}
	payload, err := structpb.NewStruct(fields)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		// The kind is part of the key: idempotency keys are unique per
		// tenant across kinds, so the bucket ARN alone would collide with
		// the same hour's encryption record and be deduplicated into it.
		IdempotencyKey: idem.Key(TLSKind+"|"+state.BucketARN, now),
		EvidenceKind:   TLSKind,
		SchemaVersion:  TLSSchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "cloud_account", Values: []string{"aws:" + identity.AccountID}},
			{Key: "environment", Values: []string{identity.Environment}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapTLSResult(state.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID(),
		},
	}, nil
}

func mapTLSResult(r s3tls.Result) evidencev1.Result {
	switch r {
	case s3tls.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case s3tls.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case s3tls.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

func mapResult(r awss3.EncryptionResult) evidencev1.Result {
	switch r {
	case awss3.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case awss3.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case awss3.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}
