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
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/swinventory"
	"github.com/mgoodric/security-atlas/connectors/mdm/swrecord"
)

// Package-level seams for the software-inventory run path, parallel to the
// device-posture seams in cmd_run.go. Tests swap in fakes for the Graph software
// read + the sdk client constructor without hitting live Graph.
var (
	softwareCollect = devices.CollectSoftware
	newSoftwareAPI  = func(cfg devices.ClientConfig) devices.SoftwareAPI {
		return devices.NewClient(cfg)
	}
)

type runSoftwareFlags struct {
	environment     string
	softwareControl string
	tenantID        string
}

func newRunSoftwareCmd() *cobra.Command {
	var f runSoftwareFlags
	cmd := &cobra.Command{
		Use:   "run-software",
		Short: "read Intune managed-device detected-software inventory and push evidence records",
		Long: `Read Intune detected-software inventory via the read-only Microsoft Graph
device-management API (GET /deviceManagement/detectedApps, displayName + version
+ id $select only, managedDevices expanded to id only), transform to
endpoint.software_inventory.v1 records, and push to the platform.

This is the deliberate slice-490 over-collection follow-on (slice 555): the
detectedApps inventory excluded from the posture summary is collected here as a
SEPARATE, scoped evidence kind for patch-/vulnerability-management compliance.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: identical read-only credential to 'run' (INTUNE_TENANT_ID +
INTUNE_CLIENT_ID + INTUNE_CLIENT_SECRET; the Entra app must hold ONLY
DeviceManagementManagedDevices.Read.All). The secret never appears in a log line
or an evidence record. Each software item carries the app name + version + Graph
app id ONLY — never executable file paths, per-user app-usage telemetry, license
keys, device contents, or owner personal contact detail.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doRunSoftware(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.softwareControl, "software-control", "scf:VPM-04", "control_id to attach to endpoint.software_inventory.v1 records")
	cmd.Flags().StringVar(&f.tenantID, "tenant-id", "", "Entra tenant id override (env: INTUNE_TENANT_ID)")
	return cmd
}

func doRunSoftware(ctx context.Context, f runSoftwareFlags) error {
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

	api := newSoftwareAPI(devices.ClientConfig{
		HTTP:         httpClient,
		TokenURL:     cred.TokenURL(),
		GraphBaseURL: cred.GraphBaseURL(),
		Scope:        cred.Scope(),
		ClientID:     cred.ClientID(),
		ClientSecret: cred.ClientSecret(),
	})
	raw, err := softwareCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("intune software collect: %w", err)
	}
	devs := swinventory.Normalize(devposture.MDMIntune, raw, nil)

	pushed := 0
	for _, dev := range devs {
		rec, err := swrecord.Build(dev, f.softwareControl, actorID("software"), "intune", f.environment)
		if err != nil {
			return fmt.Errorf("build software record %s: %w", dev.DeviceID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push software %s: %w", dev.DeviceID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d software-inventory records (mdm=intune environment=%s)\n", pushed, f.environment)
	return nil
}
