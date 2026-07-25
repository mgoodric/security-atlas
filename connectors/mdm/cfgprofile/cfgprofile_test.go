package cfgprofile

import (
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

func fixedNow() time.Time { return time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC) }

func TestNormalize_StampsMDMAndTruncatesObservedAtToHour(t *testing.T) {
	t.Parallel()
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{
		{DeviceID: "d-1", Profiles: []RawProfile{{Name: "Passcode"}}},
	}, fixedNow)
	if len(devs) != 1 {
		t.Fatalf("devices = %d; want 1", len(devs))
	}
	if devs[0].SourceMDM != devposture.MDMJamf {
		t.Errorf("source mdm = %q", devs[0].SourceMDM)
	}
	want := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if !devs[0].ObservedAt.Equal(want) {
		t.Errorf("observed_at = %v; want %v", devs[0].ObservedAt, want)
	}
}

func TestNormalize_DropsDevicesMissingID(t *testing.T) {
	t.Parallel()
	devs := Normalize(devposture.MDMIntune, []RawDeviceProfiles{
		{DeviceID: "  ", Profiles: []RawProfile{{Name: "X"}}},
		{DeviceID: "keep", Profiles: []RawProfile{{Name: "Y"}}},
	}, fixedNow)
	if len(devs) != 1 || devs[0].DeviceID != "keep" {
		t.Fatalf("got %+v; want only keep", devs)
	}
}

func TestNormalize_DropsProfilesMissingName(t *testing.T) {
	t.Parallel()
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{
		{DeviceID: "d", Profiles: []RawProfile{{Name: ""}, {Name: "Real"}}},
	}, fixedNow)
	if len(devs[0].Profiles) != 1 || devs[0].Profiles[0].Name != "Real" {
		t.Fatalf("profiles = %+v; want only Real", devs[0].Profiles)
	}
}

func TestNormalize_SortsProfilesAndDevices(t *testing.T) {
	t.Parallel()
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{
		{DeviceID: "b", Profiles: []RawProfile{{Name: "Zeta"}, {Name: "Alpha"}}},
		{DeviceID: "a", Profiles: []RawProfile{{Name: "Mid"}}},
	}, fixedNow)
	if devs[0].DeviceID != "a" || devs[1].DeviceID != "b" {
		t.Errorf("device order = %q,%q; want a,b", devs[0].DeviceID, devs[1].DeviceID)
	}
	if devs[1].Profiles[0].Name != "Alpha" || devs[1].Profiles[1].Name != "Zeta" {
		t.Errorf("profile order = %+v; want Alpha,Zeta", devs[1].Profiles)
	}
}

// TestNormalize_DropsSecretBearingSettings is the load-bearing secret-redaction
// guard at the normalizer: any setting whose key is off the compliance-relevant
// allow-list OR is flagged credential-bearing is dropped. The allow-listed
// setting survives; the Wi-Fi PSK, VPN shared secret, certificate private key,
// SCEP challenge, API token, and raw payload-content blob do NOT.
func TestNormalize_DropsSecretBearingSettings(t *testing.T) {
	t.Parallel()
	raw := []RawDeviceProfiles{{
		DeviceID: "d",
		Profiles: []RawProfile{{
			Name: "WiFi-Corp",
			Settings: []RawSetting{
				{Key: "disk_encryption_enforced", Value: "true"}, // allow-listed: survives
				{Key: "wifi_password", Value: "hunter2"},         // not allow-listed + banned
				{Key: "vpn_shared_secret", Value: "topsecret"},   // not allow-listed + banned
				{Key: "certificate_private_key", Value: "-----BEGIN"},
				{Key: "scep_challenge", Value: "abc"},
				{Key: "api_token", Value: "xoxb-1"},
				{Key: "PayloadContent", Value: "<data>...</data>"},
				{Key: "unknown_metadata", Value: "z"}, // not allow-listed (off-list, not banned)
			},
		}},
	}}
	devs := Normalize(devposture.MDMJamf, raw, fixedNow)
	got := devs[0].Profiles[0].Settings
	if len(got) != 1 {
		t.Fatalf("settings = %+v; want only the allow-listed disk_encryption_enforced", got)
	}
	if got[0].Key != "disk_encryption_enforced" || got[0].Value != "true" {
		t.Errorf("surviving setting = %+v; want disk_encryption_enforced=true", got[0])
	}
}

func TestNormalize_SortsAndTrimsSettingsAndScope(t *testing.T) {
	t.Parallel()
	raw := []RawDeviceProfiles{{
		DeviceID: "d",
		Profiles: []RawProfile{{
			Name:  "Security",
			Scope: []string{" Eng ", "", "Sales"},
			Settings: []RawSetting{
				{Key: "firewall_enabled", Value: " on "},
				{Key: "disk_encryption_enforced", Value: "true"},
			},
		}},
	}}
	p := Normalize(devposture.MDMJamf, raw, fixedNow)[0].Profiles[0]
	if len(p.Scope) != 2 || p.Scope[0] != "Eng" || p.Scope[1] != "Sales" {
		t.Errorf("scope = %+v; want [Eng Sales]", p.Scope)
	}
	if p.Settings[0].Key != "disk_encryption_enforced" || p.Settings[1].Key != "firewall_enabled" {
		t.Errorf("settings not sorted by key: %+v", p.Settings)
	}
	if p.Settings[1].Value != "on" {
		t.Errorf("setting value not trimmed: %q", p.Settings[1].Value)
	}
}

func TestNormalize_BoundsProfilesPerDevice(t *testing.T) {
	t.Parallel()
	raw := make([]RawProfile, MaxProfilesPerDevice+10)
	for i := range raw {
		// zero-padded so the stable sort is deterministic for the bound.
		raw[i] = RawProfile{Name: padName(i)}
	}
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{{DeviceID: "d", Profiles: raw}}, fixedNow)
	if len(devs[0].Profiles) != MaxProfilesPerDevice {
		t.Errorf("profiles = %d; want %d", len(devs[0].Profiles), MaxProfilesPerDevice)
	}
}

func TestNormalize_BoundsSettingsPerProfile(t *testing.T) {
	t.Parallel()
	// All allow-listed keys repeated is impossible (allow-list is finite), so this
	// exercises the bound via the allow-list size — the allow-list itself is the
	// effective ceiling. Assert the cap constant is at least the allow-list size so
	// no allow-listed key is ever dropped by the defensive bound.
	if MaxSettingsPerProfile < len(AllowedSettingKeys) {
		t.Errorf("MaxSettingsPerProfile (%d) < allow-list size (%d): an allow-listed key could be dropped",
			MaxSettingsPerProfile, len(AllowedSettingKeys))
	}
}

func TestIsBannedSettingKey(t *testing.T) {
	t.Parallel()
	banned := []string{"wifi_password", "VPN_Shared_Secret", "certificate", "scep_challenge", "api_token", "PayloadContent", "private_key", "psk"}
	for _, k := range banned {
		if !IsBannedSettingKey(k) {
			t.Errorf("IsBannedSettingKey(%q) = false; want true", k)
		}
	}
	for _, k := range []string{"disk_encryption_enforced", "firewall_enabled", "passcode_required"} {
		if IsBannedSettingKey(k) {
			t.Errorf("IsBannedSettingKey(%q) = true; want false (allow-listed)", k)
		}
	}
}

// TestAllowListHasNoBannedKey is a self-consistency guard: no key on the
// compliance-relevant allow-list may itself be flagged by the credential
// deny-list (otherwise it would be silently dropped at normalization).
func TestAllowListHasNoBannedKey(t *testing.T) {
	t.Parallel()
	for k := range AllowedSettingKeys {
		if IsBannedSettingKey(k) {
			t.Errorf("allow-listed key %q is flagged by the deny-list — it would never survive", k)
		}
	}
}

// TestNormalize_AllowsSlice595EnrichmentKeys asserts every per-setting key the
// slice-595 enrichment read surfaces is on the allow-list and survives
// normalization with its non-secret summary value intact.
func TestNormalize_AllowsSlice595EnrichmentKeys(t *testing.T) {
	t.Parallel()
	keys := []RawSetting{
		{Key: "device_compliant", Value: "true"},
		{Key: "device_supervised", Value: "false"},
		{Key: "device_managed", Value: "true"},
		{Key: "profile_assignment_state", Value: "compliant"},
		// also the pre-existing hardening keys the read populates
		{Key: "disk_encryption_enforced", Value: "true"},
		{Key: "gatekeeper_enabled", Value: "true"},
		{Key: "screen_lock_enforced", Value: "true"},
	}
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{
		{DeviceID: "d", Profiles: []RawProfile{{Name: "Enforced Summary", Settings: keys}}},
	}, fixedNow)
	got := devs[0].Profiles[0].Settings
	if len(got) != len(keys) {
		t.Fatalf("settings = %d; want %d (every enrichment key survives): %+v", len(got), len(keys), got)
	}
	for _, want := range keys {
		found := false
		for _, g := range got {
			if g.Key == want.Key && g.Value == want.Value {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("enrichment key %q=%q did not survive normalization", want.Key, want.Value)
		}
	}
}

// TestNormalize_ValueSanitization_DropsSecretSiblingNextToAllowedKey is the
// load-bearing secret-drop guard for the slice-595 enrichment: a profile payload
// carries BOTH an allowed hardening key AND a secret-bearing sibling key (a fake
// Wi-Fi password right next to an allowed disk-encryption flag). Only the allowed
// key reaches settings[]; the secret key and its value are provably dropped.
func TestNormalize_ValueSanitization_DropsSecretSiblingNextToAllowedKey(t *testing.T) {
	t.Parallel()
	raw := []RawDeviceProfiles{{
		DeviceID: "d",
		Profiles: []RawProfile{{
			Name: "WiFi-Corp",
			Settings: []RawSetting{
				{Key: "disk_encryption_enforced", Value: "true"},  // allowed hardening key — survives
				{Key: "wifi_password", Value: "FAKE-PSK-FIXTURE"}, // secret sibling — dropped
				{Key: "wifi_ssid", Value: "CorpNet"},              // off-list (not banned) — dropped
			},
		}},
	}}
	devs := Normalize(devposture.MDMJamf, raw, fixedNow)
	got := devs[0].Profiles[0].Settings
	if len(got) != 1 || got[0].Key != "disk_encryption_enforced" || got[0].Value != "true" {
		t.Fatalf("settings = %+v; want only disk_encryption_enforced=true", got)
	}
	// Belt-and-braces: the secret VALUE must appear nowhere in the surviving set.
	for _, s := range got {
		if s.Value == "FAKE-PSK-FIXTURE" {
			t.Fatalf("secret value leaked into settings: %+v", s)
		}
	}
}

func TestNormalize_NilNowUsesWallClock(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC().Truncate(time.Hour)
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{{DeviceID: "d", Profiles: []RawProfile{{Name: "P"}}}}, nil)
	if len(devs) != 1 {
		t.Fatalf("devices = %d; want 1", len(devs))
	}
	// observed_at is truncated to the hour; it should be at or after the hour we
	// captured before the call (allowing the rare hour-boundary cross).
	if devs[0].ObservedAt.Before(before) {
		t.Errorf("observed_at %v predates wall-clock hour %v", devs[0].ObservedAt, before)
	}
}

func TestNormalize_BoundsSettingsPerProfileViaDuplicateAllowedKeys(t *testing.T) {
	t.Parallel()
	// Feed MaxSettingsPerProfile+5 ALLOW-LISTED settings (duplicates of the same
	// allowed key are permitted by the filter — only the bound drops them) so the
	// MaxSettingsPerProfile ceiling branch is exercised deterministically.
	raw := make([]RawSetting, 0, MaxSettingsPerProfile+5)
	for i := 0; i < MaxSettingsPerProfile+5; i++ {
		raw = append(raw, RawSetting{Key: "passcode_required", Value: "true"})
	}
	devs := Normalize(devposture.MDMJamf, []RawDeviceProfiles{
		{DeviceID: "d", Profiles: []RawProfile{{Name: "P", Settings: raw}}},
	}, fixedNow)
	got := devs[0].Profiles[0].Settings
	if len(got) != MaxSettingsPerProfile {
		t.Errorf("settings = %d; want capped at %d", len(got), MaxSettingsPerProfile)
	}
}

func padName(i int) string {
	const digits = "0123456789"
	b := []byte{digits[i/100%10], digits[i/10%10], digits[i%10]}
	return "p" + string(b)
}
