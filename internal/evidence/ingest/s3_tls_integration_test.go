//go:build integration

// #2993: soc2_cc6_7_s3_tls_enforced reads the AWS connector's
// aws.s3.bucket_tls_enforcement.v1 records. They are routed by type
// (#2924), so they reach the control with no control_id, and ingest
// validates each payload against the registered 1.0.0 schema. These tests
// push payloads in the connector's shape through ingest.Service.Process and
// evaluate the stock bundle with the live engine.

package ingest_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/eval"
)

func s3TLSRecord(t *testing.T, result evidencev1.Result, fields map[string]any) *evidencev1.EvidenceRecord {
	t.Helper()
	payload, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	return record(t, func(r *evidencev1.EvidenceRecord) {
		r.EvidenceKind = "aws.s3.bucket_tls_enforcement.v1"
		r.SchemaVersion = "1.0.0"
		r.ControlId = ""
		r.Result = result
		r.Payload = payload
	})
}

func TestS3TLS_RoutedRecordsDecideTLSEnforcement(t *testing.T) {
	const bundle = "soc2_cc6_7_s3_tls_enforced"
	read := func(name string, present, denies bool) map[string]any {
		return map[string]any{
			"bucket_name":               name,
			"policy_present":            present,
			"denies_insecure_transport": denies,
			"account_id":                "111122223333",
			"region":                    "us-east-1",
		}
	}
	unread := map[string]any{
		"bucket_name": "acme-restricted",
		"account_id":  "111122223333",
		"region":      "us-east-1",
		"reason":      "get-bucket-policy: AccessDenied",
	}
	type push struct {
		result evidencev1.Result
		fields map[string]any
	}
	pass, fail, inconclusive := evidencev1.Result_RESULT_PASS, evidencev1.Result_RESULT_FAIL, evidencev1.Result_RESULT_INCONCLUSIVE
	cases := []struct {
		name   string
		pushes []push
		want   string
	}{
		{"enforced", []push{{pass, read("acme-logs", true, true)}}, eval.ResultPass},
		{"no bucket policy", []push{{pass, read("acme-logs", true, true)}, {fail, read("acme-legacy", false, false)}}, eval.ResultFail},
		{"policy without TLS deny", []push{{fail, read("acme-logs", true, false)}}, eval.ResultFail},
		{"unread policy", []push{{pass, read("acme-logs", true, true)}, {inconclusive, unread}}, eval.ResultInconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, app, _ := boot(t)
			admin := dbtest.NewMigratePool(t)
			ctrl := seedRoutedStockControl(t, admin, tenantA, bundle)
			for _, p := range tc.pushes {
				if _, _, err := svc.Process(context.Background(), s3TLSRecord(t, p.result, p.fields), mkCred(tenantA, nil, "")); err != nil {
					t.Fatalf("Process: %v", err)
				}
			}
			if res, n := evaluateInWindow(t, admin, app, tenantA, ctrl); res != tc.want || n != len(tc.pushes) {
				t.Errorf("(%s, %d), want (%s, %d)", res, n, tc.want, len(tc.pushes))
			}
		})
	}
}
