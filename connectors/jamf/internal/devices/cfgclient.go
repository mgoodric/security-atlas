package devices

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// configProfileSections is the EXACT set of inventory sections the config-profile
// read requests: GENERAL (for the stable computer id + supervised/managed
// hardening facts) + CONFIGURATION_PROFILES (the assigned-profile metadata:
// display name, identifier, uuid, last-installed) + the posture sections
// OPERATING_SYSTEM / DISK_ENCRYPTION / SECURITY, which carry the device's
// effective ENFORCED compliance state (FileVault status, Gatekeeper status,
// screen-lock-grace enforcement). The GPS location, USER_AND_LOCATION (owner
// contact), APPLICATIONS, and every other section are deliberately NOT requested
// (P0-556).
//
// The OPERATING_SYSTEM / DISK_ENCRYPTION / SECURITY sections are the SAME
// posture-relevant inventory sections the slice-490 posture read already
// requests under the same read-only "Read Computers" role — adding them here is
// NOT a scope widening (the credential already grants them); it is the
// metadata-grade source of the per-setting enrichment (slice 595). The
// per-setting values are derived from these ENFORCED-STATE summary fields, NOT
// from the raw configuration-profile payload.
//
// THE LOAD-BEARING SECRET-REDACTION BOUNDARY (P0-556): no requested section
// carries the raw configuration-profile <data>/PayloadContent blob — neither
// CONFIGURATION_PROFILES (metadata only) nor the posture sections (effective
// enforced-state summary only). The connector therefore never receives a Wi-Fi
// PSK, VPN shared secret, certificate private key, or SCEP challenge from this
// read. The structs below have no field for any such payload; json.Decode
// discards JSON keys with no matching struct field, so a secret cannot enter
// memory as connector data, and the cfgprofile allow-list + deny-list drop any
// non-allow-listed setting key at normalization regardless.
var configProfileSections = []string{
	"GENERAL", "CONFIGURATION_PROFILES", "OPERATING_SYSTEM", "DISK_ENCRYPTION", "SECURITY",
}

// apiConfigProfilePage is the minimal Jamf computers-inventory JSON shape for the
// config-profile read — the computer id, each assigned profile's metadata, and
// the effective enforced-state summary fields (general supervised/managed, disk
// encryption status, security/Gatekeeper status). The struct has no field for the
// raw payload blob or any credential, regardless of what the source returns
// (P0-556).
type apiConfigProfilePage struct {
	Results []struct {
		ID      string `json:"id"`
		General struct {
			Supervised       bool `json:"supervised"`
			Managed          bool `json:"managed"`
			RemoteManagement struct {
				Managed bool `json:"managed"`
			} `json:"remoteManagement"`
		} `json:"general"`
		ConfigurationProfiles []struct {
			DisplayName       string `json:"displayName"`
			ProfileIdentifier string `json:"profileIdentifier"`
			UUID              string `json:"uuid"`
			LastInstalled     string `json:"lastInstalled"`
		} `json:"configurationProfiles"`
		DiskEncryption struct {
			FileVault2State string `json:"individualRecoveryKeyValidityStatus"`
			BootEncrypted   string `json:"fileVault2Status"`
		} `json:"diskEncryption"`
		Security struct {
			ScreenLockEnforced string `json:"screenLockGracePeriodEnforced"`
			GatekeeperStatus   string `json:"gatekeeperStatus"`
		} `json:"security"`
	} `json:"results"`
}

// enforcedSummaryProfileName is the synthetic profile under which the per-device
// enforced-state hardening settings are reported. It is a connector-derived
// roll-up of the device's effective compliance state (NOT a literal Jamf profile),
// so the operator sees the enforced disk-encryption / Gatekeeper / screen-lock /
// supervision facts as compliance-relevant settings without the connector ever
// reading a raw profile payload.
const enforcedSummaryProfileName = "Enforced Configuration Summary"

// ListConfigProfiles reads the first bounded page of computer inventory limited
// to the GENERAL + CONFIGURATION_PROFILES + posture sections. Read-only: a single
// GET against /api/v1/computers-inventory under the existing read-only role.
//
// Per-setting enrichment (slice 595): the CONFIGURATION_PROFILES section reports
// WHICH profiles are deployed (metadata only — no per-setting payload), so each
// assigned profile carries no settings. The per-device enforced hardening state
// (FileVault, Gatekeeper, screen-lock-grace, supervision/management) is projected
// onto a single synthetic "Enforced Configuration Summary" profile through the
// cfgprofile allow-list — non-secret boolean summaries derived from the
// posture-section inventory fields, NEVER the raw profile payload.
func (c *Client) ListConfigProfiles(ctx context.Context) ([]RawDeviceConfigProfiles, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, s := range configProfileSections {
		q.Add("section", s)
	}
	q.Set("page", "0")
	q.Set("page-size", strconv.Itoa(pageLimit))
	var page apiConfigProfilePage
	if err := c.getJSON(ctx, "/api/v1/computers-inventory?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	out := make([]RawDeviceConfigProfiles, 0, len(page.Results))
	for _, r := range page.Results {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		profiles := make([]RawConfigProfile, 0, len(r.ConfigurationProfiles)+1)
		for _, p := range r.ConfigurationProfiles {
			profiles = append(profiles, RawConfigProfile{
				Name:         strings.TrimSpace(p.DisplayName),
				Identifier:   strings.TrimSpace(p.ProfileIdentifier),
				ProfileType:  "configuration",
				UUID:         strings.TrimSpace(p.UUID),
				LastModified: strings.TrimSpace(p.LastInstalled),
			})
		}
		// Project the effective enforced-state hardening facts onto a synthetic
		// summary profile. Values are non-secret booleans; the keys are
		// allow-listed in cfgprofile.AllowedSettingKeys.
		summary := RawConfigProfile{
			Name:        enforcedSummaryProfileName,
			ProfileType: "compliance",
			Settings: []RawConfigSetting{
				{Key: "disk_encryption_enforced", Value: boolStr(isFileVaultOn(r.DiskEncryption.BootEncrypted, r.DiskEncryption.FileVault2State))},
				{Key: "gatekeeper_enabled", Value: boolStr(isGatekeeperOn(r.Security.GatekeeperStatus))},
				{Key: "screen_lock_enforced", Value: boolStr(isScreenLockEnforced(r.Security.ScreenLockEnforced))},
				{Key: "device_supervised", Value: boolStr(r.General.Supervised)},
				{Key: "device_managed", Value: boolStr(r.General.Managed || r.General.RemoteManagement.Managed)},
			},
		}
		profiles = append(profiles, summary)
		out = append(out, RawDeviceConfigProfiles{ComputerID: id, Profiles: profiles})
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

// isGatekeeperOn reports whether the Jamf SECURITY.gatekeeperStatus value
// indicates Gatekeeper is enforcing. Jamf reports "App Store and identified
// developers" / "App Store" when on, "Anywhere"/"Disabled" when off.
func isGatekeeperOn(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return false
	}
	return strings.Contains(s, "app store")
}

// isScreenLockEnforced reports whether the SECURITY.screenLockGracePeriodEnforced
// value indicates a screen-lock grace policy is enforced (any non-empty,
// non-"NOT_ENFORCED" value), mirroring the slice-490 posture mapping.
func isScreenLockEnforced(v string) bool {
	s := strings.ToUpper(strings.TrimSpace(v))
	return s != "" && s != "NOT_ENFORCED"
}
