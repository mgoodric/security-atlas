package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/jamf/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/jamf/internal/jamfauth"
	"github.com/mgoodric/security-atlas/connectors/mdm/cfgprofile"
	"github.com/mgoodric/security-atlas/connectors/mdm/cfgrecord"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// Package-level seams for the config-profile run path, parallel to the
// device-posture + software seams. Tests swap in fakes for the Jamf config-
// profile read + the sdk client constructor without hitting live Jamf.
var (
	configProfileCollect = devices.CollectConfigProfiles
	newConfigProfileAPI  = func(hc *http.Client, baseURL, clientID, clientSecret string) devices.ConfigProfileAPI {
		return devices.NewClient(hc, baseURL, clientID, clientSecret)
	}
)

type runConfigProfilesFlags struct {
	environment   string
	configControl string
	baseURL       string
}

func newRunConfigProfilesCmd() *cobra.Command {
	var f runConfigProfilesFlags
	cmd := &cobra.Command{
		Use:   "run-config-profiles",
		Short: "read Jamf managed-computer configuration-profile detail and push evidence records",
		Long: `Read Jamf managed-computer configuration-profile detail via the read-only Jamf
Pro API (GET /api/v1/computers-inventory, GENERAL + CONFIGURATION_PROFILES +
posture sections OPERATING_SYSTEM / DISK_ENCRYPTION / SECURITY), transform to
endpoint.config_profile.v1 records, and push to the platform.

This reports WHICH configuration / compliance profiles are deployed to a managed
computer and what compliance-relevant settings they enforce — evidence for
configuration-management controls (SCF CFG-02 Secure Baseline Configurations /
CFG-04 Configuration Change Control) at a finer grain than the posture verdict.

Per-setting enrichment (slice 595): each device carries a synthetic "Enforced
Configuration Summary" profile whose settings are the effective enforced
hardening state (disk_encryption_enforced, gatekeeper_enabled,
screen_lock_enforced, device_supervised, device_managed) derived from the
posture inventory sections — non-secret booleans, NEVER the raw profile payload.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: identical read-only credential to 'run' (JAMF_BASE_URL + JAMF_CLIENT_ID +
JAMF_CLIENT_SECRET; the API client must be bound to a read-only API role). The
posture sections are covered by the SAME read-only "Read Computers" role the
posture 'run' uses — NO new scope. The secret never appears in a log line or an
evidence record.

SECRET-REDACTION BOUNDARY (P0-556): configuration profiles routinely embed
secrets — Wi-Fi PSKs, VPN shared secrets, certificate private keys, API tokens,
SCEP challenges, and raw payload-content blobs. NONE of these ever enters an
evidence record. No requested section carries the raw payload blob (the
CONFIGURATION_PROFILES section is metadata-only; the posture sections report
effective enforced-state summaries only), and the per-profile settings field is
restricted to a compliance-relevant allow-list whose values are non-secret
summaries.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRunConfigProfiles(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.configControl, "config-control", "scf:CFG-02", "control_id to attach to endpoint.config_profile.v1 records")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "Jamf Pro instance URL override (env: JAMF_BASE_URL)")
	return cmd
}

func doRunConfigProfiles(ctx context.Context, f runConfigProfilesFlags) error {
	cred, err := jamfauth.Resolve(jamfauth.ResolveOpts{BaseURL: f.baseURL})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := newConfigProfileAPI(httpClient, cred.BaseURL(), cred.ClientID(), cred.ClientSecret())
	raw, err := configProfileCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("jamf config-profile collect: %w", err)
	}
	devs := cfgprofile.Normalize(devposture.MDMJamf, raw, nil)

	pushed := 0
	for _, dev := range devs {
		rec, err := cfgrecord.Build(dev, f.configControl, actorID("config-profile"), "jamf", f.environment)
		if err != nil {
			return fmt.Errorf("build config-profile record %s: %w", dev.DeviceID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push config-profile %s: %w", dev.DeviceID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d config-profile records (mdm=jamf environment=%s)\n", pushed, f.environment)
	return nil
}
