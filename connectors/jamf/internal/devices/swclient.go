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

// maxSoftwarePages caps the Jamf software-inventory page walk so a hostile or
// runaway totalCount cannot drive an unbounded read loop (P0-590 / threat-model
// D). At pageLimit=200 results/page this bounds a single ListSoftware run to
// maxSoftwarePages*pageLimit computer records. Hitting the cap is not an error:
// the connector returns what it gathered (a complete-as-possible inventory),
// matching the connector "register-per-run, best-effort within bounds" posture.
const maxSoftwarePages = 50

// apiSoftwarePage is the minimal Jamf computers-inventory JSON shape for the
// software read — the computer id plus each application's name, version, bundle
// id, and install date ONLY, plus the page-walk control field totalCount. Every
// field the APPLICATIONS section also carries that we do NOT want (the executable
// PATH, "sizeMegabytes", per-app usage) is absent: json.Decode discards JSON keys
// with no matching struct field, so a file path or usage stat never enters memory
// as connector data (P0-555).
type apiSoftwarePage struct {
	TotalCount int `json:"totalCount"`
	Results    []struct {
		ID           string `json:"id"`
		Applications []struct {
			Name      string `json:"name"`
			Version   string `json:"version"`
			BundleID  string `json:"bundleId"`
			Installed string `json:"installDate"`
		} `json:"applications"`
	} `json:"results"`
}

// ListSoftware reads the full computer inventory limited to the GENERAL +
// APPLICATIONS sections, walking the Jamf page cursor (page / page-size /
// totalCount) until every computer is gathered or the maxSoftwarePages cap is
// hit. Read-only: one GET per page against /api/v1/computers-inventory. The
// field allow-list (apiSoftwarePage struct shape) is reused UNCHANGED from
// slice 555; this slice only adds the page loop (P0-590).
func (c *Client) ListSoftware(ctx context.Context) ([]RawDeviceSoftware, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	var out []RawDeviceSoftware
	gathered := 0
	for page := 0; page < maxSoftwarePages; page++ {
		q := url.Values{}
		for _, s := range softwareSections {
			q.Add("section", s)
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("page-size", strconv.Itoa(pageLimit))
		var resp apiSoftwarePage
		if err := c.getJSON(ctx, "/api/v1/computers-inventory?"+q.Encode(), &resp); err != nil {
			return nil, err
		}
		// An empty page terminates the walk regardless of totalCount — a defensive
		// guard against a totalCount that never converges.
		if len(resp.Results) == 0 {
			break
		}
		for _, r := range resp.Results {
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
		gathered += len(resp.Results)
		// Stop once the reported population is fully covered. totalCount<=0 (older
		// Jamf or omitted) falls back to the empty-page terminator above.
		if resp.TotalCount > 0 && gathered >= resp.TotalCount {
			break
		}
	}
	return out, nil
}
