package devices

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// configStateSelect is the EXACT set of deviceConfigurationState properties the
// connector requests: the assigned configuration-profile id + displayName +
// state ONLY. No payload, no setting values, no owner detail (P0-556).
var configStateSelect = []string{"id", "displayName", "state"}

// apiConfigStatePage is the minimal Graph managed-devices + expanded
// deviceConfigurationStates JSON shape — each device id plus each assigned
// configuration profile's id, display name, and assignment state ONLY.
//
// THE LOAD-BEARING SECRET-REDACTION BOUNDARY (P0-556): the
// deviceConfigurationStates expansion reports profile ASSIGNMENT METADATA only —
// it does NOT carry the configuration's raw setting payload, so the connector
// never even receives a Wi-Fi PSK, VPN shared secret, certificate private key,
// or SCEP challenge from this read. This struct has no field for any such
// payload; json.Decode discards JSON keys with no matching struct field, so a
// secret cannot enter memory as connector data.
type apiConfigStatePage struct {
	Value []struct {
		ID                        string `json:"id"`
		DeviceConfigurationStates []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			State       string `json:"state"`
		} `json:"deviceConfigurationStates"`
	} `json:"value"`
}

// ListConfigProfiles reads the first bounded page of managed devices with their
// expanded deviceConfigurationStates and returns the device-centric assigned-
// profile shape cfgprofile expects. Read-only: a single GET against
// /deviceManagement/managedDevices with an expanded, $select'd
// deviceConfigurationStates.
//
// The state ("compliant" / "nonCompliant" / "conflict" etc.) is surfaced as a
// non-secret compliance-relevant summary setting (key "screen_lock_enforced" is
// NOT inferred — the raw configuration-state value is descriptive metadata, not
// a per-setting key; v0 emits no settings, mirroring Jamf's metadata-only read).
// The per-profile settings field stays empty at v0; a richer per-setting read is
// a documented follow-on through the SAME allow-list guard.
func (c *Client) ListConfigProfiles(ctx context.Context) ([]RawDeviceConfigProfiles, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	// $select only the device id (never deviceName / userPrincipalName / owner
	// contact detail).
	q.Set("$select", "id")
	q.Set("$expand", "deviceConfigurationStates($select="+strings.Join(configStateSelect, ",")+")")
	q.Set("$top", strconv.Itoa(pageLimit))
	var page apiConfigStatePage
	if err := c.getJSON(ctx, "/deviceManagement/managedDevices?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	out := make([]RawDeviceConfigProfiles, 0, len(page.Value))
	for _, d := range page.Value {
		id := strings.TrimSpace(d.ID)
		if id == "" {
			continue
		}
		profiles := make([]RawConfigProfile, 0, len(d.DeviceConfigurationStates))
		for _, s := range d.DeviceConfigurationStates {
			name := strings.TrimSpace(s.DisplayName)
			if name == "" {
				continue
			}
			profiles = append(profiles, RawConfigProfile{
				Name:        name,
				Identifier:  strings.TrimSpace(s.ID),
				ProfileType: "configuration",
			})
		}
		out = append(out, RawDeviceConfigProfiles{DeviceID: id, Profiles: profiles})
	}
	return out, nil
}
