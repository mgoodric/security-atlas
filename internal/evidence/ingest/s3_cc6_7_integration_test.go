//go:build integration

// #2825: cc6_7 reads the AWS connector's S3 bucket-encryption records. On
// default flags the connector pushes them with no control_id (#2924), so
// they reach cc6_7 only by evidence type, and ingest validates each payload
// against the registered 1.1.0 schema (#2815). These tests push payloads in
// the connector's shape through ingest.Service.Process and evaluate the
// stock bundle with the live engine, so the SQL check is shown to decide on
// what the connector actually lands in the ledger.

package ingest_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
)

func s3EncryptionRecord(t *testing.T, result evidencev1.Result, fields map[string]any) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return record(t, func(r *evidencev1.EvidenceRecord) {
		r.EvidenceKind = "aws.s3.bucket_encryption_state.v1"
		r.SchemaVersion = "1.1.0"
		r.ControlId = ""
		r.Result = result
		r.Payload = payload
	})
}

func TestCC67_RoutedS3RecordsDecideDefaultEncryption(t *testing.T) {
	const bundle = "soc2_cc6_7_encryption_in_transit"
	bucket := func(name string, enc bool, alg string) map[string]any {
		p := map[string]any{
			"bucket_name":        name,
			"encryption_enabled": enc,
			"bucket_key_enabled": false,
			"account_id":         "111122223333",
			"region":             "us-east-1",
		}
		if alg != "" {
			p["sse_algorithm"] = alg
		}
		return p
	}
	type push struct {
		result evidencev1.Result
		fields map[string]any
	}
	pass, fail, inconclusive := evidencev1.Result_RESULT_PASS, evidencev1.Result_RESULT_FAIL, evidencev1.Result_RESULT_INCONCLUSIVE
	unread := bucket("acme-restricted", false, "")
	unread["reason"] = "get-bucket-encryption: AccessDenied"
	cases := []struct {
		name   string
		pushes []push
		want   string
	}{
		{"AES256", []push{{pass, bucket("acme-logs", true, "AES256")}}, eval.ResultPass},
		{"aws:kms", []push{{pass, bucket("acme-data", true, "aws:kms")}}, eval.ResultPass},
		{"disallowed algorithm", []push{{pass, bucket("acme-logs", true, "AES256")}, {pass, bucket("acme-fsx", true, "aws:fsx")}}, eval.ResultFail},
		{"no default encryption", []push{{pass, bucket("acme-logs", true, "AES256")}, {fail, bucket("acme-legacy", false, "")}}, eval.ResultFail},
		{"unread bucket", []push{{pass, bucket("acme-logs", true, "AES256")}, {inconclusive, unread}}, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, app, _ := boot(t)
			admin := dbtest.NewMigratePool(t)
			ctrl := seedRoutedStockControl(t, admin, tenantA, bundle)
			for _, p := range tc.pushes {
				if _, _, err := svc.Process(context.Background(), s3EncryptionRecord(t, p.result, p.fields), mkCred(tenantA, nil, "")); err != nil {
					t.Fatalf("Process: %v", err)
				}
			}
			if res, n := evaluateInWindow(t, admin, app, tenantA, ctrl); res != tc.want || n != len(tc.pushes) {
				t.Errorf("(%s, %d), want (%s, %d)", res, n, tc.want, len(tc.pushes))
			}
		})
	}
}
