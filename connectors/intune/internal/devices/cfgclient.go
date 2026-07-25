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

// configDeviceSelect is the EXACT set of managed-device properties the
// config-profile read requests at the device level: the stable id plus the
// effective enforced-state hardening facts (isEncrypted, complianceState). These
// are read-only properties already covered by the existing
// DeviceManagementManagedDevices.Read.All permission (the slice-490 posture read
// $selects the same isEncrypted + complianceState) — adding them here is NOT a
// scope widening. deviceName / userPrincipalName / phoneNumber / emailAddress and
// every other owner-contact / GPS field are deliberately NOT requested (P0-556).
var configDeviceSelect = []string{"id", "isEncrypted", "complianceState"}

// apiConfigStatePage is the minimal Graph managed-devices + expanded
// deviceConfigurationStates JSON shape — each device id + its effective
// enforced-state hardening facts, plus each assigned configuration profile's id,
// display name, and assignment state ONLY.
//
// THE LOAD-BEARING SECRET-REDACTION BOUNDARY (P0-556): neither the device-level
// $select nor the deviceConfigurationStates expansion carries the configuration's
// raw setting payload — only enforced-state metadata (isEncrypted /
// complianceState) and per-profile assignment state. The connector never even
// receives a Wi-Fi PSK, VPN shared secret, certificate private key, or SCEP
// challenge from this read. This struct has no field for any such payload;
// json.Decode discards JSON keys with no matching struct field, so a secret
// cannot enter memory as connector data.
type apiConfigStatePage struct {
	Value []struct {
		ID                        string `json:"id"`
		IsEncrypted               bool   `json:"isEncrypted"`
		ComplianceState           string `json:"complianceState"`
		DeviceConfigurationStates []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			State       string `json:"state"`
		} `json:"deviceConfigurationStates"`
	} `json:"value"`
}

// enforcedSummaryProfileName is the synthetic profile under which the per-device
// enforced-state hardening settings are reported. It is a connector-derived
// roll-up of the device's effective compliance state (NOT a literal Intune
// configuration policy), so the operator sees the enforced disk-encryption +
// overall-compliance facts as compliance-relevant settings without the connector
// ever reading a raw configuration payload.
const enforcedSummaryProfileName = "Enforced Configuration Summary"

// ListConfigProfiles reads the first bounded page of managed devices with their
// expanded deviceConfigurationStates and returns the device-centric assigned-
// profile shape cfgprofile expects. Read-only: a single GET against
// /deviceManagement/managedDevices with an expanded, $select'd
// deviceConfigurationStates, under the existing read-only permission.
//
// Per-setting enrichment (slice 595): each assigned configuration profile's
// assignment state ("compliant" / "nonCompliant" / "conflict" / "error") is
// surfaced as the allow-listed non-secret "profile_assignment_state" setting; the
// device-level enforced facts (isEncrypted, complianceState) are projected onto a
// single synthetic "Enforced Configuration Summary" profile as the allow-listed
// "disk_encryption_enforced" + "device_compliant" settings. All values are
// non-secret summaries derived from enforced-state metadata, NEVER the raw
// configuration payload, and every key passes the cfgprofile allow-list.
func (c *Client) ListConfigProfiles(ctx context.Context) ([]RawDeviceConfigProfiles, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	// $select only the device id + enforced-state hardening facts (never
	// deviceName / userPrincipalName / owner contact detail).
	q.Set("$select", strings.Join(configDeviceSelect, ","))
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
		profiles := make([]RawConfigProfile, 0, len(d.DeviceConfigurationStates)+1)
		for _, s := range d.DeviceConfigurationStates {
			name := strings.TrimSpace(s.DisplayName)
			if name == "" {
				continue
			}
			p := RawConfigProfile{
				Name:        name,
				Identifier:  strings.TrimSpace(s.ID),
				ProfileType: "configuration",
			}
			if state := normalizeAssignmentState(s.State); state != "" {
				p.Settings = []RawConfigSetting{{Key: "profile_assignment_state", Value: state}}
			}
			profiles = append(profiles, p)
		}
		// Project the device-level enforced facts onto a synthetic summary profile.
		summary := RawConfigProfile{
			Name:        enforcedSummaryProfileName,
			ProfileType: "compliance",
			Settings: []RawConfigSetting{
				{Key: "disk_encryption_enforced", Value: boolStr(d.IsEncrypted)},
				{Key: "device_compliant", Value: boolStr(strings.EqualFold(d.ComplianceState, "compliant"))},
			},
		}
		profiles = append(profiles, summary)
		out = append(out, RawDeviceConfigProfiles{DeviceID: id, Profiles: profiles})
	}
	return out, nil
}

// boolStr renders a boolean hardening fact as the non-secret summary value form
// the schema + allow-list expect ("true"/"false").
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// normalizeAssignmentState normalizes the Graph deviceConfigurationState.state
// enum to a lowercased non-secret summary value. Unknown / empty states yield ""
// (no setting emitted). The value is a state enum, never a credential.
func normalizeAssignmentState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "compliant":
		return "compliant"
	case "noncompliant", "non_compliant":
		return "nonCompliant"
	case "conflict":
		return "conflict"
	case "error":
		return "error"
	case "notapplicable", "not_applicable":
		return "notApplicable"
	default:
		return ""
	}
}
