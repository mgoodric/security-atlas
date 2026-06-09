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

	"github.com/mgoodric/security-atlas/connectors/grafana/internal/alertrules"
	"github.com/mgoodric/security-atlas/connectors/grafana/internal/grafanaauth"
	"github.com/mgoodric/security-atlas/connectors/grafana/internal/ssoconfig"
	"github.com/mgoodric/security-atlas/connectors/monitoring/alertcfg"
	"github.com/mgoodric/security-atlas/connectors/monitoring/monrecord"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the Grafana read + the sdk client constructor without
// hitting live Grafana or a real platform endpoint.
var (
	alertRulesCollect = alertrules.Collect
	newSDKClient      = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newAlertRulesAPI builds the live read-only HTTP client; seamed so tests
	// inject a fake.
	newAlertRulesAPI = func(hc *http.Client, baseURL, token string) alertrules.API {
		return alertrules.NewClient(hc, baseURL, token)
	}
	// ssoConfigCollect + newSSOConfigAPI are the slice-534 seams, parallel to the
	// alertrules pair: tests swap in a fake Grafana SSO + RBAC read.
	ssoConfigCollect = ssoconfig.Collect
	newSSOConfigAPI  = func(hc *http.Client, baseURL, token string) ssoconfig.API {
		return ssoconfig.NewClient(hc, baseURL, token)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment   string
	ruleControl   string
	accessControl string
	baseURL       string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Grafana alert-rule + SSO/RBAC config inventory and push evidence records",
		Long: `Read Grafana alert-rule + notification-policy inventory and Grafana SSO + RBAC
configuration via the read-only Grafana API, transform to
monitoring.alert_config.v1 + grafana.access_config.v1 records, and push to the
platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set GRAFANA_URL + GRAFANA_TOKEN. The alert-config surface needs the Viewer
role; the access-config surface needs, IN ADDITION, the read-only
fixed:settings:reader (settings:read on settings:auth.*) + fixed:roles:reader
(roles:read + users.roles:read + teams.roles:read) permissions. NEVER grant
Admin "to be safe". The token never appears in a log line or an evidence record.
The connector emits CONFIGURATION + COUNTS only — never a contact point's secret
settings, a SAML private key, an OAuth client secret, an LDAP bind password, a
signing certificate, dashboard JSON, metric time-series, or any user / team-
member / role-assignment identity.`,
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
	cmd.Flags().StringVar(&f.ruleControl, "rule-control", "scf:MON-01", "control_id to attach to monitoring.alert_config.v1 records")
	cmd.Flags().StringVar(&f.accessControl, "access-control", "scf:IAC-06", "control_id to attach to grafana.access_config.v1 records")
	cmd.Flags().StringVar(&f.baseURL, "grafana-url", "", "Grafana base URL override (env: GRAFANA_URL)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
	cred, err := grafanaauth.Resolve(grafanaauth.ResolveOpts{BaseURL: f.baseURL})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := newAlertRulesAPI(httpClient, cred.BaseURL(), cred.Token())
	raw, err := alertRulesCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("grafana collect: %w", err)
	}
	rules := alertcfg.Normalize(alertcfg.VendorGrafana, raw, nil)

	pushed := 0
	for _, rule := range rules {
		rec, err := monrecord.Build(rule, f.ruleControl, actorID("alerts"), "grafana", f.environment)
		if err != nil {
			return fmt.Errorf("build record %s: %w", rule.RuleID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push rule %s: %w", rule.RuleID, err)
		}
		pushed++
	}

	accessPushed, err := runAccessConfig(ctx, f, cred, httpClient, sdkClient)
	if err != nil {
		return err
	}

	fmt.Printf("pushed %d records (vendor=grafana environment=%s: alert_rules=%d access_config=%d)\n",
		pushed+accessPushed, f.environment, pushed, accessPushed)
	return nil
}

// runAccessConfig collects + pushes the Grafana SSO + RBAC configuration
// evidence (grafana.access_config.v1). Separated from the alert-rule pass so
// each evidence kind has an isolated collect->build->push loop; both share the
// one Push RPC (invariant #3). Collect returns ONE access-config record per run.
func runAccessConfig(ctx context.Context, f runFlags, cred grafanaauth.Credential, httpClient *http.Client, sdkClient sdkPushClient) (int, error) {
	api := newSSOConfigAPI(httpClient, cred.BaseURL(), cred.Token())
	cfg, err := ssoConfigCollect(ctx, api, nil)
	if err != nil {
		return 0, fmt.Errorf("grafana access-config collect: %w", err)
	}
	rec, err := ssoconfig.Build(cfg, f.accessControl, actorID("ssoconfig"), "grafana", f.environment)
	if err != nil {
		return 0, fmt.Errorf("build access-config record: %w", err)
	}
	if err := pushOne(ctx, sdkClient, rec); err != nil {
		return 0, fmt.Errorf("push access-config: %w", err)
	}
	return 1, nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
