package devices

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// softwareSelect is the EXACT set of detectedApp properties the connector
// requests: displayName + version + id ONLY. sizeInByte, platform-extra, and
// every other detectedApp property are deliberately NOT requested (P0-555).
var softwareSelect = []string{"id", "displayName", "version"}

// maxSoftwarePages caps the Graph detectedApps @odata.nextLink walk so a hostile
// or non-terminating nextLink chain cannot drive an unbounded read loop (P0-590
// / threat-model D). At $top=pageLimit (200) apps/page this bounds a single
// ListDetectedApps run to maxSoftwarePages*pageLimit detectedApp records.
// Hitting the cap is not an error: the connector inverts what it gathered.
const maxSoftwarePages = 50

// apiDetectedAppsPage is the minimal Graph detectedApps JSON shape — each app's
// id, name, version, and its associated managed-device ids ONLY, plus the
// @odata.nextLink page cursor. The managedDevices expansion is $select'd down to
// the device id (never deviceName, userPrincipalName, or owner contact detail).
// Properties not requested are absent: json.Decode discards JSON keys with no
// matching struct field, so a file path / size / owner detail never enters
// memory as connector data (P0-555).
type apiDetectedAppsPage struct {
	NextLink string `json:"@odata.nextLink"`
	Value    []struct {
		ID             string `json:"id"`
		DisplayName    string `json:"displayName"`
		Version        string `json:"version"`
		ManagedDevices []struct {
			ID string `json:"id"`
		} `json:"managedDevices"`
	} `json:"value"`
}

// ListDetectedApps reads the FULL set of detected apps (with their managed-device
// associations) by following the @odata.nextLink page cursor until it is absent
// or the maxSoftwarePages cap is hit, then inverts the app-centric graph into the
// device-centric shape swinventory expects. Read-only: one GET per page against
// /deviceManagement/detectedApps with an expanded, $select'd managedDevices. The
// field allow-list ($select + $expand + struct shape) is reused UNCHANGED from
// slice 555; this slice only adds the nextLink loop (P0-590). Because the app
// graph is inverted to device-centric, walking every page is required so a
// device's apps that fall on later pages are not silently dropped.
func (c *Client) ListDetectedApps(ctx context.Context) ([]RawDeviceSoftware, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	// Invert app -> [device] into device -> [app] across all pages, preserving a
	// stable device order via first-seen.
	byDevice := map[string][]RawSoftwareItem{}
	order := make([]string, 0)

	q := url.Values{}
	q.Set("$select", strings.Join(softwareSelect, ","))
	// Expand the device association, $select'd to the device id only — never the
	// device name or owner contact detail.
	q.Set("$expand", "managedDevices($select=id)")
	q.Set("$top", strconv.Itoa(pageLimit))
	// next is the request target for the current page. The first page is built
	// from the $select/$expand/$top query; subsequent pages use the server-issued
	// @odata.nextLink absolute URL verbatim (it carries an opaque skiptoken).
	next := "/deviceManagement/detectedApps?" + q.Encode()
	absolute := false

	for page := 0; page < maxSoftwarePages; page++ {
		var resp apiDetectedAppsPage
		var err error
		if absolute {
			err = c.getJSONAbsolute(ctx, next, &resp)
		} else {
			err = c.getJSON(ctx, next, &resp)
		}
		if err != nil {
			return nil, err
		}

		for _, app := range resp.Value {
			name := strings.TrimSpace(app.DisplayName)
			if name == "" {
				continue
			}
			item := RawSoftwareItem{
				Name:    name,
				Version: strings.TrimSpace(app.Version),
				AppID:   strings.TrimSpace(app.ID),
			}
			for _, md := range app.ManagedDevices {
				id := strings.TrimSpace(md.ID)
				if id == "" {
					continue
				}
				if _, seen := byDevice[id]; !seen {
					order = append(order, id)
				}
				byDevice[id] = append(byDevice[id], item)
			}
		}

		if strings.TrimSpace(resp.NextLink) == "" {
			break
		}
		next = resp.NextLink
		absolute = true
	}

	out := make([]RawDeviceSoftware, 0, len(order))
	for _, id := range order {
		out = append(out, RawDeviceSoftware{DeviceID: id, Apps: byDevice[id]})
	}
	return out, nil
}
