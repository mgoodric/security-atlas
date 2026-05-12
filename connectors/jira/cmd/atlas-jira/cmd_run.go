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

	"github.com/mgoodric/security-atlas/connectors/jira/internal/idem"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiratickets"
	"github.com/mgoodric/security-atlas/connectors/jira/internal/lineartickets"
)

type runFlags struct {
	platform    string
	environment string
	controlID   string

	// Jira
	jiraBaseURL string
	jiraEmail   string
	jiraToken   string
	jql         string

	// Linear
	linearBaseURL string
	linearKey     string
	teamKey       string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "pull tickets and push jira.ticket_evidence.v1 records",
		Long: `Pull tickets from either Jira Cloud or Linear and push canonical
jira.ticket_evidence.v1 records to the platform.

Auth (env preferred so the secret never appears in shell history):
  Jira:    JIRA_EMAIL  + JIRA_API_TOKEN
  Linear:  LINEAR_API_KEY

Scope cell on every record:
  (platform, project, environment)

Idempotency:
  sha256("jira.ticket_evidence" + ticket_id + hour) — replays within
  the hour dedupe.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.platform != "jira" && f.platform != "linear" {
				return errors.New(`--platform must be "jira" or "linear"`)
			}
			if f.environment == "" {
				return errors.New("--environment is required")
			}
			if f.platform == "jira" && f.jql == "" {
				return errors.New(`--jql is required when --platform jira`)
			}
			if f.platform == "linear" && f.teamKey == "" {
				return errors.New(`--team-key is required when --platform linear`)
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.platform, "platform", "", `target platform — "jira" or "linear" [required]`)
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.controlID, "control-id", "scf:CHG-02", "control_id to attach to jira.ticket_evidence.v1 records")

	// Jira flags
	cmd.Flags().StringVar(&f.jiraBaseURL, "jira-base-url", "", "Jira Cloud base URL, e.g. https://acme.atlassian.net")
	cmd.Flags().StringVar(&f.jiraEmail, "jira-email", "", "Jira account email (env: JIRA_EMAIL preferred)")
	cmd.Flags().StringVar(&f.jiraToken, "jira-token", "", "Jira API token (env: JIRA_API_TOKEN preferred so the token never appears in shell history)")
	cmd.Flags().StringVar(&f.jql, "jql", "", `JQL query, e.g. 'project = CR AND status changed AFTER -90d'`)

	// Linear flags
	cmd.Flags().StringVar(&f.linearBaseURL, "linear-base-url", "https://api.linear.app", "Linear API base URL")
	cmd.Flags().StringVar(&f.linearKey, "linear-key", "", "Linear API key (env: LINEAR_API_KEY preferred so the key never appears in shell history)")
	cmd.Flags().StringVar(&f.teamKey, "team-key", "", "Linear team key, e.g. ENG")

	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	sdkClient, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	httpClient := &http.Client{Timeout: 20 * time.Second}

	switch f.platform {
	case "jira":
		return runJira(ctx, f, httpClient, sdkClient)
	case "linear":
		return runLinear(ctx, f, httpClient, sdkClient)
	}
	return fmt.Errorf("unsupported platform %q", f.platform)
}

func runJira(ctx context.Context, f runFlags, httpClient *http.Client, sdkClient *sdk.Client) error {
	if f.jiraBaseURL == "" {
		return errors.New("--jira-base-url is required when --platform jira")
	}
	creds, err := jiraauth.ResolveJira(jiraauth.JiraOpts{Email: f.jiraEmail, Token: f.jiraToken})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	api := jiratickets.NewClient(httpClient, f.jiraBaseURL, creds)
	tickets, err := jiratickets.List(ctx, api, jiratickets.ListOpts{JQL: f.jql})
	if err != nil {
		return fmt.Errorf("jira list: %w", err)
	}

	pushed := 0
	for _, t := range tickets {
		rec, err := buildJiraTicketRecord(t, f.environment, f.controlID)
		if err != nil {
			return fmt.Errorf("build record %s: %w", t.TicketKey, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = sdkClient.Push(pctx, rec)
		cancel()
		if err != nil {
			return fmt.Errorf("push %s: %w", t.TicketKey, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records (platform=jira environment=%s)\n", pushed, f.environment)
	return nil
}

func runLinear(ctx context.Context, f runFlags, httpClient *http.Client, sdkClient *sdk.Client) error {
	creds, err := jiraauth.ResolveLinear(jiraauth.LinearOpts{APIKey: f.linearKey})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	api := lineartickets.NewClient(httpClient, f.linearBaseURL, creds)
	tickets, err := lineartickets.List(ctx, api, lineartickets.ListOpts{Filter: lineartickets.Filter{TeamKey: f.teamKey}})
	if err != nil {
		return fmt.Errorf("linear list: %w", err)
	}

	pushed := 0
	for _, t := range tickets {
		rec, err := buildLinearTicketRecord(t, f.environment, f.controlID)
		if err != nil {
			return fmt.Errorf("build record %s: %w", t.TicketKey, err)
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = sdkClient.Push(pctx, rec)
		cancel()
		if err != nil {
			return fmt.Errorf("push %s: %w", t.TicketKey, err)
		}
		pushed++
	}
	fmt.Printf("pushed %d records (platform=linear environment=%s)\n", pushed, f.environment)
	return nil
}

// buildJiraTicketRecord turns a Jira Ticket into a wire EvidenceRecord.
// The payload conforms to jira.ticket_evidence.v1 (additionalProperties:
// false): {ticket_key, project_key, summary, status, resolution,
// assignee, url}. Empty optional fields are dropped to keep the
// payload minimal and the JSON Schema happy.
func buildJiraTicketRecord(t jiratickets.Ticket, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	return buildTicketRecord(ticketView{
		TicketKey:  t.TicketKey,
		ProjectKey: t.ProjectKey,
		Summary:    t.Summary,
		Status:     t.Status,
		Resolution: t.Resolution,
		Assignee:   t.Assignee,
		URL:        t.URL,
		ObservedAt: t.ObservedAt,
	}, "jira", env, controlID)
}

func buildLinearTicketRecord(t lineartickets.Ticket, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	return buildTicketRecord(ticketView{
		TicketKey:  t.TicketKey,
		ProjectKey: t.ProjectKey,
		Summary:    t.Summary,
		Status:     t.Status,
		Resolution: t.Resolution,
		Assignee:   t.Assignee,
		URL:        t.URL,
		ObservedAt: t.ObservedAt,
	}, "linear", env, controlID)
}

// ticketView is the cross-platform view buildTicketRecord consumes. Both
// jiratickets.Ticket and lineartickets.Ticket project into it without
// importing each other.
type ticketView struct {
	TicketKey  string
	ProjectKey string
	Summary    string
	Status     string
	Resolution string
	Assignee   string
	URL        string
	ObservedAt time.Time
}

func buildTicketRecord(t ticketView, platform, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	now := t.ObservedAt.UTC().Truncate(time.Hour)
	pm := map[string]any{
		"ticket_key": t.TicketKey,
		"status":     t.Status,
	}
	if t.ProjectKey != "" {
		pm["project_key"] = t.ProjectKey
	}
	if t.Summary != "" {
		pm["summary"] = t.Summary
	}
	if t.Resolution != "" {
		pm["resolution"] = t.Resolution
	}
	if t.Assignee != "" {
		pm["assignee"] = t.Assignee
	}
	if t.URL != "" {
		pm["url"] = t.URL
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	// Idempotency key per orchestrator spec: ticket_id is the bare key
	// (e.g. "PROJ-123" or "ENG-42"). Tests pin this.
	idemKey := idem.TicketKey(t.TicketKey, now)
	// Status string drives Result. "Done" / "Resolved" / "Cancelled" /
	// "Closed" → PASS-shaped; everything else → INCONCLUSIVE. The
	// evaluator (slice 015) owns nuanced state policy.
	result := classifyResult(t.Status)
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idemKey,
		EvidenceKind:   "jira.ticket_evidence.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "platform", Values: []string{platform}},
			{Key: "project", Values: []string{t.ProjectKey}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(now),
		Result:     result,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID(platform, "tickets"),
		},
	}, nil
}

// classifyResult maps the ticket's status string to an evidencev1.Result.
// Conservative mapping: terminal-looking states pass, everything else
// is inconclusive (the evaluator may downgrade). Empty status →
// UNSPECIFIED (the platform decides).
func classifyResult(status string) evidencev1.Result {
	switch status {
	case "":
		return evidencev1.Result_RESULT_UNSPECIFIED
	case "Done", "Resolved", "Closed", "Cancelled", "Canceled", "Completed":
		return evidencev1.Result_RESULT_PASS
	default:
		return evidencev1.Result_RESULT_INCONCLUSIVE
	}
}
