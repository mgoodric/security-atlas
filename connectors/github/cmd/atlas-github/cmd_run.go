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

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubrepo"
	"github.com/mgoodric/security-atlas/connectors/github/internal/githubscim"
	"github.com/mgoodric/security-atlas/connectors/github/internal/idem"
)

// Package-level seams (slice 308): doRun + doWebhook reach through
// these function variables so tests can swap in fakes for githubauth +
// githubrepo + githubscim + sdk without hitting real GitHub or a real
// platform endpoint. Production code paths are byte-for-byte unchanged;
// only the call-site indirection moved. Mirrors slice 305's awsconnector
// seam shape.
var (
	githubauthResolve   = githubauth.Resolve
	githubrepoNewClient = func(httpClient *http.Client, baseURL string, creds githubauth.Credential) githubrepo.API {
		return githubrepo.NewClient(httpClient, baseURL, creds)
	}
	githubrepoInspect   = githubrepo.Inspect
	githubscimNewClient = func(httpClient *http.Client, baseURL string, creds githubauth.Credential) githubscim.API {
		return githubscim.NewClient(httpClient, baseURL, creds)
	}
	githubscimReconcile = githubscim.Reconcile
	newSDKClient        = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
)

// sdkPushClient is the narrow surface doRun + doWebhook consume from
// sdk.Client. Decoupling here lets tests pass a fake without
// constructing a real grpc.ClientConn. *sdk.Client satisfies this
// interface today.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	org             string
	environment     string
	githubBaseURL   string
	pat             string
	useApp          bool
	appID           string
	appPrivateKey   string
	repoProtControl string
	scimUserControl string
	skipRepoProt    bool
	skipSCIM        bool
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "pull org state and push evidence records",
		Long: `Pull GitHub org repos and SCIM users, transform to evidence records,
and push to the platform.

PAT scopes required:
  - Repository: Administration: Read  (gates github.repo_protection.v1)
  - Repository: Metadata:       Read
  - Organization: Members:      Read  (gates github.scim_user.v1)
  - Organization: Webhooks:     Read

Auth: pass --pat or set GITHUB_TOKEN. App auth (--use-app + GITHUB_APP_ID
+ GITHUB_APP_PRIVATE_KEY) returns ErrAppNotWired in slice 044 — slice 045
wires the JWT signer. Use a PAT for now.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.org == "" {
				return errors.New("--org is required")
			}
			if f.environment == "" {
				return errors.New("--environment is required")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.org, "org", "", "GitHub organization login [required]")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.githubBaseURL, "github-base-url", "https://api.github.com", "GitHub REST/SCIM base URL")
	cmd.Flags().StringVar(&f.pat, "pat", "", "GitHub PAT (env: GITHUB_TOKEN preferred so the token never appears in shell history)")
	cmd.Flags().BoolVar(&f.useApp, "use-app", false, "use GitHub App auth instead of PAT (slice 045)")
	cmd.Flags().StringVar(&f.appID, "app-id", "", "GitHub App id (env: GITHUB_APP_ID)")
	cmd.Flags().StringVar(&f.appPrivateKey, "app-private-key", "", "GitHub App PEM private key (env: GITHUB_APP_PRIVATE_KEY)")
	cmd.Flags().StringVar(&f.repoProtControl, "repo-protection-control", "scf:TDA-06", "control_id to attach to github.repo_protection.v1 records")
	cmd.Flags().StringVar(&f.scimUserControl, "scim-user-control", "scf:IAC-22", "control_id to attach to github.scim_user.v1 records")
	cmd.Flags().BoolVar(&f.skipRepoProt, "skip-repo-protection", false, "skip github.repo_protection.v1 pull")
	cmd.Flags().BoolVar(&f.skipSCIM, "skip-scim", false, "skip github.scim_user.v1 pull")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	creds, err := githubauthResolve(githubauth.ResolveOpts{
		PreferAppMode: f.useApp,
		PAT:           f.pat,
		AppID:         f.appID,
		AppPrivateKey: f.appPrivateKey,
	})
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

	if !f.skipRepoProt {
		repoAPI := githubrepoNewClient(httpClient, f.githubBaseURL, creds)
		states, err := githubrepoInspect(ctx, repoAPI, f.org, nil)
		if err != nil {
			return fmt.Errorf("repo-protection inspect: %w", err)
		}
		for _, s := range states {
			rec, err := buildRepoProtectionRecord(s, f.org, f.environment, f.repoProtControl)
			if err != nil {
				return fmt.Errorf("build repo_protection record %s: %w", s.RepoFullName, err)
			}
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = sdkClient.Push(pctx, rec)
			cancel()
			if err != nil {
				return fmt.Errorf("push repo_protection %s: %w", s.RepoFullName, err)
			}
			pushed++
		}
	}

	if !f.skipSCIM {
		scimAPI := githubscimNewClient(httpClient, f.githubBaseURL, creds)
		users, err := githubscimReconcile(ctx, scimAPI, f.org, nil)
		switch {
		case errors.Is(err, githubscim.ErrSCIMUnavailable):
			fmt.Printf("scim: unavailable for org=%s (non-enterprise) — skipping github.scim_user.v1\n", f.org)
		case err != nil:
			return fmt.Errorf("scim reconcile: %w", err)
		default:
			for _, u := range users {
				rec, err := buildSCIMRecord(u, f.environment, f.scimUserControl)
				if err != nil {
					return fmt.Errorf("build scim_user record %s: %w", u.SCIMUserID, err)
				}
				pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				_, err = sdkClient.Push(pctx, rec)
				cancel()
				if err != nil {
					return fmt.Errorf("push scim_user %s: %w", u.SCIMUserID, err)
				}
				pushed++
			}
		}
	}

	fmt.Printf("pushed %d records (org=%s environment=%s)\n", pushed, f.org, f.environment)
	return nil
}

func buildRepoProtectionRecord(s githubrepo.ProtectionState, org, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := s.ObservedAt.UTC().Truncate(time.Hour)
	payload, err := structpb.NewStruct(map[string]any{
		"repo_full_name":             s.RepoFullName,
		"default_branch":             s.DefaultBranch,
		"required_reviews":           float64(s.RequiredReviews),
		"require_code_owner_reviews": s.RequireCodeOwnerReviews,
		"require_signed_commits":     s.RequireSignedCommits,
		"require_linear_history":     s.RequireLinearHistory,
		"enforce_admins":             s.EnforceAdmins,
	})
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.RepoProtectionKey(s.RepoFullName, now),
		EvidenceKind:   "github.repo_protection.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{org}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     mapResult(s.Result),
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("repo"),
		},
	}, nil
}

func buildSCIMRecord(u githubscim.User, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := u.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"scim_user_id": u.SCIMUserID,
		"user_name":    u.UserName,
		"active":       u.Active,
		"org":          u.Org,
	}
	if u.ExternalID != "" {
		pm["external_id"] = u.ExternalID
	}
	if u.PrimaryEmail != "" {
		pm["primary_email"] = u.PrimaryEmail
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	result := evidencev1.Result_RESULT_PASS
	if !u.Active {
		// Deprovisioned users still emit a record, but the result is FAIL
		// so the evaluator can pick up stale entitlements.
		result = evidencev1.Result_RESULT_FAIL
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem.SCIMUserKey(u.SCIMUserID, now),
		EvidenceKind:   "github.scim_user.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{u.Org}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     result,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("scim"),
		},
	}, nil
}

func mapResult(r githubrepo.Result) evidencev1.Result {
	switch r {
	case githubrepo.ResultPass:
		return evidencev1.Result_RESULT_PASS
	case githubrepo.ResultFail:
		return evidencev1.Result_RESULT_FAIL
	case githubrepo.ResultInconclusive:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	default:
		return evidencev1.Result_RESULT_UNSPECIFIED
	}
}
