package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/intune/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/intune/internal/intuneauth"
	"github.com/mgoodric/security-atlas/connectors/mdm/cfgprofile"
	"github.com/mgoodric/security-atlas/connectors/mdm/cfgrecord"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// Package-level seams for the config-profile run path, parallel to the
// device-posture + software seams. Tests swap in fakes for the Graph config-
// profile read + the sdk client constructor without hitting live Graph.
var (
	configProfileCollect = devices.CollectConfigProfiles
	newConfigProfileAPI  = func(cfg devices.ClientConfig) devices.ConfigProfileAPI {
		return devices.NewClient(cfg)
	}
)

type runConfigProfilesFlags struct {
	environment   string
	configControl string
	tenantID      string
}

func newRunConfigProfilesCmd() *cobra.Command {
	var f runConfigProfilesFlags
	cmd := &cobra.Command{
		Use:   "run-config-profiles",
		Short: "read Intune managed-device configuration-profile detail and push evidence records",
		Long: `Read Intune managed-device configuration-profile detail via the read-only
Microsoft Graph device-management API (GET /deviceManagement/managedDevices
$select=id,isEncrypted,complianceState with deviceConfigurationStates expanded to
id + displayName + state only), transform to endpoint.config_profile.v1 records,
and push to the platform.

This reports WHICH configuration / compliance profiles are deployed to a managed
device and what compliance-relevant settings they enforce — evidence for
configuration-management controls (SCF CFG-02 Secure Baseline Configurations /
CFG-04 Configuration Change Control) at a finer grain than the posture verdict.

Per-setting enrichment (slice 595): each assigned profile carries its allow-listed
profile_assignment_state (compliant / nonCompliant / conflict / error), and each
device carries a synthetic "Enforced Configuration Summary" profile whose settings
are the device-level enforced facts (disk_encryption_enforced from isEncrypted,
device_compliant from complianceState) — non-secret summaries, NEVER the raw
configuration payload.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: identical read-only credential to 'run' (INTUNE_TENANT_ID +
INTUNE_CLIENT_ID + INTUNE_CLIENT_SECRET; the Entra app must hold ONLY
DeviceManagementManagedDevices.Read.All). The isEncrypted + complianceState
properties are covered by that SAME read-only permission the posture 'run' uses —
NO new scope. The secret never appears in a log line or an evidence record.

SECRET-REDACTION BOUNDARY (P0-556): configuration profiles routinely embed
secrets — Wi-Fi PSKs, VPN shared secrets, certificate private keys, API tokens,
SCEP challenges, and raw payload blobs. NONE of these ever enters an evidence
record. Neither the device $select nor the deviceConfigurationStates expansion
carries the configuration's raw setting payload (only enforced-state metadata +
assignment state), and the per-profile settings field is restricted to a
compliance-relevant allow-list whose values are non-secret summaries.`,
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
	cmd.Flags().StringVar(&f.tenantID, "tenant-id", "", "Entra tenant id override (env: INTUNE_TENANT_ID)")
	return cmd
}

func doRunConfigProfiles(ctx context.Context, f runConfigProfilesFlags) error {
	cred, err := intuneauth.Resolve(intuneauth.ResolveOpts{TenantID: f.tenantID})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	api := newConfigProfileAPI(devices.ClientConfig{
		HTTP:         httpClient,
		TokenURL:     cred.TokenURL(),
		GraphBaseURL: cred.GraphBaseURL(),
		Scope:        cred.Scope(),
		ClientID:     cred.ClientID(),
		ClientSecret: cred.ClientSecret(),
	})
	raw, err := configProfileCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("intune config-profile collect: %w", err)
	}
	devs := cfgprofile.Normalize(devposture.MDMIntune, raw, nil)

	pushed := 0
	for _, dev := range devs {
		rec, err := cfgrecord.Build(dev, f.configControl, actorID("config-profile"), "intune", f.environment)
		if err != nil {
			return fmt.Errorf("build config-profile record %s: %w", dev.DeviceID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push config-profile %s: %w", dev.DeviceID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d config-profile records (mdm=intune environment=%s)\n", pushed, f.environment)
	return nil
}
