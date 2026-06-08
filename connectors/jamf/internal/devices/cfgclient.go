package devices

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// configProfileSections is the EXACT set of inventory sections the config-profile
// read requests: GENERAL (for the stable computer id) + CONFIGURATION_PROFILES
// (the assigned-profile metadata: display name, identifier, uuid, last-installed).
// The GPS location, USER_AND_LOCATION (owner contact), APPLICATIONS, and every
// other section are deliberately NOT requested (P0-556).
//
// THE LOAD-BEARING SECRET-REDACTION BOUNDARY (P0-556): the CONFIGURATION_PROFILES
// inventory section returns profile METADATA only — it does NOT carry the raw
// configuration-profile <data>/PayloadContent blob, so the connector never even
// receives a Wi-Fi PSK, VPN shared secret, certificate private key, or SCEP
// challenge from this read. The struct below has no field for any such payload;
// json.Decode discards JSON keys with no matching struct field, so a secret
// cannot enter memory as connector data.
var configProfileSections = []string{"GENERAL", "CONFIGURATION_PROFILES"}

// apiConfigProfilePage is the minimal Jamf computers-inventory JSON shape for the
// config-profile read — the computer id plus each assigned profile's display
// name, identifier, uuid, and last-installed date ONLY. The Jamf
// CONFIGURATION_PROFILES section does not expose the raw payload blob, and this
// struct has no field for it regardless (P0-556).
type apiConfigProfilePage struct {
	Results []struct {
		ID                    string `json:"id"`
		ConfigurationProfiles []struct {
			DisplayName       string `json:"displayName"`
			ProfileIdentifier string `json:"profileIdentifier"`
			UUID              string `json:"uuid"`
			LastInstalled     string `json:"lastInstalled"`
		} `json:"configurationProfiles"`
	} `json:"results"`
}

// ListConfigProfiles reads the first bounded page of computer inventory limited
// to the GENERAL + CONFIGURATION_PROFILES sections. Read-only: a single GET
// against /api/v1/computers-inventory.
//
// Jamf's CONFIGURATION_PROFILES inventory section is metadata-only — it reports
// WHICH profiles are deployed (name, identifier, uuid, install date) but not the
// per-setting key/values. The per-profile Settings list is therefore empty at
// v0 for Jamf (the schema allows an empty settings array); the device's posture
// record (endpoint.device_posture.v1) already carries the enforced posture
// summary, and a richer per-setting read is a documented follow-on. The Settings
// field exists on the type so a future read populates it through the SAME
// allow-list guard.
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
		profiles := make([]RawConfigProfile, 0, len(r.ConfigurationProfiles))
		for _, p := range r.ConfigurationProfiles {
			profiles = append(profiles, RawConfigProfile{
				Name:         strings.TrimSpace(p.DisplayName),
				Identifier:   strings.TrimSpace(p.ProfileIdentifier),
				ProfileType:  "configuration",
				UUID:         strings.TrimSpace(p.UUID),
				LastModified: strings.TrimSpace(p.LastInstalled),
			})
		}
		out = append(out, RawDeviceConfigProfiles{ComputerID: id, Profiles: profiles})
	}
	return out, nil
}
