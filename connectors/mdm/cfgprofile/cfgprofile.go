// Package cfgprofile is the shared normalization layer for the MDM connector
// family's configuration-profile detail evidence (Jamf + Intune, slice 556).
// Both MDMs' managed configuration / compliance profiles reduce to the same
// shape — a managed device plus a bounded list of the profiles assigned to it,
// each profile carrying its name, identifier, type/category, the assigned
// device-group scope, a UUID, a last-modified stamp, and a bounded list of
// compliance-relevant settings reported as non-secret summary values — so they
// share one evidence kind (endpoint.config_profile.v1) and one normalizer.
//
// This is the configuration-management sibling of the slice-490 posture summary
// (which reports WHETHER a device is compliant) and the slice-555 software
// inventory: it reports WHICH configuration / compliance profiles are deployed
// to a managed device and what compliance-relevant settings they enforce, as
// evidence for configuration-management controls (SCF CFG-02 Secure Baseline
// Configurations / CFG-04 Configuration Change Control).
//
// The load-bearing guard (P0-556 / threat-model I — SECRET REDACTION): a
// configuration profile routinely embeds SECRETS — Wi-Fi PSKs, VPN shared
// secrets, certificate private keys, API tokens, SCEP challenges, and arbitrary
// password / `<data>` payload values. A leak of any of these into the evidence
// ledger is the worst-case outcome. The type system is the first line of the
// redaction defence: a Setting carries only a Key + a non-secret summary Value,
// and the Key is drawn from a compliance-relevant ALLOW-LIST — there is nowhere
// to put a raw payload blob, a password, a private key, or a shared secret. The
// vendor clients request only profile METADATA + a settings summary at the API
// boundary (never the raw payload-content blob), and the record builder
// re-applies the settings allow-list. A secret reaching an evidence record would
// be a compile error at the type layer, a not-requested field at the API layer,
// and a dropped key at the builder layer.
package cfgprofile

import (
	"sort"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// MaxProfilesPerDevice bounds the per-device assigned-profile list. A managed
// endpoint rarely carries more than a few dozen configuration / compliance
// profiles; the cap keeps a single evidence record bounded (threat-model D,
// parallel to the slice-555 software bound) and is a defensive over-collection
// ceiling. Profiles beyond the cap are dropped after a stable sort, so the
// bound is deterministic.
const MaxProfilesPerDevice = 200

// MaxSettingsPerProfile bounds the per-profile compliance-relevant settings
// list. The allow-list (see AllowedSettingKeys) already caps the distinct keys;
// this is a defence-in-depth ceiling in case a source reports duplicate or
// unexpected keys.
const MaxSettingsPerProfile = 64

// AllowedSettingKeys is the compliance-relevant settings allow-list — the ONLY
// setting keys that may enter an evidence record. Every key names a posture /
// configuration control of interest to an auditor ("show the screen-lock policy
// that is enforced", "show the disk-encryption configuration"), and every value
// for these keys is a non-secret summary (a boolean, an enum, or a small
// integer rendered as a string). Keys that could carry a credential payload (a
// Wi-Fi password, a VPN shared secret, a certificate private key, a SCEP
// challenge, an API token) are NOT on this list, so they are dropped at
// normalization even if a source surfaces them. This is the allow-list half of
// the secret-redaction guard (P0-556); the deny-list (IsBannedSettingKey) is the
// belt-and-braces second check.
var AllowedSettingKeys = map[string]bool{
	"passcode_required":          true,
	"passcode_min_length":        true,
	"passcode_complexity":        true,
	"disk_encryption_enforced":   true,
	"firewall_enabled":           true,
	"screen_lock_enforced":       true,
	"screen_lock_grace_seconds":  true,
	"auto_update_enabled":        true,
	"gatekeeper_enabled":         true,
	"os_min_version_enforced":    true,
	"remote_lock_enabled":        true,
	"usb_restricted":             true,
	"bluetooth_sharing_disabled": true,
}

// bannedSettingSubstrings names substrings that, if present in a setting key,
// indicate a credential / secret payload that must NEVER enter an evidence
// record. The deny-list runs in addition to the allow-list: a key must be on the
// allow-list AND contain none of these substrings to survive normalization.
var bannedSettingSubstrings = []string{
	"password", "passwd", "secret", "psk", "privatekey", "private_key",
	"sharedsecret", "shared_secret", "token", "credential", "challenge",
	"payloadcontent", "payload_content", "certificate", "cert_data",
	"data", "key_data", "apikey", "api_key", "pin",
}

// IsBannedSettingKey reports whether a setting key contains a credential / secret
// substring (case-insensitive). A banned key is dropped at normalization
// regardless of the allow-list. Exported so the record builder + tests can share
// the single source of truth.
func IsBannedSettingKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, b := range bannedSettingSubstrings {
		if strings.Contains(k, b) {
			return true
		}
	}
	return false
}

// RawSetting is the narrow, secret-bounded view a vendor client returns for one
// compliance-relevant profile setting. The vendor clients map their API
// response into this shape, discarding raw payload-content blobs and any
// credential-bearing payload field at the decode boundary. Tests construct it
// directly.
//
// There is intentionally NO field on this struct for a raw payload blob, a
// password, a private key, a shared secret, or a SCEP challenge (P0-556). The
// Value is always a non-secret summary string.
type RawSetting struct {
	// Key is the compliance-relevant setting key (e.g. "passcode_required").
	// Filtered against AllowedSettingKeys + IsBannedSettingKey at normalization.
	Key string
	// Value is the non-secret summary value (e.g. "true", "enforced", "8"). NEVER
	// a credential value.
	Value string
}

// RawProfile is the narrow, secret-bounded view a vendor client returns for one
// configuration / compliance profile assigned to a device. There is no field for
// a raw payload-content blob, a credential, or owner contact detail (P0-556).
type RawProfile struct {
	// Name is the profile display name (required; empty profiles are dropped).
	Name string
	// Identifier is the profile identifier (Jamf payload identifier / Intune
	// policy id). Opaque, non-secret.
	Identifier string
	// ProfileType is the profile type / category (e.g. "configuration",
	// "compliance", "Restrictions", "FileVault"). Descriptive metadata.
	ProfileType string
	// Scope is the assigned device-group / assignment names (which groups the
	// profile is deployed to). Group NAMES only — never owner contact detail.
	Scope []string
	// UUID is the profile UUID the source reports. Opaque, non-secret.
	UUID string
	// LastModified is the profile's last-modified / deployed timestamp the source
	// reports. Descriptive.
	LastModified string
	// Settings is the pre-filter compliance-relevant settings list. Filtered
	// against the allow-list + deny-list at normalization.
	Settings []RawSetting
}

// RawDeviceProfiles is the narrow per-device view a vendor client returns: the
// managed device's stable id plus its assigned configuration / compliance
// profiles. There is no field for geolocation, device contents, or owner contact
// detail (P0-556).
type RawDeviceProfiles struct {
	// DeviceID is the MDM-native stable identifier (opaque, non-secret). Joins to
	// the device's endpoint.device_posture.v1 record.
	DeviceID string
	// Profiles is the device's assigned-profile list (pre-bound, pre-sort).
	Profiles []RawProfile
}

// Setting is the normalized per-setting shape the record builder emits. Field
// names map 1:1 to the schema's settings[] item.
type Setting struct {
	Key   string
	Value string
}

// Profile is the normalized per-profile shape the record builder emits. Field
// names map 1:1 to the schema's profiles[] item.
type Profile struct {
	Name         string
	Identifier   string
	ProfileType  string
	Scope        []string
	UUID         string
	LastModified string
	Settings     []Setting
}

// Device is the normalized per-device config-profile record the cmd layer turns
// into an evidence record. Field names map 1:1 to the endpoint.config_profile.v1
// schema.
type Device struct {
	SourceMDM  devposture.MDM
	DeviceID   string
	Profiles   []Profile
	ObservedAt time.Time
}

// Normalize converts a vendor's raw per-device profile lists into normalized
// Devices, stamping the source MDM + a single observed-at. now is injectable for
// deterministic tests (nil -> time.Now UTC). Devices missing an id are dropped
// (the schema requires it). Profiles missing a name are dropped (the schema
// requires it). Each device's profile list is stable-sorted (by name, then
// identifier) and bounded to MaxProfilesPerDevice; each profile's settings are
// filtered against the allow-list + deny-list, stable-sorted by key, and bounded
// to MaxSettingsPerProfile.
func Normalize(mdm devposture.MDM, raw []RawDeviceProfiles, now func() time.Time) []Device {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	observedAt := now().UTC().Truncate(time.Hour)
	out := make([]Device, 0, len(raw))
	for _, d := range raw {
		id := strings.TrimSpace(d.DeviceID)
		if id == "" {
			continue
		}
		out = append(out, Device{
			SourceMDM:  mdm,
			DeviceID:   id,
			Profiles:   normalizeProfiles(d.Profiles),
			ObservedAt: observedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

func normalizeProfiles(raw []RawProfile) []Profile {
	profiles := make([]Profile, 0, len(raw))
	for _, p := range raw {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		profiles = append(profiles, Profile{
			Name:         name,
			Identifier:   strings.TrimSpace(p.Identifier),
			ProfileType:  strings.TrimSpace(p.ProfileType),
			Scope:        normalizeScope(p.Scope),
			UUID:         strings.TrimSpace(p.UUID),
			LastModified: strings.TrimSpace(p.LastModified),
			Settings:     normalizeSettings(p.Settings),
		})
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Name != profiles[j].Name {
			return profiles[i].Name < profiles[j].Name
		}
		return profiles[i].Identifier < profiles[j].Identifier
	})
	if len(profiles) > MaxProfilesPerDevice {
		profiles = profiles[:MaxProfilesPerDevice]
	}
	return profiles
}

func normalizeScope(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// normalizeSettings is the load-bearing secret-redaction filter. A setting
// survives ONLY when its key is on the AllowedSettingKeys allow-list AND is not
// flagged by IsBannedSettingKey. Everything else — including any credential
// payload a source might surface — is dropped.
func normalizeSettings(raw []RawSetting) []Setting {
	settings := make([]Setting, 0, len(raw))
	for _, s := range raw {
		key := strings.TrimSpace(s.Key)
		if key == "" {
			continue
		}
		if !AllowedSettingKeys[key] {
			continue
		}
		if IsBannedSettingKey(key) {
			continue
		}
		settings = append(settings, Setting{Key: key, Value: strings.TrimSpace(s.Value)})
	}
	sort.Slice(settings, func(i, j int) bool { return settings[i].Key < settings[j].Key })
	if len(settings) > MaxSettingsPerProfile {
		settings = settings[:MaxSettingsPerProfile]
	}
	return settings
}
