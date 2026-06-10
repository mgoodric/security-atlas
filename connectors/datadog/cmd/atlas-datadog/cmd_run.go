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

	"github.com/mgoodric/security-atlas/connectors/datadog/internal/datadogauth"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/firingevents"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/monitors"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/siemrules"
	"github.com/mgoodric/security-atlas/connectors/datadog/internal/siemsignals"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
	"github.com/mgoodric/security-atlas/connectors/monitoring/firingrecord"
	"github.com/mgoodric/security-atlas/connectors/monitoring/monrecord"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the Datadog read + the sdk client constructor without
// hitting live Datadog or a real platform endpoint. Production code paths are
// byte-for-byte unchanged; only the call-site indirection moved.
var (
	monitorsCollect = monitors.Collect
	newSDKClient    = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newMonitorsAPI builds the live read-only HTTP client; seamed so tests
	// inject a fake.
	newMonitorsAPI = func(hc *http.Client, baseURL, apiKey, appKey string) monitors.API {
		return monitors.NewClient(hc, baseURL, apiKey, appKey)
	}
	// siemrulesCollect + newSIEMRulesAPI are the slice-533 seams, parallel to
	// the monitors pair: tests swap in a fake Datadog security-monitoring read.
	siemrulesCollect = siemrules.Collect
	newSIEMRulesAPI  = func(hc *http.Client, baseURL, apiKey, appKey string) siemrules.API {
		return siemrules.NewClient(hc, baseURL, apiKey, appKey)
	}
	// siemSignalsCollect + newSIEMSignalsAPI are the slice-636 seams, parallel
	// to the siemrules pair: tests swap in a fake Datadog security-signals read.
	siemSignalsCollect = siemsignals.Collect
	newSIEMSignalsAPI  = func(hc *http.Client, baseURL, apiKey, appKey string) siemsignals.API {
		return siemsignals.NewClient(hc, baseURL, apiKey, appKey)
	}
	// firingCollect + newFiringAPI are the slice-535 seams, parallel to the
	// siemsignals pair: tests swap in a fake Datadog Events read (monitor
	// alert-firing history).
	firingCollect = firingevents.Collect
	newFiringAPI  = func(hc *http.Client, baseURL, apiKey, appKey string) firingevents.API {
		return firingevents.NewClient(hc, baseURL, apiKey, appKey)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment       string
	monitorControl    string
	siemControl       string
	siemSignalControl string
	firingControl     string
	siemLookback      time.Duration
	firingLookback    time.Duration
	site              string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Datadog monitor inventory and push evidence records",
		Long: `Read Datadog monitor inventory (GET /api/v1/monitor, monitors_read),
Datadog Cloud-SIEM detection-rule inventory
(GET /api/v2/security_monitoring/rules, security_monitoring_rules_read), and
Datadog Cloud-SIEM signal-history triage outcomes
(GET /api/v2/security_monitoring/signals, security_monitoring_signals_read) via
the read-only Datadog API, transform to monitoring.alert_config.v1 +
datadog.siem_rule.v1 + datadog.siem_signal.v1 records, and push to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). The signal-history surface reads a bounded look-back window
(--siem-lookback, default 24h). NOT continuous monitoring and NOT event-driven.

Auth: set DATADOG_API_KEY + DATADOG_APP_KEY (read-only monitors_read +
security_monitoring_rules_read + security_monitoring_signals_read scopes), and
optionally DATADOG_SITE. The keys never appear in a log line or an evidence
record. The connector emits monitor / detection-rule name, type, enabled state,
severity, notification-target HANDLES, and — for signal history — the signal id,
firing rule id, status, timeline timestamps, and the OPAQUE triager handle only
— never the secret webhook URL, an integration token, a recipient email, the
monitor query, the detection query, a signal MESSAGE body, matched log samples,
matched-event payloads, signal-body tags, dashboard JSON, or metric
time-series.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRun(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.monitorControl, "monitor-control", "scf:MON-01", "control_id to attach to monitoring.alert_config.v1 records")
	cmd.Flags().StringVar(&f.siemControl, "siem-control", "scf:THR-01", "control_id to attach to datadog.siem_rule.v1 records")
	cmd.Flags().StringVar(&f.siemSignalControl, "siem-signal-control", "scf:IRO-09", "control_id to attach to datadog.siem_signal.v1 records")
	cmd.Flags().StringVar(&f.firingControl, "firing-control", "scf:IRO-09", "control_id to attach to monitoring.alert_firing.v1 records")
	cmd.Flags().DurationVar(&f.siemLookback, "siem-lookback", 24*time.Hour, "bounded look-back window for the SIEM signal-history pull (honest interval — NOT continuous monitoring)")
	cmd.Flags().DurationVar(&f.firingLookback, "datadog-firing-lookback", 24*time.Hour, "bounded look-back window for the monitor alert-firing-history pull (honest interval — NOT continuous monitoring)")
	cmd.Flags().StringVar(&f.site, "site", "", "Datadog site override (env: DATADOG_SITE; default datadoghq.com)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cred, err := datadogauth.Resolve(datadogauth.ResolveOpts{Site: f.site})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := newMonitorsAPI(httpClient, cred.BaseURL(), cred.APIKey(), cred.AppKey())
	raw, err := monitorsCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("datadog collect: %w", err)
	}
	rules := alertcfg.Normalize(alertcfg.VendorDatadog, raw, nil)

	pushed := 0
	for _, rule := range rules {
		rec, err := monrecord.Build(rule, f.monitorControl, actorID("monitors"), "datadog", f.environment)
		if err != nil {
			return fmt.Errorf("build record %s: %w", rule.RuleID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push monitor %s: %w", rule.RuleID, err)
		}
		pushed++
	}

	siemPushed, err := runSIEMRules(ctx, f, cred, httpClient, sdkClient)
	if err != nil {
		return err
	}

	signalPushed, err := runSIEMSignals(ctx, f, cred, httpClient, sdkClient)
	if err != nil {
		return err
	}

	firingPushed, err := runFiring(ctx, f, cred, httpClient, sdkClient)
	if err != nil {
		return err
	}

	fmt.Printf("pushed %d records (vendor=datadog environment=%s: monitors=%d siem_rules=%d siem_signals=%d alert_firings=%d)\n",
		pushed+siemPushed+signalPushed+firingPushed, f.environment, pushed, siemPushed, signalPushed, firingPushed)
	return nil
}

// runFiring collects + pushes Datadog monitor alert-firing-history evidence
// (monitoring.alert_firing.v1) over a bounded look-back window. Separated from
// the other passes so each evidence kind has an isolated collect->build->push
// loop; all four share the one Push RPC (invariant #3). Bounded PULL, not
// event-driven (decisions-log D1).
func runFiring(ctx context.Context, f runFlags, cred datadogauth.Credential, httpClient *http.Client, sdkClient sdkPushClient) (int, error) {
	api := newFiringAPI(httpClient, cred.BaseURL(), cred.APIKey(), cred.AppKey())
	firings, err := firingCollect(ctx, api, f.firingLookback, nil)
	if err != nil {
		return 0, fmt.Errorf("datadog firing collect: %w", err)
	}
	pushed := 0
	for _, fr := range firings {
		rec, err := firingrecord.Build(fr, f.firingControl, actorID("firing"), "datadog", f.environment)
		if err != nil {
			return pushed, fmt.Errorf("build firing record %s: %w", fr.RuleID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return pushed, fmt.Errorf("push firing %s: %w", fr.RuleID, err)
		}
		pushed++
	}
	return pushed, nil
}

// runSIEMRules collects + pushes Datadog Cloud-SIEM detection-rule evidence
// (datadog.siem_rule.v1). Separated from the monitor pass so each evidence kind
// has an isolated collect→build→push loop; both share the one Push RPC
// (invariant #3).
func runSIEMRules(ctx context.Context, f runFlags, cred datadogauth.Credential, httpClient *http.Client, sdkClient sdkPushClient) (int, error) {
	api := newSIEMRulesAPI(httpClient, cred.BaseURL(), cred.APIKey(), cred.AppKey())
	rules, err := siemrulesCollect(ctx, api, nil)
	if err != nil {
		return 0, fmt.Errorf("datadog siem collect: %w", err)
	}
	pushed := 0
	for _, rule := range rules {
		rec, err := siemrules.Build(rule, f.siemControl, actorID("siemrules"), "datadog", f.environment)
		if err != nil {
			return pushed, fmt.Errorf("build siem record %s: %w", rule.RuleID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return pushed, fmt.Errorf("push siem rule %s: %w", rule.RuleID, err)
		}
		pushed++
	}
	return pushed, nil
}

// runSIEMSignals collects + pushes Datadog Cloud-SIEM signal-history evidence
// (datadog.siem_signal.v1) over a bounded look-back window. Separated from the
// rule pass so each evidence kind has an isolated collect->build->push loop;
// all three share the one Push RPC (invariant #3). Bounded PULL, not
// event-driven (decisions-log D2).
func runSIEMSignals(ctx context.Context, f runFlags, cred datadogauth.Credential, httpClient *http.Client, sdkClient sdkPushClient) (int, error) {
	api := newSIEMSignalsAPI(httpClient, cred.BaseURL(), cred.APIKey(), cred.AppKey())
	signals, err := siemSignalsCollect(ctx, api, f.siemLookback, nil)
	if err != nil {
		return 0, fmt.Errorf("datadog siem signal collect: %w", err)
	}
	pushed := 0
	for _, sig := range signals {
		rec, err := siemsignals.Build(sig, f.siemSignalControl, actorID("siemsignals"), "datadog", f.environment)
		if err != nil {
			return pushed, fmt.Errorf("build siem signal record %s: %w", sig.SignalID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return pushed, fmt.Errorf("push siem signal %s: %w", sig.SignalID, err)
		}
		pushed++
	}
	return pushed, nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
