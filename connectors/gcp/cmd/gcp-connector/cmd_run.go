package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpapi"
	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpauth"
	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpcollect"
	"github.com/mgoodric/security-atlas/connectors/gcp/internal/idem"
)

// ReadOnlyScope is the single OAuth2 scope the connector requests for ADC.
// cloud-platform.read-only grants read access across the project's GCP APIs
// WITHOUT any write capability — the connector can never mutate a resource
// (slice 442 threat-model E / P0-442-2). It is NOT a data-plane scope: the
// IAM roles (gcpauth.RequiredRoles) gate what the read token can actually see,
// and they deliberately exclude object-data read.
const ReadOnlyScope = "https://www.googleapis.com/auth/cloud-platform.read-only"

// Package-level seams (slice 305 pattern): doRun reaches through these
// function variables so tests can swap in fakes for the GCP surfaces +
// the credential acquisition + the SDK push client without hitting a real
// GCP project or platform endpoint. Production code paths are unchanged;
// only the call-site indirection moved.
var (
	acquireToken = func(ctx context.Context) (string, error) {
		ts, err := google.DefaultTokenSource(ctx, ReadOnlyScope)
		if err != nil {
			return "", fmt.Errorf("gcp ADC: %w", err)
		}
		tok, err := ts.Token()
		if err != nil {
			return "", fmt.Errorf("gcp ADC token: %w", err)
		}
		return tok.AccessToken, nil
	}
	newGCPClient = func(cred gcpauth.Credential, projectID string, insecureTLS bool) gcpClient {
		return gcpapi.New(cred, projectID, insecureTLS)
	}
	authResolve        = gcpauth.Resolve
	collectIAMBindings = gcpcollect.CollectIAMBindings
	collectBuckets     = gcpcollect.CollectBuckets
	newSDKClient       = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
)

// gcpClient is the union of the narrow GCP surfaces doRun consumes. The real
// *gcpapi.Client satisfies it; tests pass a fake.
type gcpClient interface {
	gcpauth.IdentityAPI
	gcpcollect.IAMAPI
	gcpcollect.StorageAPI
}

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	projectID       string
	iamControlID    string
	bucketControlID string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "read the project IAM policy + Cloud Storage bucket config and push evidence",
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.projectID == "" {
				return fmt.Errorf("--project is required (the GCP project id to collect from)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.projectID, "project", "", "GCP project id to collect from [required]")
	cmd.Flags().StringVar(&f.iamControlID, "iam-control-id", "scf:IAC-21", "control id attached to each IAM-binding record")
	cmd.Flags().StringVar(&f.bucketControlID, "bucket-control-id", "scf:CRY-04", "control id attached to each storage-bucket record")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	rawToken, err := acquireToken(ctx)
	if err != nil {
		return err
	}
	cred, err := gcpauth.NewCredential(rawToken)
	if err != nil {
		return err
	}
	client := newGCPClient(cred, f.projectID, common.insecure)

	identity, err := authResolve(ctx, client)
	if err != nil {
		return err
	}

	bindings, err := collectIAMBindings(ctx, client)
	if err != nil {
		return fmt.Errorf("iam: %w", err)
	}
	buckets, err := collectBuckets(ctx, client)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}

	push, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = push.Close() }()

	now := time.Now().UTC().Truncate(time.Hour)
	pushed := 0

	for _, b := range bindings {
		rec, buildErr := buildIAMRecord(b, identity, f.iamControlID, now)
		if buildErr != nil {
			return fmt.Errorf("build iam record %s/%s: %w", b.Role, b.Member, buildErr)
		}
		if err := pushOne(ctx, push, rec); err != nil {
			return fmt.Errorf("push iam %s/%s: %w", b.Role, b.Member, err)
		}
		pushed++
	}

	for _, bkt := range buckets {
		rec, buildErr := buildBucketRecord(bkt, identity, f.bucketControlID, now)
		if buildErr != nil {
			return fmt.Errorf("build bucket record %s: %w", bkt.Name, buildErr)
		}
		if err := pushOne(ctx, push, rec); err != nil {
			return fmt.Errorf("push bucket %s: %w", bkt.Name, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (project=%s iam_bindings=%d buckets=%d profile=pull)\n",
		pushed, identity.ProjectID, len(bindings), len(buckets))
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, record *evidencev1.EvidenceRecord) error {
	ctxPush, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(ctxPush, record)
	return err
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

func buildIAMRecord(b gcpcollect.IAMBinding, identity gcpauth.Identity, controlID string, now time.Time) (*evidencev1.EvidenceRecord, error) {
	payload, err := structpb.NewStruct(map[string]any{
		"member":         b.Member,
		"member_type":    b.MemberType,
		"role":           b.Role,
		"is_privileged":  b.IsPrivileged,
		"is_service_acc": b.IsServiceAcc,
		"disabled":       b.Disabled,
		"reason":         b.Reason,
	})
	if err != nil {
		return nil, err
	}
	anchor := identity.ProjectID + "|" + b.Role + "|" + b.Member
	return &evidencev1.EvidenceRecord{
		IdempotencyKey:    idem.Key(anchor, now),
		EvidenceKind:      KindIAMBinding,
		SchemaVersion:     "1.0.0",
		ControlId:         controlID,
		Scope:             projectScope(identity),
		ObservedAt:        timestamppb.New(now),
		Result:            mapResult(b.Result),
		Payload:           payload,
		SourceAttribution: sourceAttribution("iam"),
	}, nil
}

func buildBucketRecord(bkt gcpcollect.StorageBucket, identity gcpauth.Identity, controlID string, now time.Time) (*evidencev1.EvidenceRecord, error) {
	payload, err := structpb.NewStruct(map[string]any{
		"bucket_name":          bkt.Name,
		"location":             bkt.Location,
		"default_kms_key_name": bkt.DefaultKMSKeyName,
		"uniform_access":       bkt.UniformAccess,
		"public_access":        bkt.PublicAccessFlag,
		"versioning_enabled":   bkt.VersioningEnabled,
		"retention_seconds":    float64(bkt.RetentionSeconds),
		"reason":               bkt.Reason,
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey:    idem.Key("bucket:"+bkt.Name, now),
		EvidenceKind:      KindStorageBucket,
		SchemaVersion:     "1.0.0",
		ControlId:         controlID,
		Scope:             projectScope(identity),
		ObservedAt:        timestamppb.New(now),
		Result:            mapResult(bkt.Result),
		Payload:           payload,
		SourceAttribution: sourceAttribution("storage"),
	}, nil
}

// projectScope is the minimum scope dimension every GCP record carries — the
// GCP project id (slice 004 scope-minimum convention; mirrors awsauth's
// cloud_account + slackauth's tenant_workspace). Org/folder is a follow-on.
func projectScope(identity gcpauth.Identity) []*evidencev1.ScopeDimension {
	return []*evidencev1.ScopeDimension{
		{Key: "cloud_project", Values: []string{"gcp:" + identity.ProjectID}},
	}
}

func sourceAttribution(service string) *evidencev1.SourceAttribution {
	return &evidencev1.SourceAttribution{
		ActorType: "connector",
		ActorId:   actorID(service),
		// SessionId intentionally left empty: a per-call UUID would change the
		// record's canonical hash between dedup retries (slice 004 pattern).
	}
}

func mapResult(r gcpcollect.Result) evidencev1.Result {
	switch r {
	case gcpcollect.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case gcpcollect.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case gcpcollect.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}
