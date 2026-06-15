package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpauth"
	"github.com/mgoodric/security-atlas/connectors/gcp/internal/gcpcollect"
)

// resetCommon snapshots and restores the package-global `common` struct so
// tests that mutate it don't bleed into each other (slice 004 pattern).
func resetCommon(t *testing.T) {
	t.Helper()
	prev := common
	t.Cleanup(func() { common = prev })
	common.endpoint = "localhost:1"
	common.token = "test-token"
	common.insecure = true
}

// restoreSeams snapshots and restores the package-level seams so a test's
// fakes don't leak into another test.
func restoreSeams(t *testing.T) {
	t.Helper()
	pAcquire := acquireToken
	pNewGCP := newGCPClient
	pNewSDK := newSDKClient
	t.Cleanup(func() {
		acquireToken = pAcquire
		newGCPClient = pNewGCP
		newSDKClient = pNewSDK
	})
}

// fakeGCPClient satisfies the gcpClient union (Identity + IAM + Storage) so
// doRun runs with no network.
type fakeGCPClient struct {
	identity gcpauth.Identity
	bindings []gcpcollect.IAMBinding
	buckets  []gcpcollect.StorageBucket
}

func (f *fakeGCPClient) ResolveProject(_ context.Context) (gcpauth.Identity, error) {
	return f.identity, nil
}
func (f *fakeGCPClient) ListIAMBindings(_ context.Context, _ string) ([]gcpcollect.IAMBinding, string, error) {
	return f.bindings, "", nil
}
func (f *fakeGCPClient) ListBuckets(_ context.Context, _ string) ([]gcpcollect.StorageBucket, string, error) {
	return f.buckets, "", nil
}

// fakeSDKClient captures the pushed records so the test can assert the
// round-trip emitted what it should.
type fakeSDKClient struct {
	pushed []*evidencev1.EvidenceRecord
	err    error
}

func (f *fakeSDKClient) Push(_ context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.pushed = append(f.pushed, rec)
	return &evidencev1.EvidenceReceipt{}, nil
}
func (f *fakeSDKClient) Close() error { return nil }

func wireFakes(t *testing.T, gcp *fakeGCPClient, sink *fakeSDKClient) {
	t.Helper()
	restoreSeams(t)
	acquireToken = func(_ context.Context) (string, error) { return "ya29.fake", nil }
	newGCPClient = func(_ gcpauth.Credential, _ string, _ bool) gcpClient { return gcp }
	newSDKClient = func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return sink, nil }
}

// TestDoRun_RoundTrip is the AC-9 collect->push round-trip: the connector
// reads IAM bindings + buckets from a faked GCP client and pushes one record
// per item, with the right kinds, scope, and result mapping.
func TestDoRun_RoundTrip(t *testing.T) {
	resetCommon(t)
	gcp := &fakeGCPClient{
		identity: gcpauth.Identity{ProjectID: "prod-123"},
		bindings: []gcpcollect.IAMBinding{
			{Member: "user:a@b.com", MemberType: "user", Role: "roles/viewer", Result: gcpcollect.ResultInconclusive},
		},
		buckets: []gcpcollect.StorageBucket{
			{Name: "locked", PublicAccessFlag: "enforced", UniformAccess: true, Result: gcpcollect.ResultPass},
			{Name: "open", PublicAccessFlag: "inherited", Result: gcpcollect.ResultFail, Reason: "public-access-prevention is not enforced"},
		},
	}
	sink := &fakeSDKClient{}
	wireFakes(t, gcp, sink)

	if err := doRun(context.Background(), runFlags{projectID: "prod-123", iamControlID: "scf:IAC-21", bucketControlID: "scf:CRY-04"}); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if len(sink.pushed) != 3 {
		t.Fatalf("pushed %d records; want 3 (1 binding + 2 buckets)", len(sink.pushed))
	}

	var iam, storage int
	for _, rec := range sink.pushed {
		// Every record carries the project scope.
		if len(rec.GetScope()) != 1 || rec.GetScope()[0].GetKey() != "cloud_project" ||
			rec.GetScope()[0].GetValues()[0] != "gcp:prod-123" {
			t.Errorf("record scope = %v; want cloud_project=gcp:prod-123", rec.GetScope())
		}
		if rec.GetIdempotencyKey() == "" {
			t.Error("record missing idempotency key")
		}
		if rec.GetSourceAttribution().GetActorType() != "connector" {
			t.Error("record actor_type must be connector")
		}
		if !strings.HasPrefix(rec.GetSourceAttribution().GetActorId(), "connector:gcp:") {
			t.Errorf("actor_id = %q; want connector:gcp: prefix", rec.GetSourceAttribution().GetActorId())
		}
		switch rec.GetEvidenceKind() {
		case KindIAMBinding:
			iam++
		case KindStorageBucket:
			storage++
		default:
			t.Errorf("unexpected kind %q", rec.GetEvidenceKind())
		}
	}
	if iam != 1 || storage != 2 {
		t.Errorf("kinds = iam:%d storage:%d; want 1,2", iam, storage)
	}
}

// TestDoRun_PayloadIsConfigOnly is the AC-10 over-collection assertion at the
// record level: the pushed payloads must contain ONLY config/binding metadata
// keys — no object content, no secret, no service-account key material, and no
// raw GCP credential.
func TestDoRun_PayloadIsConfigOnly(t *testing.T) {
	resetCommon(t)
	gcp := &fakeGCPClient{
		identity: gcpauth.Identity{ProjectID: "p"},
		bindings: []gcpcollect.IAMBinding{{Member: "serviceAccount:s@p.iam", MemberType: "serviceAccount", Role: "roles/owner", IsServiceAcc: true, Result: gcpcollect.ResultInconclusive}},
		buckets:  []gcpcollect.StorageBucket{{Name: "b", PublicAccessFlag: "enforced", UniformAccess: true, Result: gcpcollect.ResultPass}},
	}
	sink := &fakeSDKClient{}
	wireFakes(t, gcp, sink)

	if err := doRun(context.Background(), runFlags{projectID: "p"}); err != nil {
		t.Fatalf("doRun: %v", err)
	}

	bannedKeyTokens := []string{"object", "blob", "content", "secret", "credential", "password", "private", "acl"}
	for _, rec := range sink.pushed {
		for key := range rec.GetPayload().GetFields() {
			lk := strings.ToLower(key)
			for _, banned := range bannedKeyTokens {
				if strings.Contains(lk, banned) {
					t.Errorf("payload key %q contains banned over-collection token %q (kind=%s)", key, banned, rec.GetEvidenceKind())
				}
			}
			// The fake credential value must never appear in any payload.
			if strings.Contains(lk, "ya29") {
				t.Errorf("payload key %q looks like a leaked credential", key)
			}
		}
		// No payload VALUE may carry the credential either.
		for _, v := range rec.GetPayload().GetFields() {
			if strings.Contains(v.GetStringValue(), "ya29.fake") {
				t.Error("a payload value carried the GCP credential — P0-442-4 violation")
			}
		}
	}
}

// TestDoRun_AcquireTokenError surfaces the ADC failure path (no usable
// credential).
func TestDoRun_AcquireTokenError(t *testing.T) {
	resetCommon(t)
	restoreSeams(t)
	acquireToken = func(_ context.Context) (string, error) { return "", errors.New("no ADC") }
	if err := doRun(context.Background(), runFlags{projectID: "p"}); err == nil {
		t.Fatal("expected error when ADC acquisition fails")
	}
}

// TestDoRun_PushError surfaces the push failure path.
func TestDoRun_PushError(t *testing.T) {
	resetCommon(t)
	gcp := &fakeGCPClient{
		identity: gcpauth.Identity{ProjectID: "p"},
		buckets:  []gcpcollect.StorageBucket{{Name: "b", PublicAccessFlag: "enforced", UniformAccess: true, Result: gcpcollect.ResultPass}},
	}
	sink := &fakeSDKClient{err: errors.New("push boom")}
	wireFakes(t, gcp, sink)
	if err := doRun(context.Background(), runFlags{projectID: "p"}); err == nil {
		t.Fatal("expected push error to propagate")
	}
}

// TestActorID is the stable-format assertion (slice 004 actor_id convention).
func TestActorID(t *testing.T) {
	t.Parallel()
	if got := actorID("iam"); !strings.HasPrefix(got, "connector:gcp:iam@") {
		t.Errorf("actorID(iam) = %q; want connector:gcp:iam@ prefix", got)
	}
}

// TestSupportedKinds asserts the two kinds are registered for the connector.
func TestSupportedKinds(t *testing.T) {
	t.Parallel()
	if len(SupportedKinds) != 2 {
		t.Fatalf("SupportedKinds = %v; want 2 kinds", SupportedKinds)
	}
}

// TestPullIntervalHonest asserts the pull-profile note never claims
// "continuous monitoring" (anti-pattern / P0-442-6).
func TestPullIntervalHonest(t *testing.T) {
	t.Parallel()
	if strings.Contains(strings.ToLower(PullIntervalNote), "continuous") &&
		!strings.Contains(strings.ToLower(PullIntervalNote), "not") {
		t.Errorf("PullIntervalNote claims continuous monitoring: %q", PullIntervalNote)
	}
}
