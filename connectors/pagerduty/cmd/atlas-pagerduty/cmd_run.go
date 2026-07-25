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
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/metrics"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/oncall"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pagerdutyauth"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdrecord"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/postmortems"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the PagerDuty reads + the sdk client constructor
// without hitting live PagerDuty or a real platform endpoint. Production code
// paths are byte-for-byte unchanged; only the call-site indirection moved.
var (
	oncallCollect     = oncall.Collect
	incidentsCollect  = incidents.Collect
	postmortemCollect = postmortems.Collect
	metricsCollect    = metrics.Collect
	nowFn             = time.Now
	newSDKClient      = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	newOnCallAPI = func(hc *http.Client, baseURL, token string) oncall.API {
		return oncall.NewClient(hc, baseURL, token)
	}
	newIncidentsAPI = func(hc *http.Client, baseURL, token string) incidents.API {
		return incidents.NewClient(hc, baseURL, token)
	}
	newPostmortemsAPI = func(hc *http.Client, baseURL, token string) postmortems.API {
		return postmortems.NewClient(hc, baseURL, token)
	}
	newMetricsAPI = func(hc *http.Client, baseURL, token string) metrics.API {
		return metrics.NewClient(hc, baseURL, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment       string
	service           string
	onCallControl     string
	incidentControl   string
	postmortemControl string
	metricsControl    string
	lookbackDays      int
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read PagerDuty on-call coverage + incident + postmortem + response-metrics evidence and push it",
		Long: `Read PagerDuty on-call coverage (escalation policies + on-call),
incident summaries, postmortem / retrospective METADATA, and SERVICE-level
incident-response performance AGGREGATES (MTTA / MTTR — mean + percentiles +
counts) — all over a bounded look-back window — via the read-only PagerDuty REST
API, transform to pagerduty.oncall_coverage.v1 / pagerduty.incident_summary.v1 /
pagerduty.postmortem_summary.v1 / pagerduty.response_metrics.v1 records, and push
to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set PAGERDUTY_TOKEN (read-only). The token never appears in a log line or
an evidence record. The connector emits coverage facts + on-call IDENTITY,
incident SUMMARY metadata, postmortem META-FACTS, and SERVICE-/TEAM-level
response-time AGGREGATES only — never responder personal contact details,
incident free-text, the postmortem narrative / timeline / root-cause prose, an
action-item title, or which named responder acknowledged or resolved an
incident (response metrics are aggregated to the service grain; they are never a
per-engineer scorecard).`,
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
	cmd.Flags().StringVar(&f.postmortemControl, "postmortem-control", "scf:IRO-13", "control_id to attach to pagerduty.postmortem_summary.v1 records")
	cmd.Flags().StringVar(&f.metricsControl, "metrics-control", "scf:IRO-02", "control_id to attach to pagerduty.response_metrics.v1 records")
	cmd.Flags().IntVar(&f.lookbackDays, "lookback-days", 90, "bounded incident + postmortem + response-metrics look-back window in days")
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

	// Postmortem / retrospective METADATA over the same bounded look-back window.
	postmortemsAPI := newPostmortemsAPI(httpClient, cred.BaseURL(), cred.Token())
	pms, err := postmortemCollect(ctx, postmortemsAPI, since, now)
	if err != nil {
		return fmt.Errorf("pagerduty postmortems collect: %w", err)
	}
	for _, p := range pms {
		rec, err := pdrecord.BuildPostmortem(p, f.postmortemControl, actorID("postmortems"), f.service, f.environment, now)
		if err != nil {
			return fmt.Errorf("build postmortem record %s: %w", p.ID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push postmortem %s: %w", p.ID, err)
		}
		pushed++
	}

	// SERVICE-level incident-response performance AGGREGATES (MTTA / MTTR) over
	// the same bounded look-back window. The aggregation grain is the service —
	// NEVER a named responder (P0-539 / threat-model I, DOMINANT).
	metricsAPI := newMetricsAPI(httpClient, cred.BaseURL(), cred.Token())
	svcMetrics, err := metricsCollect(ctx, metricsAPI, since, now)
	if err != nil {
		return fmt.Errorf("pagerduty metrics collect: %w", err)
	}
	for _, m := range svcMetrics {
		rec, err := pdrecord.BuildResponseMetrics(m, f.metricsControl, actorID("metrics"), f.service, f.environment, since, now, now)
		if err != nil {
			return fmt.Errorf("build metrics record %s: %w", m.ServiceID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push metrics %s: %w", m.ServiceID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (vendor=pagerduty environment=%s policies=%d incidents=%d postmortems=%d service_metrics=%d lookback_days=%d)\n",
		pushed, f.environment, len(policies), len(incs), len(pms), len(svcMetrics), f.lookbackDays)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
