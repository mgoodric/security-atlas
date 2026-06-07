package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/incidents"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pagerdutyauth"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdrecord"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the PagerDuty reads + the sdk client constructor
// without hitting live PagerDuty or a real platform endpoint. Production code
// paths are byte-for-byte unchanged; only the call-site indirection moved.
var (
	oncallCollect    = oncall.Collect
	incidentsCollect = incidents.Collect
	nowFn            = time.Now
	newSDKClient     = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	newOnCallAPI = func(hc *http.Client, baseURL, token string) oncall.API {
		return oncall.NewClient(hc, baseURL, token)
	}
	newIncidentsAPI = func(hc *http.Client, baseURL, token string) incidents.API {
		return incidents.NewClient(hc, baseURL, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment     string
	service         string
	onCallControl   string
	incidentControl string
	lookbackDays    int
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read PagerDuty on-call coverage + incident summaries and push evidence",
		Long: `Read PagerDuty on-call coverage (escalation policies + on-call) and
incident summaries (bounded look-back window) via the read-only PagerDuty REST
API, transform to pagerduty.oncall_coverage.v1 / pagerduty.incident_summary.v1
records, and push to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set PAGERDUTY_TOKEN (read-only). The token never appears in a log line or
an evidence record. The connector emits coverage facts + on-call IDENTITY and
incident SUMMARY metadata only — never responder personal contact details,
incident free-text, or postmortem text.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			if f.lookbackDays <= 0 {
				return errors.New("--lookback-days must be positive")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.service, "service", "pagerduty", "service scope tag")
	cmd.Flags().StringVar(&f.onCallControl, "oncall-control", "scf:IRO-04", "control_id to attach to pagerduty.oncall_coverage.v1 records")
	cmd.Flags().StringVar(&f.incidentControl, "incident-control", "scf:IRO-02", "control_id to attach to pagerduty.incident_summary.v1 records")
	cmd.Flags().IntVar(&f.lookbackDays, "lookback-days", 90, "bounded incident look-back window in days")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cred, err := pagerdutyauth.Resolve(pagerdutyauth.ResolveOpts{})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	now := nowFn().UTC()

	// On-call coverage.
	onCallAPI := newOnCallAPI(httpClient, cred.BaseURL(), cred.Token())
	policies, err := oncallCollect(ctx, onCallAPI)
	if err != nil {
		return fmt.Errorf("pagerduty oncall collect: %w", err)
	}
	pushed := 0
	for _, p := range policies {
		rec, err := pdrecord.BuildOnCall(p, f.onCallControl, actorID("oncall"), f.service, f.environment, now)
		if err != nil {
			return fmt.Errorf("build oncall record %s: %w", p.ID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push oncall %s: %w", p.ID, err)
		}
		pushed++
	}

	// Incident summaries over the bounded look-back window.
	since := now.AddDate(0, 0, -f.lookbackDays)
	incidentsAPI := newIncidentsAPI(httpClient, cred.BaseURL(), cred.Token())
	incs, err := incidentsCollect(ctx, incidentsAPI, since, now)
	if err != nil {
		return fmt.Errorf("pagerduty incidents collect: %w", err)
	}
	for _, in := range incs {
		rec, err := pdrecord.BuildIncident(in, f.incidentControl, actorID("incidents"), f.service, f.environment, now)
		if err != nil {
			return fmt.Errorf("build incident record %s: %w", in.ID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push incident %s: %w", in.ID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (vendor=pagerduty environment=%s policies=%d incidents=%d lookback_days=%d)\n",
		pushed, f.environment, len(policies), len(incs), f.lookbackDays)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
