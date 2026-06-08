package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/swinventory"
)

// SoftwareAPI is the narrow surface CollectSoftware depends on. The concrete
// implementation issues read-only Graph API calls (the detectedApps endpoint);
// tests pass a fake. v0 reads the first bounded page; cursor pagination
// (@odata.nextLink) is a documented follow-on (threat-model D).
type SoftwareAPI interface {
	ListDetectedApps(ctx context.Context) ([]RawDeviceSoftware, error)
}

// RawSoftwareItem is the narrow, PII-bounded view the Graph client returns for
// one detected application. The HTTP client maps the detectedApps response into
// this shape, discarding device size, platform-extra, and any usage telemetry at
// the decode boundary. Tests construct it directly.
//
// There is intentionally no Path / SizeInByte / UsageCount / LicenseKey field on
// this struct (P0-555).
type RawSoftwareItem struct {
	Name    string
	Version string
	// AppID is the Graph detectedApp resource id — a stable cross-device app
	// identifier (NOT a file path).
	AppID string
}

// RawDeviceSoftware is one Intune managed device's id + its detected-software
// list. The Graph detectedApps graph is app-centric (app -> managedDevices); the
// client inverts it to this device-centric shape.
type RawDeviceSoftware struct {
	DeviceID string
	Apps     []RawSoftwareItem
}

// CollectSoftware lists detected apps per managed device and returns PII-bounded
// swinventory.RawDeviceSoftware ready for swinventory.Normalize. Separated from
// normalization so the cmd layer owns the observed-at clock.
func CollectSoftware(ctx context.Context, api SoftwareAPI) ([]swinventory.RawDeviceSoftware, error) {
	if api == nil {
		return nil, errors.New("devices: SoftwareAPI is nil")
	}
	raw, err := api.ListDetectedApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list intune detected apps: %w", err)
	}
	out := make([]swinventory.RawDeviceSoftware, 0, len(raw))
	for _, d := range raw {
		if strings.TrimSpace(d.DeviceID) == "" {
			continue
		}
		items := make([]swinventory.RawSoftwareItem, 0, len(d.Apps))
		for _, a := range d.Apps {
			items = append(items, swinventory.RawSoftwareItem{
				Name:       a.Name,
				Version:    a.Version,
				Identifier: a.AppID,
			})
		}
		out = append(out, swinventory.RawDeviceSoftware{
			DeviceID: d.DeviceID,
			Software: items,
		})
	}
	return out, nil
}
