package main

import (
	"context"
	"fmt"
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
)

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
			if f.kind != SupportedKind {
				return fmt.Errorf("--kind must be %q (slice 004 supports one kind)", SupportedKind)
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
	cmd.Flags().StringVar(&f.kind, "kind", SupportedKind, "evidence kind to pull")
	cmd.Flags().StringVar(&f.roleARN, "role-arn", "", "IAM role ARN to assume [required]")
	cmd.Flags().StringVar(&f.region, "region", "", "primary AWS region [required]")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag; fallback when Organizations:DescribeAccount is not available")
	cmd.Flags().StringVar(&f.controlID, "control-id", "scf:CRY-04", "control id to attach to each record")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cfg, err := awsauth.Assume(ctx, f.roleARN, f.region)
	if err != nil {
		return err
	}
	identity, err := awsauth.ResolveIdentity(ctx, awsauth.STSClient(cfg), awsauth.OrgClient(cfg), f.environment)
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
	states, err := awss3.Inspect(ctx, s3Client, resolver, clientFor)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}

	client, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = client.Close() }()

	pushed := 0
	for _, state := range states {
		record, buildErr := buildRecord(state, identity, f.controlID)
		if buildErr != nil {
			return fmt.Errorf("build record for %s: %w", state.BucketName, buildErr)
		}
		ctxPush, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := client.Push(ctxPush, record)
		cancel()
		if err != nil {
			return fmt.Errorf("push %s: %w", state.BucketName, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records (account=%s environment=%s source=%s)\n",
		pushed, identity.AccountID, identity.Environment, identity.Source)
	return nil
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
	payload, err := structpb.NewStruct(map[string]any{
		"bucket_arn":         state.BucketARN,
		"bucket_name":        state.BucketName,
		"bucket_region":      state.BucketRegion,
		"algorithm":          state.Algorithm,
		"kms_key_id":         state.KMSKeyID,
		"bucket_key_enabled": state.BucketKeyEnabled,
		"reason":             state.Reason,
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.Key(state.BucketARN, now),
		EvidenceKind:   SupportedKind,
		SchemaVersion:  "1.0.0",
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
