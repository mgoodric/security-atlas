package main

import (
	"context"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
)

// recordingSDKClient keeps every pushed record.
type recordingSDKClient struct{ records []*evidencev1.EvidenceRecord }

func (r *recordingSDKClient) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	r.records = append(r.records, rec)
	return &evidencev1.EvidenceReceipt{}, nil
}

func (r *recordingSDKClient) Close() error { return nil }

// #2828: --admin-apps and --infrastructure-apps reach the pushed
// okta.app_assignment.v1 payloads, and every other app is standard.
func TestDoRun_ClassifiesAppsFromFlags(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "test-bearer"
	common.insecure = true

	rc := &recordingSDKClient{}
	installSeams(t, seamOverrides{
		appsPull: func(_ context.Context, _ oktaapps.API, _ func() time.Time) ([]oktaapps.Assignment, error) {
			return []oktaapps.Assignment{sampleAssignment("a1"), sampleAssignment("a2"), sampleAssignment("a3")}, nil
		},
		newClient: func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return rc, nil },
	})
	f := okRunFlags()
	f.skipMFAPolicy, f.skipUserLife = true, true
	f.adminApps = []string{"a1", "missing"}
	f.infraApps = []string{"a2"}
	if err := doRun(context.Background(), f); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	want := map[string]string{"a1": "admin", "a2": "infrastructure", "a3": "standard"}
	if len(rc.records) != len(want) {
		t.Fatalf("pushed %d records, want %d", len(rc.records), len(want))
	}
	for _, rec := range rc.records {
		p := rec.GetPayload().AsMap()
		id, _ := p["app_id"].(string)
		if got := p["classification"]; got != want[id] {
			t.Errorf("%s: classification = %v, want %s", id, got, want[id])
		}
	}
}
