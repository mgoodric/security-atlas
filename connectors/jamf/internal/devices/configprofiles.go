package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mgoodric/security-atlas/connectors/mdm/cfgprofile"
)

// ConfigProfileAPI is the narrow surface CollectConfigProfiles depends on. The
// concrete implementation issues read-only Jamf Pro API calls (the
// CONFIGURATION_PROFILES inventory section); tests pass a fake. v0 reads the
// first bounded page; cursor pagination is a documented follow-on
// (threat-model D).
type ConfigProfileAPI interface {
	ListConfigProfiles(ctx context.Context) ([]RawDeviceConfigProfiles, error)
}

// RawConfigSetting is the narrow, secret-bounded view the Jamf client returns
// for one compliance-relevant profile setting. There is intentionally NO field
// for a raw payload blob, a Wi-Fi PSK, a VPN shared secret, a private key, or a
// SCEP challenge (P0-556) — only an allow-listed key + a non-secret summary
// value.
type RawConfigSetting struct {
	Key   string
	Value string
}

// RawConfigProfile is the narrow, secret-bounded view the Jamf client returns
// for one configuration / compliance profile assigned to a computer. There is no
// field for a raw payload-content blob, a credential, or owner contact detail
// (P0-556).
type RawConfigProfile struct {
	Name         string
	Identifier   string
	ProfileType  string
	Scope        []string
	UUID         string
	LastModified string
	Settings     []RawConfigSetting
}

// RawDeviceConfigProfiles is one managed computer's id + its assigned
// configuration / compliance profiles.
type RawDeviceConfigProfiles struct {
	ComputerID string
	Profiles   []RawConfigProfile
}

// CollectConfigProfiles lists every visible managed computer's assigned
// configuration / compliance profiles and returns secret-bounded
// cfgprofile.RawDeviceProfiles ready for cfgprofile.Normalize. Separated from
// normalization so the cmd layer owns the observed-at clock.
func CollectConfigProfiles(ctx context.Context, api ConfigProfileAPI) ([]cfgprofile.RawDeviceProfiles, error) {
	if api == nil {
		return nil, errors.New("devices: ConfigProfileAPI is nil")
	}
	raw, err := api.ListConfigProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list jamf config profiles: %w", err)
	}
	out := make([]cfgprofile.RawDeviceProfiles, 0, len(raw))
	for _, d := range raw {
		if strings.TrimSpace(d.ComputerID) == "" {
			continue
		}
		profiles := make([]cfgprofile.RawProfile, 0, len(d.Profiles))
		for _, p := range d.Profiles {
			settings := make([]cfgprofile.RawSetting, 0, len(p.Settings))
			for _, s := range p.Settings {
				settings = append(settings, cfgprofile.RawSetting{Key: s.Key, Value: s.Value})
			}
			profiles = append(profiles, cfgprofile.RawProfile{
				Name:         p.Name,
				Identifier:   p.Identifier,
				ProfileType:  p.ProfileType,
				Scope:        p.Scope,
				UUID:         p.UUID,
				LastModified: p.LastModified,
				Settings:     settings,
			})
		}
		out = append(out, cfgprofile.RawDeviceProfiles{
			DeviceID: d.ComputerID,
			Profiles: profiles,
		})
	}
	return out, nil
}
