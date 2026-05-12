// Package osqueryposture pulls one HostPosture record per endpoint.
//
// Two upstream modes share the same canonical HostPosture model:
//
//   - Fleet API mode (developer docs at fleetdm.com/docs/rest-api/rest-api).
//     Two-call pull: GET /api/v1/fleet/hosts (paginated list) +
//     GET /api/v1/fleet/hosts/{id} (per-host detail, including the disk
//     encryption + MDM enrolment fields not returned in the list). The
//     per-host call is what surfaces the boolean policy fields the
//     evidence schema declares — the list endpoint alone is not enough.
//
//   - Local osqueryd extension socket mode. The connector dials a
//     Unix-domain socket and submits a Thrift-shaped osquery SQL query.
//     For slice 047 the local-mode wire is intentionally minimal — the
//     production hook is the Fleet path, which is what mid-market
//     customers actually run. Local-mode tests inject a fake LocalQueryer
//     so the package contract is exercised without a real osqueryd.
//
// The package emits canonical HostPosture values; the cmd layer translates
// each to an evidencev1.EvidenceRecord with the existing slice-014
// osquery.host_posture.v1 schema (host_uuid, hostname, platform,
// os_version, disk_encryption_enabled, screen_lock_enabled,
// firewall_enabled, mdm_enrolled). The 1.0.0 schema declares
// additionalProperties:false so this package emits only those fields.
package osqueryposture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mgoodric/security-atlas/connectors/osquery/internal/osqueryauth"
)

// Result mirrors the connector's pass/fail intent.
type Result string

const (
	ResultPass         Result = "pass"
	ResultFail         Result = "fail"
	ResultInconclusive Result = "inconclusive"
)

// HostPosture is one record the cmd layer turns into an evidence record.
// Field names map 1:1 to the osquery.host_posture.v1 schema declared by
// slice 014. Nothing outside the schema's declared properties is emitted.
type HostPosture struct {
	HostUUID              string
	Hostname              string
	Platform              string // "darwin", "linux", "windows"
	OSVersion             string
	DiskEncryptionEnabled bool
	ScreenLockEnabled     bool
	FirewallEnabled       bool
	MDMEnrolled           bool

	// Result + Reason are computed by evaluate(). Result derives from the
	// minimum-baseline rule defined below; the evaluator (slice 015) owns
	// the policy ladder.
	Result Result
	Reason string

	// ObservedAt is filled by Pull. The cmd layer truncates this to the
	// hour for the idempotency key.
	ObservedAt time.Time
}

// HostListEntry is one row of the Fleet host-list response. Exported so
// tests outside this package can implement FleetAPI without depending on
// the package's private JSON shape. JSON tags allow direct decode from
// the Fleet REST response without an intermediate raw struct.
type HostListEntry struct {
	ID           uint64 `json:"id"`
	UUID         string `json:"uuid"`
	Hostname     string `json:"hostname"`
	ComputerName string `json:"computer_name"`
	Platform     string `json:"platform"`
	OSVersion    string `json:"os_version"`
}

// HostDetail is the per-host detail response. Field names map to the
// schema's declared booleans. JSON tags allow direct decode from Fleet's
// nested {"host": {...}} envelope via rawHostDetail.
type HostDetail struct {
	ID                    uint64 `json:"id"`
	UUID                  string `json:"uuid"`
	Hostname              string `json:"hostname"`
	ComputerName          string `json:"computer_name"`
	Platform              string `json:"platform"`
	OSVersion             string `json:"os_version"`
	DiskEncryptionEnabled bool   `json:"disk_encryption_enabled"`
	// Fleet's REST payload uses "screenlock_enabled"; the schema we emit
	// uses "screen_lock_enabled". The tag here matches the wire shape.
	ScreenLockEnabled bool `json:"screenlock_enabled"`
	FirewallEnabled   bool `json:"firewall_enabled"`
	MDMEnrolled       bool `json:"mdm_enrolled"`
}

// FleetAPI is the narrow Fleet surface Pull depends on. The concrete REST
// transport (FleetClient) satisfies it; tests inject a fake.
type FleetAPI interface {
	ListHosts(ctx context.Context) ([]HostListEntry, error)
	GetHost(ctx context.Context, id uint64) (*HostDetail, error)
}

// LocalQueryer is the narrow local-osqueryd surface Pull depends on when
// running in --mode=local. Returns one map per host (osquery query rows).
type LocalQueryer interface {
	Query(ctx context.Context, sql string) ([]map[string]string, error)
}

// PullFromFleet lists every host and fetches per-host detail. Errors
// fetching one host's detail are recorded as Result=Inconclusive on that
// row rather than aborting the whole run.
func PullFromFleet(ctx context.Context, api FleetAPI, now func() time.Time) ([]HostPosture, error) {
	if api == nil {
		return nil, errors.New("osqueryposture: FleetAPI is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	hosts, err := api.ListHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list fleet hosts: %w", err)
	}
	out := make([]HostPosture, 0, len(hosts))
	for i := range hosts {
		h := hosts[i]
		if h.UUID == "" {
			// Anti-criterion: no host_uuid means no canonical identity to
			// derive an idempotency key from. Skip rather than fabricate.
			continue
		}
		row := HostPosture{
			HostUUID:   h.UUID,
			Hostname:   firstNonEmpty(h.Hostname, h.ComputerName),
			Platform:   normalizePlatform(h.Platform),
			OSVersion:  h.OSVersion,
			ObservedAt: now(),
		}
		detail, err := api.GetHost(ctx, h.ID)
		if err != nil {
			row.Result = ResultInconclusive
			row.Reason = "detail fetch failed: " + err.Error()
			out = append(out, row)
			continue
		}
		fillFromDetail(&row, detail)
		row.Result, row.Reason = evaluate(row)
		out = append(out, row)
	}
	return out, nil
}

// PullFromLocal runs one osquery SQL query against the local osqueryd
// socket and returns one HostPosture (single host = single row).
func PullFromLocal(ctx context.Context, q LocalQueryer, now func() time.Time) ([]HostPosture, error) {
	if q == nil {
		return nil, errors.New("osqueryposture: LocalQueryer is nil")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	// osquery 5.x: system_info exposes uuid + hostname + computer_name;
	// os_version exposes platform + version; disk_encryption exposes
	// encrypted on darwin/linux; screenlock + alf (macOS app firewall)
	// approximate the schema booleans. The query joins these so we get
	// one row per local host. Tests inject the row directly.
	rows, err := q.Query(ctx, localPostureSQL)
	if err != nil {
		return nil, fmt.Errorf("local osqueryd query: %w", err)
	}
	out := make([]HostPosture, 0, len(rows))
	for _, r := range rows {
		uuid := r["uuid"]
		if uuid == "" {
			continue
		}
		row := HostPosture{
			HostUUID:              uuid,
			Hostname:              firstNonEmpty(r["hostname"], r["computer_name"]),
			Platform:              normalizePlatform(r["platform"]),
			OSVersion:             r["os_version"],
			DiskEncryptionEnabled: parseBool(r["disk_encryption_enabled"]),
			ScreenLockEnabled:     parseBool(r["screen_lock_enabled"]),
			FirewallEnabled:       parseBool(r["firewall_enabled"]),
			MDMEnrolled:           parseBool(r["mdm_enrolled"]),
			ObservedAt:            now(),
		}
		row.Result, row.Reason = evaluate(row)
		out = append(out, row)
	}
	return out, nil
}

// localPostureSQL is the SQL the local mode submits to osqueryd. Kept here
// (not in cmd) so the README and the implementation cannot drift.
const localPostureSQL = `
SELECT
  s.uuid                      AS uuid,
  s.hostname                  AS hostname,
  s.computer_name             AS computer_name,
  o.platform                  AS platform,
  o.version                   AS os_version,
  COALESCE(d.encrypted, '0')  AS disk_encryption_enabled,
  COALESCE(sc.enabled, '0')   AS screen_lock_enabled,
  COALESCE(a.global_state, '0') AS firewall_enabled,
  COALESCE(m.enrolled, '0')   AS mdm_enrolled
FROM system_info s
LEFT JOIN os_version o
LEFT JOIN disk_encryption d ON d.name = s.hostname
LEFT JOIN screenlock sc
LEFT JOIN alf a
LEFT JOIN mdm m
LIMIT 1;
`

// evaluate computes pass/fail. v1 baseline: disk encryption AND screen
// lock both enabled = PASS. Either off = FAIL. Inconclusive when both
// booleans came back unset and the platform is unknown. The connector
// keeps this rule shallow; the evaluator owns the policy ladder.
func evaluate(p HostPosture) (Result, string) {
	switch {
	case p.DiskEncryptionEnabled && p.ScreenLockEnabled:
		return ResultPass, "disk_encryption+screen_lock"
	case !p.DiskEncryptionEnabled && !p.ScreenLockEnabled && p.Platform == "":
		return ResultInconclusive, "no platform / no booleans"
	case !p.DiskEncryptionEnabled:
		return ResultFail, "disk_encryption_disabled"
	default:
		return ResultFail, "screen_lock_disabled"
	}
}

// ---- Fleet REST client ----

type rawHostListResponse struct {
	Hosts []HostListEntry `json:"hosts"`
}

// rawHostDetail unwraps Fleet's {"host": {...}} envelope.
type rawHostDetail struct {
	Host HostDetail `json:"host"`
}

// FleetClient is a thin HTTP client for the Fleet host-detail endpoints.
type FleetClient struct {
	HTTP    *http.Client
	BaseURL string
	Creds   osqueryauth.Credential
}

// NewFleetClient builds a FleetClient against the Fleet REST API.
func NewFleetClient(httpClient *http.Client, baseURL string, creds osqueryauth.Credential) *FleetClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &FleetClient{HTTP: httpClient, BaseURL: strings.TrimRight(baseURL, "/"), Creds: creds}
}

// ListHosts fetches the first page of hosts. v1 pulls up to 1000;
// pagination lands when fleet sizes demand it.
func (c *FleetClient) ListHosts(ctx context.Context) ([]HostListEntry, error) {
	u := c.BaseURL + "/api/v1/fleet/hosts?per_page=1000"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, &APIError{Status: res.StatusCode, Retry: res.Header.Get("Retry-After"), Body: drain(res.Body)}
	}
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var raw rawHostListResponse
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode hosts: %w", err)
	}
	return raw.Hosts, nil
}

// GetHost fetches one host's detail. Returns ErrHostNotFound when the host
// has been removed between list and detail calls (Fleet returns 404).
func (c *FleetClient) GetHost(ctx context.Context, id uint64) (*HostDetail, error) {
	if id == 0 {
		return nil, errors.New("osqueryposture: host id required")
	}
	u := c.BaseURL + "/api/v1/fleet/hosts/" + strconv.FormatUint(id, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.Creds.Apply(req)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrHostNotFound
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, &APIError{Status: res.StatusCode, Retry: res.Header.Get("Retry-After"), Body: drain(res.Body)}
	}
	if res.StatusCode != http.StatusOK {
		return nil, &APIError{Status: res.StatusCode, Body: drain(res.Body)}
	}
	var d rawHostDetail
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("decode host: %w", err)
	}
	out := d.Host
	return &out, nil
}

// ErrHostNotFound is the sentinel Fleet returns when a host has been
// removed between the list call and the detail call.
var ErrHostNotFound = errors.New("osqueryposture: fleet host not found")

// APIError carries Fleet REST error context. Retry is the verbatim
// Retry-After header when present (Fleet returns 429 on rate-limit hit).
type APIError struct {
	Status int
	Retry  string
	Body   string
}

func (e *APIError) Error() string {
	if e.Body == "" {
		if e.Retry != "" {
			return fmt.Sprintf("fleet: HTTP %d (retry-after=%s)", e.Status, e.Retry)
		}
		return fmt.Sprintf("fleet: HTTP %d", e.Status)
	}
	if e.Retry != "" {
		return fmt.Sprintf("fleet: HTTP %d (retry-after=%s): %s", e.Status, e.Retry, e.Body)
	}
	return fmt.Sprintf("fleet: HTTP %d: %s", e.Status, e.Body)
}

// ---- helpers ----

func fillFromDetail(p *HostPosture, d *HostDetail) {
	if d == nil {
		return
	}
	// Detail call returns more authoritative names than the list call.
	if d.Hostname != "" {
		p.Hostname = d.Hostname
	} else if d.ComputerName != "" && p.Hostname == "" {
		p.Hostname = d.ComputerName
	}
	if d.OSVersion != "" {
		p.OSVersion = d.OSVersion
	}
	if d.Platform != "" {
		p.Platform = normalizePlatform(d.Platform)
	}
	p.DiskEncryptionEnabled = d.DiskEncryptionEnabled
	p.ScreenLockEnabled = d.ScreenLockEnabled
	p.FirewallEnabled = d.FirewallEnabled
	p.MDMEnrolled = d.MDMEnrolled
}

// normalizePlatform collapses Fleet/osquery vendor strings to the
// canonical {"darwin", "linux", "windows"} set the schema documents.
func normalizePlatform(in string) string {
	s := strings.ToLower(strings.TrimSpace(in))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "darwin") || strings.Contains(s, "macos") || strings.Contains(s, "osx") || s == "mac":
		return "darwin"
	case strings.Contains(s, "windows") || strings.HasPrefix(s, "win"):
		return "windows"
	case strings.Contains(s, "linux") || strings.Contains(s, "rhel") || strings.Contains(s, "centos") || strings.Contains(s, "ubuntu") || strings.Contains(s, "debian"):
		return "linux"
	default:
		return s
	}
}

// parseBool accepts the common osquery-row boolean shapes: "1", "true",
// "yes" (case-insensitive). Everything else is false.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func drain(r io.Reader) string {
	const max = 1 << 13
	b, _ := io.ReadAll(io.LimitReader(r, max))
	return string(b)
}
