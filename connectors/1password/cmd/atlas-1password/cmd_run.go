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

type runFlags struct {
	environment  string
	opBaseURL    string
	serviceToken string
	controlID    string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "pull org-policy state and push an evidence record",
		Long: `Pull 1Password Business org-policy state, transform to a
1password.org_policy.v1 evidence record, and push to the platform.

Service Account scopes required (least-privilege):
  - vault:read_items  (Read)
  - account:read      (Read)

Auth: set $ONEPASSWORD_SERVICE_ACCOUNT_TOKEN, or pass --token. The env
path is preferred so the secret never appears in shell history.`,
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
	cmd.Flags().StringVar(&f.opBaseURL, "1password-base-url", "https://api.1password.com", "1Password public API base URL")
	cmd.Flags().StringVar(&f.serviceToken, "token", "", "1Password Service Account token (env: ONEPASSWORD_SERVICE_ACCOUNT_TOKEN preferred so the token never appears in shell history)")
	cmd.Flags().StringVar(&f.controlID, "org-policy-control", "scf:IAC-10", "control_id to attach to 1password.org_policy.v1 records")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	creds, err := opauth.Resolve(opauth.ResolveOpts{Token: f.serviceToken})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := opaccount.NewClient(httpClient, f.opBaseURL, creds)
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
// matching slice 044's convention; idempotency_key is the slice 046
// `1password.org_policy|<org_id>|<hour>` derivation; actor_id follows
// the platform-wide `connector:<vendor>:<service>@<version>` shape.
//
// The optional schema fields are populated only when present in the
// upstream response — schemaregistry validation already enforces the
// shape on the platform side.
func buildOrgPolicyRecord(s *opaccount.PolicyState, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := s.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"org_id":              s.OrgID,
		"two_factor_required": s.TwoFactorRequired,
	}
	if s.MinimumPasswordLength > 0 {
		pm["minimum_password_length"] = float64(s.MinimumPasswordLength)
	}
	pm["domain_restrictions_enabled"] = s.DomainRestrictionsEnabled
	if s.ActiveMembers > 0 {
		pm["active_members"] = float64(s.ActiveMembers)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.OrgPolicyKey(s.OrgID, now),
		EvidenceKind:   "1password.org_policy.v1",
		SchemaVersion:  "1.0.0",
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
