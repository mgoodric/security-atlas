package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/slack/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackapi"
	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackauth"
	"github.com/mgoodric/security-atlas/connectors/slack/internal/slackcollect"
)

// Package-level seams (slice 305 pattern): doRun reaches through these
// function variables so tests can swap in fakes for the Slack surfaces +
// the SDK push client without hitting a real Slack workspace or platform
// endpoint. Production code paths are unchanged; only the call-site
// indirection moved.
var (
	newSlackClient = func(token slackauth.Token, insecureTLS bool) slackClient {
		return slackapi.New(token, insecureTLS)
	}
	authResolve        = slackauth.Resolve
	collectMembers     = slackcollect.CollectMembers
	collectAuditEvents = slackcollect.CollectAuditEvents
	collectRetention   = slackcollect.CollectRetention
	newSDKClient       = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
)

// slackClient is the union of the narrow Slack surfaces doRun consumes. The
// real *slackapi.Client satisfies it; tests pass a fake.
type slackClient interface {
	slackauth.IdentityAPI
	slackcollect.MembersAPI
	slackcollect.AuditAPI
	slackcollect.RetentionAPI
}

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	slackToken         string
	memberControlID    string
	auditControlID     string
	retentionControlID string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:           "run",
		Short:         "read the Slack roster / admin audit-log / retention settings and push evidence",
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.slackToken == "" {
				f.slackToken = os.Getenv("SLACK_TOKEN")
			}
			if f.slackToken == "" {
				return fmt.Errorf("--slack-token or SLACK_TOKEN is required (least-privilege read-only Slack OAuth token)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.slackToken, "slack-token", "", "Slack read-only OAuth token (env: SLACK_TOKEN)")
	cmd.Flags().StringVar(&f.memberControlID, "member-control-id", "scf:IAC-01", "control id attached to each workspace-member record")
	cmd.Flags().StringVar(&f.auditControlID, "audit-control-id", "scf:MON-01", "control id attached to each admin audit-log record")
	cmd.Flags().StringVar(&f.retentionControlID, "retention-control-id", "scf:DCH-01", "control id attached to the retention-settings record")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	token, err := slackauth.NewToken(f.slackToken)
	if err != nil {
		return err
	}
	client := newSlackClient(token, common.insecure)

	identity, err := authResolve(ctx, client)
	if err != nil {
		return err
	}

	members, err := collectMembers(ctx, client)
	if err != nil {
		return fmt.Errorf("members: %w", err)
	}
	events, err := collectAuditEvents(ctx, client)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	retention, err := collectRetention(ctx, client)
	if err != nil {
		return fmt.Errorf("retention: %w", err)
	}

	push, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = push.Close() }()

	now := time.Now().UTC().Truncate(time.Hour)
	pushed := 0

	for _, m := range members {
		rec, buildErr := buildMemberRecord(m, identity, f.memberControlID, now)
		if buildErr != nil {
			return fmt.Errorf("build member record %s: %w", m.UserID, buildErr)
		}
		if err := pushOne(ctx, push, rec); err != nil {
			return fmt.Errorf("push member %s: %w", m.UserID, err)
		}
		pushed++
	}

	for _, e := range events {
		rec, buildErr := buildAuditRecord(e, identity, f.auditControlID)
		if buildErr != nil {
			return fmt.Errorf("build audit record %s: %w", e.ID, buildErr)
		}
		if err := pushOne(ctx, push, rec); err != nil {
			return fmt.Errorf("push audit %s: %w", e.ID, err)
		}
		pushed++
	}

	rec, buildErr := buildRetentionRecord(retention, identity, f.retentionControlID, now)
	if buildErr != nil {
		return fmt.Errorf("build retention record: %w", buildErr)
	}
	if err := pushOne(ctx, push, rec); err != nil {
		return fmt.Errorf("push retention: %w", err)
	}
	pushed++

	fmt.Printf("pushed %d records (workspace=%s members=%d audit_events=%d profile=pull)\n",
		pushed, identity.TeamID, len(members), len(events))
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

func buildMemberRecord(m slackcollect.Member, identity slackauth.Identity, controlID string, now time.Time) (*evidencev1.EvidenceRecord, error) {
	payload, err := structpb.NewStruct(map[string]any{
		"user_id":  m.UserID,
		"handle":   m.Handle,
		"is_admin": m.IsAdmin,
		"is_owner": m.IsOwner,
		"is_bot":   m.IsBot,
		"deleted":  m.Deleted,
		"has_2fa":  m.Has2FA,
		"reason":   m.Reason,
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey:    idem.Key(m.UserID, now),
		EvidenceKind:      KindMember,
		SchemaVersion:     "1.0.0",
		ControlId:         controlID,
		Scope:             workspaceScope(identity),
		ObservedAt:        timestamppb.New(now),
		Result:            mapResult(m.Result),
		Payload:           payload,
		SourceAttribution: sourceAttribution("members"),
	}, nil
}

func buildAuditRecord(e slackcollect.AuditEvent, identity slackauth.Identity, controlID string) (*evidencev1.EvidenceRecord, error) {
	// Admin audit events carry their own observed moment (DateCreate). The
	// record's observed_at is that moment; the idempotency key anchors on the
	// immutable Slack audit entry id (idem.EventKey).
	observedAt := time.Unix(e.DateCreate, 0).UTC()
	payload, err := structpb.NewStruct(map[string]any{
		"entry_id":    e.ID,
		"action":      e.Action,
		"actor_id":    e.ActorID,
		"actor_email": e.ActorEmail,
		"entity_type": e.EntityType,
		"date_create": float64(e.DateCreate),
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.EventKey(e.ID, e.DateCreate),
		EvidenceKind:   KindAuditLog,
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope:          workspaceScope(identity),
		ObservedAt:     timestamppb.New(observedAt),
		// Audit-log entries are observational evidence, not pass/fail checks.
		Result:            evidencev1.Result_RESULT_UNSPECIFIED,
		Payload:           payload,
		SourceAttribution: sourceAttribution("auditlogs"),
	}, nil
}

func buildRetentionRecord(s slackcollect.RetentionSettings, identity slackauth.Identity, controlID string, now time.Time) (*evidencev1.EvidenceRecord, error) {
	payload, err := structpb.NewStruct(map[string]any{
		"team_id":                 s.TeamID,
		"messages_retention_days": float64(s.MessagesRetentionDays),
		"files_retention_days":    float64(s.FilesRetentionDays),
		"retention_enabled":       s.RetentionEnabled,
		"reason":                  s.Reason,
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey:    idem.Key("retention:"+identity.TeamID, now),
		EvidenceKind:      KindRetention,
		SchemaVersion:     "1.0.0",
		ControlId:         controlID,
		Scope:             workspaceScope(identity),
		ObservedAt:        timestamppb.New(now),
		Result:            mapResult(s.Result),
		Payload:           payload,
		SourceAttribution: sourceAttribution("retention"),
	}, nil
}

// workspaceScope is the minimum scope dimension every Slack record carries —
// the Slack workspace/team id (slice 004 scope-minimum convention).
func workspaceScope(identity slackauth.Identity) []*evidencev1.ScopeDimension {
	return []*evidencev1.ScopeDimension{
		{Key: "tenant_workspace", Values: []string{"slack:" + identity.TeamID}},
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

func mapResult(r slackcollect.Result) evidencev1.Result {
	switch r {
	case slackcollect.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case slackcollect.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case slackcollect.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}
