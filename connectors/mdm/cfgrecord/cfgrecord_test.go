package cfgrecord

import (
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/cfgprofile"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/idem"
)

func sampleDevice() cfgprofile.Device {
	return cfgprofile.Device{
		SourceMDM: devposture.MDMJamf,
		DeviceID:  "d-1",
		Profiles: []cfgprofile.Profile{
			{
				Name:         "Passcode Policy",
				Identifier:   "com.acme.passcode",
				ProfileType:  "configuration",
				Scope:        []string{"All Macs"},
				UUID:         "11111111-2222-3333-4444-555555555555",
				LastModified: "2026-01-02T03:04:05Z",
				Settings: []cfgprofile.Setting{
					{Key: "passcode_required", Value: "true"},
					{Key: "disk_encryption_enforced", Value: "true"},
				},
			},
			{Name: "Restrictions"}, // minimal: only required name
		},
		ObservedAt: time.Date(2026, 6, 7, 12, 30, 0, 0, time.UTC),
	}
}

func TestBuild_SetsKindAndIdempotencyAndScope(t *testing.T) {
	t.Parallel()
	rec, err := Build(sampleDevice(), "scf:CFG-02", "connector:jamf:config-profile@dev", "jamf", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rec.GetEvidenceKind() != EvidenceKind || EvidenceKind != "endpoint.config_profile.v1" {
		t.Errorf("kind = %q; want endpoint.config_profile.v1", rec.GetEvidenceKind())
	}
	if rec.GetSchemaVersion() != SchemaVersion {
		t.Errorf("schema version = %q", rec.GetSchemaVersion())
	}
	want := idem.ConfigProfileKey("jamf", "d-1", time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC))
	if rec.GetIdempotencyKey() != want {
		t.Errorf("idempotency key = %q; want %q", rec.GetIdempotencyKey(), want)
	}
	if rec.GetControlId() != "scf:CFG-02" {
		t.Errorf("control = %q", rec.GetControlId())
	}
	scope := map[string]string{}
	for _, d := range rec.GetScope() {
		scope[d.GetKey()] = d.GetValues()[0]
	}
	if scope["service"] != "jamf" || scope["environment"] != "prod" {
		t.Errorf("scope = %v", scope)
	}
	if !rec.GetObservedAt().AsTime().Equal(time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("observed_at = %v", rec.GetObservedAt().AsTime())
	}
}

// TestBuild_PayloadCarriesProfileFieldsOnly_NoSecrets is THE load-bearing
// secret-redaction guard: the top-level payload, every profile, and every
// setting are limited to allow-listed keys — and no banned credential substring
// (password / secret / privatekey / psk / token / PayloadContent / etc.) reaches
// a record anywhere (key OR value).
func TestBuild_PayloadCarriesProfileFieldsOnly_NoSecrets(t *testing.T) {
	t.Parallel()
	// Hand-build a Device carrying credential-bearing settings directly, bypassing
	// the normalizer, to prove the BUILDER itself re-applies the redaction guard.
	dev := sampleDevice()
	dev.Profiles[0].Settings = append(dev.Profiles[0].Settings,
		cfgprofile.Setting{Key: "wifi_password", Value: "hunter2"},
		cfgprofile.Setting{Key: "vpn_shared_secret", Value: "topsecret"},
		cfgprofile.Setting{Key: "certificate_private_key", Value: "FAKE-PRIVKEY-MATERIAL-FIXTURE"},
		cfgprofile.Setting{Key: "PayloadContent", Value: "<data>c2VjcmV0</data>"},
	)
	rec, err := Build(dev, "scf:CFG-02", "a", "jamf", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pm := rec.GetPayload().AsMap()

	allowedTop := map[string]bool{"source_mdm": true, "device_id": true, "profile_count": true, "profiles": true}
	for k := range pm {
		if !allowedTop[k] {
			t.Errorf("non-allow-listed top-level payload key %q (P0-556)", k)
		}
	}
	for _, k := range []string{"source_mdm", "device_id", "profile_count", "profiles"} {
		if _, ok := pm[k]; !ok {
			t.Errorf("missing required payload key %q", k)
		}
	}

	allowedProfile := map[string]bool{
		"name": true, "identifier": true, "profile_type": true,
		"scope": true, "uuid": true, "last_modified": true, "settings": true,
	}
	allowedSetting := map[string]bool{"key": true, "value": true}
	bannedSubstrings := []string{"password", "passwd", "secret", "psk", "privatekey", "private_key", "token", "credential", "challenge", "payloadcontent", "certificate"}

	profiles := pm["profiles"].([]any)
	for _, pr := range profiles {
		m := pr.(map[string]any)
		for k := range m {
			if !allowedProfile[k] {
				t.Errorf("non-allow-listed profile key %q (P0-556)", k)
			}
		}
		settings := m["settings"].([]any)
		for _, st := range settings {
			sm := st.(map[string]any)
			for k := range sm {
				if !allowedSetting[k] {
					t.Errorf("non-allow-listed setting key %q (P0-556)", k)
				}
			}
			settingKey := sm["key"].(string)
			// The credential-bearing setting keys must NOT have survived into the
			// payload at all.
			lk := strings.ToLower(settingKey)
			for _, b := range bannedSubstrings {
				if strings.Contains(lk, b) {
					t.Errorf("banned setting key %q reached payload (P0-556 secret-redaction breach)", settingKey)
				}
			}
		}
	}

	// Walk the entire payload (keys AND string values) and assert no leaked secret
	// VALUE either — a defence-in-depth scan over the whole record.
	for _, leaked := range []string{"hunter2", "topsecret", "FAKE-PRIVKEY-MATERIAL-FIXTURE", "c2VjcmV0"} {
		if walkContains(pm, leaked) {
			t.Errorf("leaked secret value %q reached the evidence payload (P0-556)", leaked)
		}
	}
}

func TestBuild_ProfileCountAndOptionalFields(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleDevice(), "scf:CFG-02", "a", "jamf", "prod")
	pm := rec.GetPayload().AsMap()
	if pm["profile_count"].(float64) != 2 {
		t.Errorf("profile_count = %v; want 2", pm["profile_count"])
	}
	profiles := pm["profiles"].([]any)
	// First profile carries all optional fields.
	first := profiles[0].(map[string]any)
	for _, k := range []string{"identifier", "profile_type", "scope", "uuid", "last_modified"} {
		if _, ok := first[k]; !ok {
			t.Errorf("first profile missing populated optional %q", k)
		}
	}
	// Second profile (name-only) omits the empty optionals.
	second := profiles[1].(map[string]any)
	for _, k := range []string{"identifier", "profile_type", "scope", "uuid", "last_modified"} {
		if _, ok := second[k]; ok {
			t.Errorf("empty optional %q should be omitted on the name-only profile", k)
		}
	}
	// settings is always present (possibly empty) so the evaluator sees the shape.
	if _, ok := second["settings"]; !ok {
		t.Error("settings key should always be present")
	}
}

func TestBuild_EmptyProfileListEmitsZeroCount(t *testing.T) {
	t.Parallel()
	d := sampleDevice()
	d.Profiles = nil
	rec, err := Build(d, "scf:CFG-02", "a", "jamf", "prod")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pm := rec.GetPayload().AsMap()
	if pm["profile_count"].(float64) != 0 {
		t.Errorf("profile_count = %v; want 0", pm["profile_count"])
	}
	if len(pm["profiles"].([]any)) != 0 {
		t.Error("profiles should be empty")
	}
}

func TestBuild_SameDeviceSameHourSameKey(t *testing.T) {
	t.Parallel()
	r1, _ := Build(sampleDevice(), "scf:CFG-02", "a", "jamf", "prod")
	r2, _ := Build(sampleDevice(), "scf:CFG-02", "a", "jamf", "prod")
	if r1.GetIdempotencyKey() != r2.GetIdempotencyKey() {
		t.Error("same device + hour should yield same idempotency key")
	}
}

func TestBuild_ResultIsInconclusive(t *testing.T) {
	t.Parallel()
	rec, _ := Build(sampleDevice(), "scf:CFG-02", "a", "jamf", "prod")
	if rec.GetResult().String() != "RESULT_INCONCLUSIVE" {
		t.Errorf("result = %s; want RESULT_INCONCLUSIVE", rec.GetResult())
	}
}

// walkContains recursively scans any structpb-decoded value for a substring in
// any string leaf (key names are also checked at the map level).
func walkContains(v any, needle string) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, needle)
	case map[string]any:
		for k, val := range t {
			if strings.Contains(k, needle) || walkContains(val, needle) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if walkContains(val, needle) {
				return true
			}
		}
	}
	return false
}
