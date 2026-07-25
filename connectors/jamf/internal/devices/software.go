package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/swinventory"
)

// SoftwareAPI is the narrow surface CollectSoftware depends on. The concrete
// implementation issues read-only Jamf Pro API calls (the APPLICATIONS
// inventory section); tests pass a fake. v0 reads the first bounded page;
// cursor pagination is a documented follow-on (threat-model D).
type SoftwareAPI interface {
	ListSoftware(ctx context.Context) ([]RawDeviceSoftware, error)
}

// RawSoftwareItem is the narrow, PII-bounded view the Jamf client returns for
// one installed application. The HTTP client maps the Jamf APPLICATIONS section
// into this shape, discarding file paths, app-usage telemetry, and license keys
// at the decode boundary. Tests construct it directly.
//
// There is intentionally no Path / UsageCount / LicenseKey field on this struct
// (P0-555).
type RawSoftwareItem struct {
	Name        string
	Version     string
	BundleID    string
	InstallDate string
}

// RawDeviceSoftware is one managed computer's id + its installed-software list.
type RawDeviceSoftware struct {
	ComputerID string
	Apps       []RawSoftwareItem
}

// CollectSoftware lists every visible managed computer's installed-software
// inventory and returns PII-bounded swinventory.RawDeviceSoftware ready for
// swinventory.Normalize. Separated from normalization so the cmd layer owns the
// observed-at clock.
func CollectSoftware(ctx context.Context, api SoftwareAPI) ([]swinventory.RawDeviceSoftware, error) {
	if api == nil {
		return nil, errors.New("devices: SoftwareAPI is nil")
	}
	raw, err := api.ListSoftware(ctx)
	if err != nil {
		return nil, fmt.Errorf("list jamf software: %w", err)
	}
	out := make([]swinventory.RawDeviceSoftware, 0, len(raw))
	for _, d := range raw {
		if strings.TrimSpace(d.ComputerID) == "" {
			continue
		}
		items := make([]swinventory.RawSoftwareItem, 0, len(d.Apps))
		for _, a := range d.Apps {
			items = append(items, swinventory.RawSoftwareItem{
				Name:        a.Name,
				Version:     a.Version,
				Identifier:  a.BundleID,
				InstallDate: a.InstallDate,
			})
		}
		out = append(out, swinventory.RawDeviceSoftware{
			DeviceID: d.ComputerID,
			Software: items,
		})
	}
	return out, nil
}
