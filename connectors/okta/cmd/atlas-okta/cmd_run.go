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

	"github.com/mgoodric/security-atlas/connectors/okta/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaapps"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktaauth"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktapolicy"
	"github.com/mgoodric/security-atlas/connectors/okta/internal/oktausers"
)

// Package-level seams (slice 309): doRun reaches through these function
// variables so tests can swap in fakes for the three okta Pull calls
// and the sdk client constructor without hitting real Okta endpoints
// or a real platform endpoint. Production code paths are byte-for-byte
// unchanged; only the call-site indirection moved.
var (
	oktapolicyPull = oktapolicy.Pull
	oktaappsPull   = oktaapps.Pull
	oktausersPull  = oktausers.Pull
	newSDKClient   = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
// Decoupling here lets tests pass a fake without constructing a real
// grpc.ClientConn. *sdk.Client satisfies this interface today.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	org              string
	environment      string
	oktaBaseURL      string
	token            string
	mfaPolicyControl string
	appAssignControl string
	userLifeControl  string
	skipMFAPolicy    bool
	skipAppAssign    bool
	skipUserLife     bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "pull Okta state and push evidence records",
		Long: `Pull Okta MFA policies, app assignments, and user lifecycle,
transform to evidence records, and push to the platform.

Okta scopes required (least-privilege, Read-only Administrator):
  - okta.policies.read  (gates okta.mfa_policy.v1)
  - okta.apps.read      (gates okta.app_assignment.v1)
  - okta.groups.read    (gates okta.app_assignment.v1)
  - okta.users.read     (gates okta.user_lifecycle.v1)

Auth: set OKTA_API_TOKEN in the process environment. The --token flag
accepts the same value but env-var is preferred so the token never
appears in shell history.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.org == "" {
				return errors.New("--org is required")
			}
			if f.environment == "" {
				return errors.New("--environment is required")
			}
			if f.oktaBaseURL == "" {
				return errors.New("--okta-base-url is required (e.g. https://example.okta.com)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.org, "org", "", "Okta organization name (tenant slug) [required]")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.oktaBaseURL, "okta-base-url", "", "Okta tenant base URL (e.g. https://example.okta.com) [required]")
	// NOTE: --token accepted for ad-hoc shells but env-var path is the
	// documented preferred path so the secret never reaches shell history.
	cmd.Flags().StringVar(&f.token, "token-env-only-do-not-pass-on-cli", "", "")
	_ = cmd.Flags().MarkHidden("token-env-only-do-not-pass-on-cli")
	cmd.Flags().StringVar(&f.mfaPolicyControl, "mfa-policy-control", "scf:IAC-06", "control_id to attach to okta.mfa_policy.v1 records")
	cmd.Flags().StringVar(&f.appAssignControl, "app-assignment-control", "scf:IAC-21", "control_id to attach to okta.app_assignment.v1 records")
	cmd.Flags().StringVar(&f.userLifeControl, "user-lifecycle-control", "scf:IAC-22", "control_id to attach to okta.user_lifecycle.v1 records")
	cmd.Flags().BoolVar(&f.skipMFAPolicy, "skip-mfa-policy", false, "skip okta.mfa_policy.v1 pull")
	cmd.Flags().BoolVar(&f.skipAppAssign, "skip-app-assignment", false, "skip okta.app_assignment.v1 pull")
	cmd.Flags().BoolVar(&f.skipUserLife, "skip-user-lifecycle", false, "skip okta.user_lifecycle.v1 pull")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	creds, err := oktaauth.Resolve(oktaauth.ResolveOpts{Token: f.token})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	pushed := 0

	if !f.skipMFAPolicy {
		api := oktapolicy.NewClient(httpClient, f.oktaBaseURL, creds)
		states, err := oktapolicyPull(ctx, api, nil)
		if err != nil {
			return fmt.Errorf("mfa policy pull: %w", err)
		}
		for _, s := range states {
			rec, err := buildMFAPolicyRecord(s, f.org, f.environment, f.mfaPolicyControl)
			if err != nil {
				return fmt.Errorf("build mfa_policy record %s: %w", s.PolicyID, err)
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = sdkClient.Push(pctx, rec)
			cancel()
			if err != nil {
				return fmt.Errorf("push mfa_policy %s: %w", s.PolicyID, err)
			}
			pushed++
		}
	}

	if !f.skipAppAssign {
		api := oktaapps.NewClient(httpClient, f.oktaBaseURL, creds)
		assignments, err := oktaappsPull(ctx, api, nil)
		if err != nil {
			return fmt.Errorf("app assignment pull: %w", err)
		}
		for _, a := range assignments {
			rec, err := buildAppAssignmentRecord(a, f.org, f.environment, f.appAssignControl)
			if err != nil {
				return fmt.Errorf("build app_assignment record %s: %w", a.AppID, err)
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = sdkClient.Push(pctx, rec)
			cancel()
			if err != nil {
				return fmt.Errorf("push app_assignment %s: %w", a.AppID, err)
			}
			pushed++
		}
	}

	if !f.skipUserLife {
		api := oktausers.NewClient(httpClient, f.oktaBaseURL, creds)
		users, err := oktausersPull(ctx, api, nil)
		if err != nil {
			return fmt.Errorf("user lifecycle pull: %w", err)
		}
		for _, u := range users {
			rec, err := buildUserLifecycleRecord(u, f.org, f.environment, f.userLifeControl)
			if err != nil {
				return fmt.Errorf("build user_lifecycle record %s: %w", u.UserID, err)
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = sdkClient.Push(pctx, rec)
			cancel()
			if err != nil {
				return fmt.Errorf("push user_lifecycle %s: %w", u.UserID, err)
			}
			pushed++
		}
	}

	fmt.Printf("pushed %d records (org=%s environment=%s)\n", pushed, f.org, f.environment)
	return nil
}

func buildMFAPolicyRecord(s oktapolicy.PolicyState, org, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := s.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"policy_id":    s.PolicyID,
		"policy_name":  s.PolicyName,
		"mfa_required": s.MFARequired,
	}
	if len(s.FactorsAllowed) > 0 {
		pm["factors_allowed"] = asAny(s.FactorsAllowed)
	}
	if len(s.AppliesToGroups) > 0 {
		pm["applies_to_groups"] = asAny(s.AppliesToGroups)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.MFAPolicyKey(s.PolicyID, now),
		EvidenceKind:   "okta.mfa_policy.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{org}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapPolicyResult(s.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("policy"),
		},
	}, nil
}

func buildAppAssignmentRecord(a oktaapps.Assignment, org, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := a.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"app_id":               a.AppID,
		"app_name":             a.AppName,
		"status":               a.Status,
		"assigned_group_ids":   asAny(a.AssignedGroupIDs),
		"assigned_group_count": float64(a.AssignedGroupCount),
	}
	if a.SignOnMode != "" {
		pm["sign_on_mode"] = a.SignOnMode
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.AppAssignmentKey(a.AppID, now),
		EvidenceKind:   "okta.app_assignment.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{org}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // descriptive — evaluator decides
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("apps"),
		},
	}, nil
}

func buildUserLifecycleRecord(u oktausers.Lifecycle, org, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := u.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"user_id":      u.UserID,
		"login":        u.Login,
		"status":       u.Status,
		"mfa_enrolled": u.MFAEnrolled,
	}
	if u.PrimaryEmail != "" {
		pm["primary_email"] = u.PrimaryEmail
	}
	if !u.CreatedAt.IsZero() {
		pm["created_at"] = u.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !u.ActivatedAt.IsZero() {
		pm["activated_at"] = u.ActivatedAt.UTC().Format(time.RFC3339)
	}
	if !u.LastLoginAt.IsZero() {
		pm["last_login_at"] = u.LastLoginAt.UTC().Format(time.RFC3339)
	}
	if !u.DeactivatedAt.IsZero() && u.Status == "DEPROVISIONED" {
		pm["deactivated_at"] = u.DeactivatedAt.UTC().Format(time.RFC3339)
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.UserLifecycleKey(u.UserID, now),
		EvidenceKind:   "okta.user_lifecycle.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{org}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapUsersResult(u.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("users"),
		},
	}, nil
}

func mapPolicyResult(r oktapolicy.Result) evidencev1.Result {
	switch r {
	case oktapolicy.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case oktapolicy.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case oktapolicy.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

func mapUsersResult(r oktausers.Result) evidencev1.Result {
	switch r {
	case oktausers.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case oktausers.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case oktausers.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}

// asAny converts []string to []any so structpb.NewStruct accepts the
// value. structpb's NewValue path requires []any for arrays.
func asAny(in []string) []any {
	if len(in) == 0 {
		return nil
	}
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
