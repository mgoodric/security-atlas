// Package swinventory is the shared normalization layer for the MDM connector
// family's installed-software inventory evidence (Jamf + Intune, slice 555).
// Both MDMs' installed-application inventory reduces to the same shape — a
// managed device plus a bounded list of installed software, each item carrying
// only an app name, an optional version, an optional bundle/package identifier,
// and an optional install date — so they share one evidence kind
// (endpoint.software_inventory.v1) and one normalizer.
//
// This is the deliberate slice-490 follow-on: slice 490 EXCLUDED installed-app
// inventory from endpoint.device_posture.v1 as an over-collection guard
// (P0-490-3). Software inventory answers a different control question
// (patch-/vulnerability-management + asset inventory: "are managed endpoints
// running patched, authorized software?"), so it warrants its own scoped kind
// rather than widening the posture summary.
//
// The load-bearing guard (P0-555 / threat-model I): a SoftwareItem carries the
// app NAME + optional VERSION + optional bundle/package IDENTIFIER + optional
// INSTALL DATE only. It deliberately has NO field for file paths, per-user
// app-usage telemetry, license keys, device contents, browsing data, or owner
// personal contact detail. The type system itself is the first line of the
// over-collection defence: there is nowhere to put a file path, a usage count,
// or a license key. The vendor clients request and decode only the listed
// fields at the API boundary, so the unwanted fields never enter memory as
// connector data. The cmd-layer test asserts no banned key/substring reaches an
// emitted record.
package swinventory

import (
	"sort"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
)

// MaxSoftwarePerDevice bounds the per-device software list. A managed endpoint
// rarely carries more than a few hundred installed apps; the cap keeps a single
// evidence record bounded (threat-model D, parallel to the device-posture page
// bound) and is a defensive over-collection ceiling. Items beyond the cap are
// dropped after a stable sort, so the bound is deterministic.
const MaxSoftwarePerDevice = 500

// RawSoftwareItem is the narrow, PII-bounded view a vendor client returns for
// one installed-software item. The vendor clients map their API response into
// this shape, discarding file paths, usage telemetry, and license keys at the
// decode boundary. Tests construct it directly.
//
// There is intentionally no Path / UsageCount / LastUsed / LicenseKey field on
// this struct (P0-555).
type RawSoftwareItem struct {
	// Name is the application / package display name (required; empty items are
	// dropped). NOT a file path.
	Name string
	// Version is the optional installed version string. The load-bearing field
	// for known-vulnerable-version detection.
	Version string
	// Identifier is the optional bundle id (macOS) / package id (Windows). A
	// stable cross-device identifier, NEVER a file path.
	Identifier string
	// InstallDate is the optional install/first-seen date the source reports.
	// Descriptive.
	InstallDate string
}

// RawDeviceSoftware is the narrow per-device view a vendor client returns: the
// managed device's stable id plus its installed-software list. There is no
// field for geolocation, device contents, or owner contact detail (P0-555).
type RawDeviceSoftware struct {
	// DeviceID is the MDM-native stable identifier (opaque, non-secret). Joins
	// to the device's endpoint.device_posture.v1 record.
	DeviceID string
	// Software is the device's installed-software list (pre-bound, pre-sort).
	Software []RawSoftwareItem
}

// SoftwareItem is the normalized per-item shape the record builder emits. Field
// names map 1:1 to the schema's software[] item.
type SoftwareItem struct {
	Name        string
	Version     string
	Identifier  string
	InstallDate string
}

// Device is the normalized per-device software-inventory record the cmd layer
// turns into an evidence record. Field names map 1:1 to the
// endpoint.software_inventory.v1 schema.
type Device struct {
	SourceMDM  devposture.MDM
	DeviceID   string
	Software   []SoftwareItem
	ObservedAt time.Time
}

// Normalize converts a vendor's raw per-device software lists into normalized
// Devices, stamping the source MDM + a single observed-at. now is injectable
// for deterministic tests (nil -> time.Now UTC). Devices missing an id are
// dropped (the schema requires it). Software items missing a name are dropped
// (the schema requires it). Each device's software list is stable-sorted (by
// name, then version, then identifier) and bounded to MaxSoftwarePerDevice.
func Normalize(mdm devposture.MDM, raw []RawDeviceSoftware, now func() time.Time) []Device {
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
		items := normalizeSoftware(d.Software)
		out = append(out, Device{
			SourceMDM:  mdm,
			DeviceID:   id,
			Software:   items,
			ObservedAt: observedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeviceID < out[j].DeviceID })
	return out
}

func normalizeSoftware(raw []RawSoftwareItem) []SoftwareItem {
	items := make([]SoftwareItem, 0, len(raw))
	for _, s := range raw {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		items = append(items, SoftwareItem{
			Name:        name,
			Version:     strings.TrimSpace(s.Version),
			Identifier:  strings.TrimSpace(s.Identifier),
			InstallDate: strings.TrimSpace(s.InstallDate),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name != items[j].Name {
			return items[i].Name < items[j].Name
		}
		if items[i].Version != items[j].Version {
			return items[i].Version < items[j].Version
		}
		return items[i].Identifier < items[j].Identifier
	})
	if len(items) > MaxSoftwarePerDevice {
		items = items[:MaxSoftwarePerDevice]
	}
	return items
}
