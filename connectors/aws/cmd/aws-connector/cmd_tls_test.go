// #2993: `run --kind aws.s3.bucket_tls_enforcement.v1` reports whether each
// bucket policy denies requests not sent over TLS.
package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"google.golang.org/protobuf/encoding/protojson"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/aws/internal/awsauth"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/awss3"
	"github.com/mgoodric/security-atlas/connectors/aws/internal/s3tls"
)

var tlsIdentity = awsauth.Identity{AccountID: "111122223333", Environment: "prod", Source: "flag"}

// Every state the collector emits: enforced, enforced-not, no policy, and
// the two unread shapes.
var tlsStates = map[string]s3tls.BucketState{
	"pass": {BucketARN: "arn:aws:s3:::prod-vault", BucketName: "prod-vault", BucketRegion: "us-east-1",
		Result: s3tls.ResultPass, PolicyRead: true, PolicyPresent: true, DeniesInsecureTransport: true},
	"fail_policy_without_deny": {BucketARN: "arn:aws:s3:::prod-logs", BucketName: "prod-logs", BucketRegion: "eu-west-1",
		Result: s3tls.ResultFail, PolicyRead: true, PolicyPresent: true},
	"fail_no_policy": {BucketARN: "arn:aws:s3:::scratch", BucketName: "scratch", BucketRegion: "us-west-2",
		Result: s3tls.ResultFail, PolicyRead: true},
	"inconclusive_policy_unread": {BucketARN: "arn:aws:s3:::locked", BucketName: "locked", BucketRegion: "us-east-1",
		Result: s3tls.ResultInconclusive, Reason: "get-bucket-policy: AccessDenied"},
	"inconclusive_region_unresolved": {BucketARN: "arn:aws:s3:::hidden", BucketName: "hidden",
		Result: s3tls.ResultInconclusive, Reason: "resolve region: AccessDenied"},
}

func TestBuildTLSRecord_PayloadConformsToRegisteredSchema(t *testing.T) {
	t.Parallel()
	reg, err := schemaregistry.NewEmbedded(schemaregistry.PlatformSchemasFS())
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	for name, state := range tlsStates {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec, err := buildTLSRecord(state, tlsIdentity, "")
			if err != nil {
				t.Fatalf("buildTLSRecord: %v", err)
			}
			payloadJSON, err := protojson.Marshal(rec.GetPayload())
			if err != nil {
				t.Fatalf("protojson.Marshal: %v", err)
			}
			if err := reg.ValidatePayload(context.Background(), tenantA, rec.GetEvidenceKind(), rec.GetSchemaVersion(), payloadJSON); err != nil {
				t.Fatalf("ingest schema validation rejected the connector payload: %v", err)
			}
		})
	}
}

func TestBuildTLSRecord_Shape(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		result evidencev1.Result
		want   map[string]any
	}{
		"pass": {evidencev1.Result_RESULT_PASS, map[string]any{
			"bucket_name": "prod-vault", "account_id": "111122223333", "region": "us-east-1",
			"policy_present": true, "denies_insecure_transport": true,
		}},
		"fail_no_policy": {evidencev1.Result_RESULT_FAIL, map[string]any{
			"bucket_name": "scratch", "account_id": "111122223333", "region": "us-west-2",
			"policy_present": false, "denies_insecure_transport": false,
		}},
		// Unread: the two booleans are omitted, not false.
		"inconclusive_policy_unread": {evidencev1.Result_RESULT_INCONCLUSIVE, map[string]any{
			"bucket_name": "locked", "account_id": "111122223333", "region": "us-east-1",
			"reason": "get-bucket-policy: AccessDenied",
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec, err := buildTLSRecord(tlsStates[name], tlsIdentity, "")
			if err != nil {
				t.Fatalf("buildTLSRecord: %v", err)
			}
			if rec.EvidenceKind != TLSKind || rec.SchemaVersion != TLSSchemaVersion {
				t.Errorf("kind/version = %s/%s", rec.EvidenceKind, rec.SchemaVersion)
			}
			if rec.ControlId != "" {
				t.Errorf("ControlId = %q; want empty (routed by type)", rec.ControlId)
			}
			if rec.Result != tc.result {
				t.Errorf("Result = %v; want %v", rec.Result, tc.result)
			}
			if got := rec.GetPayload().AsMap(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Payload = %v; want %v", got, tc.want)
			}
		})
	}
}

// The ledger's idempotency keys are unique per tenant across kinds, so the
// TLS record must not reuse the encryption record's key for the same
// bucket and hour.
func TestBuildTLSRecord_IdempotencyKeyDiffersFromEncryption(t *testing.T) {
	t.Parallel()
	tls, err := buildTLSRecord(tlsStates["pass"], tlsIdentity, "")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := buildRecord(awss3.BucketState{BucketARN: "arn:aws:s3:::prod-vault", BucketName: "prod-vault",
		Result: awss3.ResultPass, Algorithm: "AES256"}, tlsIdentity, "")
	if err != nil {
		t.Fatal(err)
	}
	if tls.IdempotencyKey == enc.IdempotencyKey {
		t.Fatalf("TLS and encryption records share idempotency key %s", tls.IdempotencyKey)
	}
	again, err := buildTLSRecord(tlsStates["pass"], tlsIdentity, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.IdempotencyKey != tls.IdempotencyKey {
		t.Error("two TLS builds in one hour have different keys")
	}
}

type recordingClient struct{ records []*evidencev1.EvidenceRecord }

func (c *recordingClient) Push(_ context.Context, r *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	c.records = append(c.records, r)
	return &evidencev1.EvidenceReceipt{}, nil
}
func (c *recordingClient) Close() error { return nil }

func TestDoRun_TLSKindPushesTLSRecords(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	client := &recordingClient{}
	installSeams(t, seamOverrides{
		assume:    func(context.Context, string, string) (aws.Config, error) { return aws.Config{Region: "us-east-1"}, nil },
		resolveID: resolveIDNoop,
		inspect: func(context.Context, awss3.API, awss3.RegionResolver, func(string) awss3.API) ([]awss3.BucketState, error) {
			t.Error("encryption inspect called for the TLS kind")
			return nil, nil
		},
		newClient: func(string, string, ...sdk.Option) (sdkPushClient, error) { return client, nil },
	})
	prev := s3tlsInspect
	s3tlsInspect = func(context.Context, s3tls.API, s3tls.RegionResolver, func(string) s3tls.API) ([]s3tls.BucketState, error) {
		return []s3tls.BucketState{tlsStates["pass"], tlsStates["fail_no_policy"]}, nil
	}
	t.Cleanup(func() { s3tlsInspect = prev })

	f := okFlags()
	f.kind = TLSKind
	f.controlID = ""
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if len(client.records) != 2 {
		t.Fatalf("pushed %d records; want 2", len(client.records))
	}
	for _, r := range client.records {
		if r.EvidenceKind != TLSKind {
			t.Errorf("pushed kind %q; want %q", r.EvidenceKind, TLSKind)
		}
	}
}

func TestNewRunCmd_PreRunAcceptsTLSKind(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	cmd := newRunCmd()
	args := []string{"--kind", TLSKind, "--role-arn", "arn:aws:iam::111122223333:role/test", "--region", "us-east-1"}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if err := cmd.PreRunE(cmd, nil); err != nil {
		t.Fatalf("PreRunE: %v", err)
	}
}
