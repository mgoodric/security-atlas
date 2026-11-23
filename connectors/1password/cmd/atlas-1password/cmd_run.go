package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/1password/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/1password/internal/opaccount"
	"github.com/mgoodric/security-atlas/connectors/1password/internal/opauth"
)

// OrgPolicyKind and OrgPolicySchemaVersion name the record the run pushes.
const (
	OrgPolicyKind          = "1password.org_policy.v2"
	OrgPolicySchemaVersion = "2.0.0"
)

type runFlags struct {
	environment   string
	eventsBaseURL string
	usersBaseURL  string
	controlID     string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "pull account audit-stream and member state and push an evidence record",
		Long: `Read the 1Password account's audit-stream and member state from the
documented Events and Users APIs, transform it to a
1password.org_policy.v2 evidence record, and push it to the platform.

Secrets come from the environment only:
  - ONEPASSWORD_EVENTS_TOKEN         Events Reporting integration bearer
  - ONEPASSWORD_USERS_CLIENT_ID      Users API OAuth application client ID
  - ONEPASSWORD_USERS_CLIENT_SECRET  Users API OAuth application secret

Accounts outside 1password.com set both base URLs for their region.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.eventsBaseURL, "events-base-url", opaccount.DefaultEventsBaseURL, "1Password Events API base URL (events.1password.ca, events.1password.eu, events.ent.1password.com for other regions)")
	cmd.Flags().StringVar(&f.usersBaseURL, "users-base-url", opaccount.DefaultUsersBaseURL, "1Password Users API base URL (api.1password.ca, api.1password.eu, api.ent.1password.com for other regions)")
	cmd.Flags().StringVar(&f.controlID, "org-policy-control", "", "control_id to attach to 1password.org_policy.v2 records; empty (the default) routes each record to every control that queries 1password.org_policy.v2 (#2924)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	creds, err := opauth.Resolve()
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	// The redirect guard keeps the Events bearer and the OAuth client
	// secret on the origin they were sent to (#2947).
	httpClient := &http.Client{Timeout: 20 * time.Second, CheckRedirect: opauth.CheckRedirect}
	sdkClient, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := opaccount.NewClient(httpClient, f.eventsBaseURL, f.usersBaseURL, creds)
	state, err := opaccount.Inspect(ctx, api, nil)
	if err != nil {
		return fmt.Errorf("org-policy inspect: %w", err)
	}

	rec, err := buildOrgPolicyRecord(state, f.environment, f.controlID)
	if err != nil {
		return fmt.Errorf("build org_policy record: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := sdkClient.Push(pctx, rec); err != nil {
		return fmt.Errorf("push org_policy: %w", err)
	}

	fmt.Printf("pushed 1 record (org=%s environment=%s result=%s)\n", state.OrgID, f.environment, state.Result)
	return nil
}

// buildOrgPolicyRecord shapes an opaccount.PolicyState into an
// EvidenceRecord. Scope is the two-key (org, environment) tuple
// matching slice 044's convention; idempotency_key is the
// `1password.org_policy.v2|<org_id>|<hour>` derivation; actor_id follows
// the platform-wide `connector:<vendor>:<service>@<version>` shape.
//
// Every v2 field is required, so every one is always emitted.
func buildOrgPolicyRecord(s *opaccount.PolicyState, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := s.ObservedAt.UTC().Truncate(time.Hour)
	payload, err := structpb.NewStruct(map[string]any{
		"org_id":               s.OrgID,
		"audit_events_enabled": s.AuditEventsEnabled,
		"active_members":       float64(s.ActiveMembers),
		"suspended_members":    float64(s.SuspendedMembers),
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.OrgPolicyKey(s.OrgID, now),
		EvidenceKind:   OrgPolicyKind,
		SchemaVersion:  OrgPolicySchemaVersion,
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{s.OrgID}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapResult(s.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("org_policy"),
		},
	}, nil
}

func mapResult(r opaccount.Result) evidencev1.Result {
	switch r {
	case opaccount.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case opaccount.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case opaccount.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}
