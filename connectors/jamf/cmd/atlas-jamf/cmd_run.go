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

	"github.com/mgoodric/security-atlas/connectors/jamf/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/jamf/internal/jamfauth"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/devrecord"
)

// Package-level seams: doRun reaches through these function variables so tests
// can swap in fakes for the Jamf read + the sdk client constructor without
// hitting live Jamf or a real platform endpoint. Production code paths are
// byte-for-byte unchanged; only the call-site indirection moved.
var (
	devicesCollect = devices.Collect
	newSDKClient   = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	// newDevicesAPI builds the live read-only HTTP client; seamed so tests
	// inject a fake.
	newDevicesAPI = func(hc *http.Client, baseURL, clientID, clientSecret string) devices.API {
		return devices.NewClient(hc, baseURL, clientID, clientSecret)
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
	baseURL       string
}

func newRunCmd() *cobra.Command {
	var f runFlags
	cmd := &cobra.Command{
		Use:   "run",
		Short: "read Jamf managed-computer posture and push evidence records",
		Long: `Read Jamf managed-computer posture via the read-only Jamf Pro API
(GET /api/v1/computers-inventory, posture-relevant sections only), transform to
endpoint.device_posture.v1 records, and push to the platform.

Profile: pull. One bounded read-and-push pass per invocation; operator-scheduled
(recommended 24h). NOT continuous monitoring.

Auth: set JAMF_BASE_URL + JAMF_CLIENT_ID + JAMF_CLIENT_SECRET (the API client
must be bound to a read-only API role). The secret never appears in a log line
or an evidence record. The connector emits posture summary + the device->owner
ASSIGNMENT identity only — never device geolocation, installed-app inventory,
device contents, or owner personal contact detail.`,
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
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "Jamf Pro instance URL override (env: JAMF_BASE_URL)")
	return cmd
}

func doRun(ctx context.Context, f runFlags) error {
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

	api := newDevicesAPI(httpClient, cred.BaseURL(), cred.ClientID(), cred.ClientSecret())
	raw, err := devicesCollect(ctx, api)
	if err != nil {
		return fmt.Errorf("jamf collect: %w", err)
	}
	devs := devposture.Normalize(devposture.MDMJamf, raw, nil)

	pushed := 0
	for _, dev := range devs {
		rec, err := devrecord.Build(dev, f.deviceControl, actorID("devices"), "jamf", f.environment)
		if err != nil {
			return fmt.Errorf("build record %s: %w", dev.DeviceID, err)
		}
		if err := pushOne(ctx, sdkClient, rec); err != nil {
			return fmt.Errorf("push device %s: %w", dev.DeviceID, err)
		}
		pushed++
	}

	fmt.Printf("pushed %d records (mdm=jamf environment=%s)\n", pushed, f.environment)
	return nil
}

func pushOne(ctx context.Context, client sdkPushClient, rec *evidencev1.EvidenceRecord) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := client.Push(pctx, rec)
	return err
}
