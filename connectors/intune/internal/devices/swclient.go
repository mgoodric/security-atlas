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

// apiDetectedAppsPage is the minimal Graph detectedApps JSON shape — each app's
// id, name, version, and its associated managed-device ids ONLY. The
// managedDevices expansion is $select'd down to the device id (never deviceName,
// userPrincipalName, or owner contact detail). Properties not requested are
// absent: json.Decode discards JSON keys with no matching struct field, so a
// file path / size / owner detail never enters memory as connector data
// (P0-555).
type apiDetectedAppsPage struct {
	Value []struct {
		ID             string `json:"id"`
		DisplayName    string `json:"displayName"`
		Version        string `json:"version"`
		ManagedDevices []struct {
			ID string `json:"id"`
		} `json:"managedDevices"`
	} `json:"value"`
}

// ListDetectedApps reads the first bounded page of detected apps (with their
// managed-device associations) and inverts the app-centric graph into the
// device-centric shape swinventory expects. Read-only: a single GET against
// /deviceManagement/detectedApps with an expanded, $select'd managedDevices.
func (c *Client) ListDetectedApps(ctx context.Context) ([]RawDeviceSoftware, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("$select", strings.Join(softwareSelect, ","))
	// Expand the device association, $select'd to the device id only — never the
	// device name or owner contact detail.
	q.Set("$expand", "managedDevices($select=id)")
	q.Set("$top", strconv.Itoa(pageLimit))
	var page apiDetectedAppsPage
	if err := c.getJSON(ctx, "/deviceManagement/detectedApps?"+q.Encode(), &page); err != nil {
		return nil, err
	}

	// Invert app -> [device] into device -> [app], preserving a stable device
	// order via firstSeen.
	byDevice := map[string][]RawSoftwareItem{}
	order := make([]string, 0)
	for _, app := range page.Value {
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

	out := make([]RawDeviceSoftware, 0, len(order))
	for _, id := range order {
		out = append(out, RawDeviceSoftware{DeviceID: id, Apps: byDevice[id]})
	}
	return out, nil
}
