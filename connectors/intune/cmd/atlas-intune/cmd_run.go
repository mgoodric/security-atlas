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

	"github.com/mgoodric/security-atlas/connectors/intune/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/intune/internal/intuneauth"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/devrecord"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the Intune read + the sdk client constructor without
// hitting live Graph or a real platform endpoint. Production code paths are
// byte-for-byte unchanged; only the call-site indirection moved.
var (
	devicesCollect = devices.Collect
	newSDKClient   = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newDevicesAPI builds the live read-only Graph client; seamed so tests
	// inject a fake.
	newDevicesAPI = func(cfg devices.ClientConfig) devices.API {
		return devices.NewClient(cfg)
	}
)

// sdkPushClient is the narrow surface doRun consumes from sdk.Client.
type sdkPushClient interface {
	Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error)
	Close() error
}

type runFlags struct {
	environment   string
	deviceControl string
	tenantID      string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Intune managed-device compliance posture and push evidence records",
		Long: `Read Intune managed-device compliance posture via the read-only Microsoft
Graph device-management API (GET /deviceManagement/managedDevices, posture
$select only), transform to endpoint.device_posture.v1 records, and push to the
platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set INTUNE_TENANT_ID + INTUNE_CLIENT_ID + INTUNE_CLIENT_SECRET (the Entra
app must hold ONLY DeviceManagementManagedDevices.Read.All). The secret never
appears in a log line or an evidence record. The connector emits compliance
posture summary + the device->owner ASSIGNMENT identity only — never device
geolocation, the detectedApps inventory, device contents, or owner personal
contact detail.`,
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
	cmd.Flags().StringVar(&f.deviceControl, "device-control", "scf:END-04", "control_id to attach to endpoint.device_posture.v1 records")
	cmd.Flags().StringVar(&f.tenantID, "tenant-id", "", "Entra tenant id override (env: INTUNE_TENANT_ID)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
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

	api := newDevicesAPI(devices.ClientConfig{
		HTTP:         httpClient,
		TokenURL:     cred.TokenURL(),
		GraphBaseURL: cred.GraphBaseURL(),
		Scope:        cred.Scope(),
		ClientID:     cred.ClientID(),
		ClientSecret: cred.ClientSecret(),
	})
	raw, err := devicesCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("intune collect: %w", err)
	}
	devs := devposture.Normalize(devposture.MDMIntune, raw, nil)

	pushed := 0
	for _, dev := range devs {
		rec, err := devrecord.Build(dev, f.deviceControl, actorID("devices"), "intune", f.environment)
		if err != nil {
			return fmt.Errorf("build record %s: %w", dev.DeviceID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push device %s: %w", dev.DeviceID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (mdm=intune environment=%s)\n", pushed, f.environment)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
