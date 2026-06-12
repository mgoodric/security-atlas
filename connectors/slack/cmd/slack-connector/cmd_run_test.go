package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackauth"
	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackcollect"
)

// resetCommon snapshots and restores the package-global `common` struct so
// tests that mutate it don't bleed into each other (slice 004 pattern).
func resetCommon(t *testing.T) {
	t.Helper()
	prev := common
	t.Cleanup(func() { common = prev })
	common.endpoint = ""
	common.token = ""
	common.insecure = false
}

// fakeSlackClient satisfies the slackClient union (Identity + Members +
// Audit + Retention) so doRun runs with no network.
type fakeSlackClient struct {
	identity  slackauth.Identity
	members   []slackcollect.Member
	events    []slackcollect.AuditEvent
	retention slackcollect.RetentionSettings
}

func (f *fakeSlackClient) ResolveTeam(_ context.Context) (slackauth.Identity, error) {
	return f.identity, nil
}
func (f *fakeSlackClient) ListMembers(_ context.Context, _ string) ([]slackcollect.Member, string, error) {
	return f.members, "", nil
}
func (f *fakeSlackClient) ListAuditEvents(_ context.Context, _ string) ([]slackcollect.AuditEvent, string, error) {
	return f.events, "", nil
}
func (f *fakeSlackClient) GetRetention(_ context.Context) (slackcollect.RetentionSettings, error) {
	return f.retention, nil
}

// fakeSDKClient captures the pushed records so the test can assert the
// round-trip and inspect every payload (AC-10 / AC-11).
type fakeSDKClient struct {
	pushed      []*evidencev1.EvidenceRecord
	pushErr     error
	closeCalled bool
}

func (f *fakeSDKClient) Push(_ context.Context, r *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	f.pushed = append(f.pushed, r)
	return &evidencev1.EvidenceReceipt{}, nil
}
func (f *fakeSDKClient) Close() error { f.closeCalled = true; return nil }

type seams struct {
	slack func(token slackauth.Token, insecureTLS bool) slackClient
	push  *fakeSDKClient
}

func installSeams(t *testing.T, s seams) {
	t.Helper()
	if s.slack != nil {
		prev := newSlackClient
		newSlackClient = s.slack
		t.Cleanup(func() { newSlackClient = prev })
	}
	if s.push != nil {
		prev := newSDKClient
		newSDKClient = func(_, _ string, _ ...sdk.Option) (sdkPushClient, error) { return s.push, nil }
		t.Cleanup(func() { newSDKClient = prev })
	}
}

func okRunFlags() runFlags {
	return runFlags{
		slackToken:         "xoxb-test-token",
		memberControlID:    "scf:IAC-01",
		auditControlID:     "scf:MON-01",
		retentionControlID: "scf:DCH-01",
	}
}

func fullFake() *fakeSlackClient {
	return &fakeSlackClient{
		identity: slackauth.Identity{TeamID: "T0001", TeamName: "Acme"},
		members: []slackcollect.Member{
			{UserID: "U1", Handle: "alice", IsAdmin: true, Has2FA: true},
			{UserID: "U2", Handle: "bob", Has2FA: false},
		},
		events: []slackcollect.AuditEvent{
			{ID: "e1", Action: "user_login", ActorID: "U1", ActorEmail: "alice@acme.test", EntityType: "workspace", DateCreate: 1700000000},
		},
		retention: slackcollect.RetentionSettings{TeamID: "T0001", RetentionEnabled: true, MessagesRetentionDays: 90},
	}
}

// TestDoRun_RoundTrip drives the full collect→build→push path with fakes:
// 2 members + 1 audit event + 1 retention = 4 records (AC-10).
func TestDoRun_RoundTrip(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "platform-bearer"
	common.insecure = true

	push := &fakeSDKClient{}
	installSeams(t, seams{
		slack: func(_ slackauth.Token, _ bool) slackClient { return fullFake() },
		push:  push,
	})

	if err := doRun(context.Background(), okRunFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}
	if len(push.pushed) != 4 {
		t.Fatalf("pushed = %d; want 4 (2 members + 1 audit + 1 retention)", len(push.pushed))
	}
	if !push.closeCalled {
		t.Error("Close not called via defer")
	}

	// Verify each kind appears and scoping is correct.
	kinds := map[string]int{}
	for _, r := range push.pushed {
		kinds[r.EvidenceKind]++
		if len(r.Scope) == 0 || r.Scope[0].Key != "tenant_workspace" {
			t.Errorf("record %s missing tenant_workspace scope", r.EvidenceKind)
		}
		if r.Scope[0].Values[0] != "slack:T0001" {
			t.Errorf("scope value = %q; want slack:T0001", r.Scope[0].Values[0])
		}
		if r.IdempotencyKey == "" {
			t.Errorf("record %s has empty idempotency key", r.EvidenceKind)
		}
		if r.SourceAttribution == nil || !strings.HasPrefix(r.SourceAttribution.ActorId, "connector:slack:") {
			t.Errorf("record %s actor id = %+v; want connector:slack: prefix", r.EvidenceKind, r.SourceAttribution)
		}
		if r.SourceAttribution.SessionId != "" {
			t.Errorf("record %s SessionId should be empty for dedup stability", r.EvidenceKind)
		}
	}
	if kinds[KindMember] != 2 || kinds[KindAuditLog] != 1 || kinds[KindRetention] != 1 {
		t.Errorf("kind distribution = %v; want 2/1/1", kinds)
	}
}

// TestDoRun_NoMessageContentInPayload is the cmd-layer over-collection guard
// (slice 443 AC-11 / threat-model I). Even with a "hostile" fake that stuffs
// member handles and audit actions, NO pushed payload field may carry a
// message-content key. We assert the payload field-name set is a subset of
// the known metadata keys.
func TestDoRun_NoMessageContentInPayload(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "platform-bearer"
	common.insecure = true

	push := &fakeSDKClient{}
	installSeams(t, seams{
		slack: func(_ slackauth.Token, _ bool) slackClient { return fullFake() },
		push:  push,
	})
	if err := doRun(context.Background(), okRunFlags()); err != nil {
		t.Fatalf("doRun: %v", err)
	}

	// Word-token match on the snake_case payload keys (not naive substring),
	// so "messages_retention_days" / "is_admin" are correctly metadata, while
	// a key whose own word is "body"/"text"/"history" trips the guard.
	banned := map[string]bool{
		"body": true, "text": true, "content": true, "history": true,
		"dm": true, "conversation": true, "conversations": true,
		"thread": true, "reply": true, "replies": true,
		"attachment": true, "attachments": true, "blocks": true,
	}
	for _, r := range push.pushed {
		if r.Payload == nil {
			continue
		}
		for field := range r.Payload.Fields {
			for _, w := range strings.Split(strings.ToLower(field), "_") {
				if banned[w] {
					t.Errorf("record %s payload field %q contains banned over-collection word %q", r.EvidenceKind, field, w)
				}
			}
		}
	}
}

// TestDoRun_TokenError: an empty Slack token fails before any collection.
func TestDoRun_TokenError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "platform-bearer"
	f := okRunFlags()
	f.slackToken = ""
	err := doRun(context.Background(), f)
	if err == nil {
		t.Fatal("empty slack token should error")
	}
}

// TestDoRun_PushError: first push fails; doRun stops and wraps the error.
func TestDoRun_PushError(t *testing.T) {
	resetCommon(t)
	common.endpoint = "127.0.0.1:1"
	common.token = "platform-bearer"
	common.insecure = true

	sentinel := errors.New("push rejected")
	push := &fakeSDKClient{pushErr: sentinel}
	installSeams(t, seams{
		slack: func(_ slackauth.Token, _ bool) slackClient { return fullFake() },
		push:  push,
	})
	err := doRun(context.Background(), okRunFlags())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v; want sentinel chain", err)
	}
	if !strings.HasPrefix(err.Error(), "push member ") {
		t.Errorf("err = %q; want 'push member ' prefix", err.Error())
	}
}

// TestBuildAuditRecord_UsesEventTimestamp pins that an audit record's
// observed_at is the event's own DateCreate, not the run clock.
func TestBuildAuditRecord_UsesEventTimestamp(t *testing.T) {
	t.Parallel()
	e := slackcollect.AuditEvent{ID: "e9", Action: "role_change", DateCreate: 1700000000}
	rec, err := buildAuditRecord(e, slackauth.Identity{TeamID: "T1"}, "scf:MON-01")
	if err != nil {
		t.Fatalf("buildAuditRecord: %v", err)
	}
	if rec.Result != evidencev1.Result_RESULT_UNSPECIFIED {
		t.Errorf("audit Result = %v; want UNSPECIFIED (observational)", rec.Result)
	}
	want := time.Unix(1700000000, 0).UTC()
	if !rec.ObservedAt.AsTime().Equal(want) {
		t.Errorf("ObservedAt = %v; want %v", rec.ObservedAt.AsTime(), want)
	}
}

// TestMapResult covers the verdict mapping.
func TestMapResult(t *testing.T) {
	t.Parallel()
	cases := map[slackcollect.Result]evidencev1.Result{
		slackcollect.ResultPass:         evidencev1.Result_RESULT_PASS,
		slackcollect.ResultFail:         evidencev1.Result_RESULT_FAIL,
		slackcollect.ResultInconclusive: evidencev1.Result_RESULT_INCONCLUSIVE,
		slackcollect.Result("weird"):    evidencev1.Result_RESULT_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := mapResult(in); got != want {
			t.Errorf("mapResult(%q) = %v; want %v", in, got, want)
		}
	}
}
