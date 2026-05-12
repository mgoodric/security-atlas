package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/manual/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/manual/internal/manuals3"
)

type s3Flags struct {
	bucket    string
	prefix    string
	region    string
	controlID string
	scope     []string
}

func newS3Cmd() *cobra.Command {
	var f s3Flags
	cmd := &cobra.Command{
		Use:           "s3",
		Short:         "list an S3 prefix; emit one manual.upload.v1 record per object",
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.bucket == "" {
				return errors.New("--bucket is required")
			}
			if f.prefix == "" {
				return errors.New("--prefix is required (refusing to scan entire bucket)")
			}
			if len(f.scope) == 0 {
				return errors.New("at least one --scope key=value pair is required")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doS3(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.bucket, "bucket", "", "S3 bucket [required]")
	cmd.Flags().StringVar(&f.prefix, "prefix", "", "S3 key prefix [required]")
	cmd.Flags().StringVar(&f.region, "region", "", "AWS region (optional; falls back to standard chain)")
	cmd.Flags().StringVar(&f.controlID, "control-id", "scf:GOV-04", "control id to attach to each record")
	cmd.Flags().StringArrayVar(&f.scope, "scope", nil, "scope tag in key=value form (repeatable, at least one required)")
	return cmd
}

func doS3(ctx context.Context, f s3Flags) error {
	// Standard AWS credential chain: env > shared profile > IRSA > IMDS.
	// No static access keys are ever accepted from a flag.
	var cfgOpts []func(*config.LoadOptions) error
	if f.region != "" {
		cfgOpts = append(cfgOpts, config.WithRegion(f.region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return fmt.Errorf("aws config: %w", err)
	}
	api := s3.NewFromConfig(cfg)

	objs, err := manuals3.List(ctx, api, f.bucket, f.prefix)
	if err != nil {
		return err
	}

	scope, err := parseScope(f.scope)
	if err != nil {
		return err
	}

	client, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = client.Close() }()

	now := time.Now().UTC().Truncate(time.Hour)
	pushed := 0
	for _, obj := range objs {
		rec, err := buildS3Record(obj, f.controlID, scope, now)
		if err != nil {
			return fmt.Errorf("build record key=%q: %w", obj.Key, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = client.Push(pctx, rec)
		cancel()
		if err != nil {
			return fmt.Errorf("push key=%q: %w", obj.Key, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records (bucket=%s prefix=%s)\n", pushed, f.bucket, f.prefix)
	return nil
}

func buildS3Record(obj manuals3.Object, controlID string, scope []*evidencev1.ScopeDimension, observedAt time.Time) (*evidencev1.EvidenceRecord, error) {
	payload, err := structpb.NewStruct(map[string]any{
		"uploaded_by":   actorID("s3"),
		"filename":      obj.Key,
		"content_type":  "application/octet-stream",
		"size_bytes":    float64(obj.Size),
		"description":   fmt.Sprintf("s3://%s/%s", obj.Bucket, obj.Key),
		"bucket":        obj.Bucket,
		"etag":          obj.ETag,
		"last_modified": obj.LastModified.Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.S3ObjectKey(obj.Bucket, obj.Key, obj.ETag),
		EvidenceKind:   "manual.upload.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope:          scope,
		ObservedAt:     timestamppb.New(observedAt),
		Result:         evidencev1.Result_RESULT_INCONCLUSIVE,
		Payload:        payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("s3"),
		},
	}, nil
}
