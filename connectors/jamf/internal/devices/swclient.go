package devices

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// softwareSections is the EXACT set of inventory sections the software-inventory
// read requests: GENERAL (for the stable computer id) + APPLICATIONS (the
// installed-software list). The GPS location, USER_AND_LOCATION (owner contact),
// disk-encryption, and every other section are deliberately NOT requested
// (P0-555). Note APPLICATIONS — deliberately excluded from the posture read
// (postureSections) — IS requested here because installed-software inventory is
// exactly this kind's control question.
var softwareSections = []string{"GENERAL", "APPLICATIONS"}

// apiSoftwarePage is the minimal Jamf computers-inventory JSON shape for the
// software read — the computer id plus each application's name, version, bundle
// id, and install date ONLY. Every field the APPLICATIONS section also carries
// that we do NOT want (the executable PATH, "sizeMegabytes", per-app usage) is
// absent: json.Decode discards JSON keys with no matching struct field, so a
// file path or usage stat never enters memory as connector data (P0-555).
type apiSoftwarePage struct {
	Results []struct {
		ID           string `json:"id"`
		Applications []struct {
			Name      string `json:"name"`
			Version   string `json:"version"`
			BundleID  string `json:"bundleId"`
			Installed string `json:"installDate"`
		} `json:"applications"`
	} `json:"results"`
}

// ListSoftware reads the first bounded page of computer inventory limited to the
// GENERAL + APPLICATIONS sections. Read-only: a single GET against
// /api/v1/computers-inventory.
func (c *Client) ListSoftware(ctx context.Context) ([]RawDeviceSoftware, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, s := range softwareSections {
		q.Add("section", s)
	}
	q.Set("page", "0")
	q.Set("page-size", strconv.Itoa(pageLimit))
	var page apiSoftwarePage
	if err := c.getJSON(ctx, "/api/v1/computers-inventory?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	out := make([]RawDeviceSoftware, 0, len(page.Results))
	for _, r := range page.Results {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			continue
		}
		apps := make([]RawSoftwareItem, 0, len(r.Applications))
		for _, a := range r.Applications {
			apps = append(apps, RawSoftwareItem{
				Name:        strings.TrimSpace(a.Name),
				Version:     strings.TrimSpace(a.Version),
				BundleID:    strings.TrimSpace(a.BundleID),
				InstallDate: strings.TrimSpace(a.Installed),
			})
		}
		out = append(out, RawDeviceSoftware{ComputerID: id, Apps: apps})
	}
	return out, nil
}
