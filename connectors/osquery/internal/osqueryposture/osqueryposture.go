// Package osqueryposture pulls one HostPosture record per endpoint.
//
// Two upstream modes share the same canonical HostPosture model:
//
//   - Fleet API mode (developer docs at fleetdm.com/docs/rest-api/rest-api).
//     Two-call pull: GET /api/v1/fleet/hosts (paginated list) +
//     GET /api/v1/fleet/hosts/{id} (per-host detail). The documented
//     Get host response carries `disk_encryption_enabled` (omitted when
//     Fleet does not know it), `mdm.enrollment_status`, `seen_time` and
//     `policies[]`. It carries no firewall or screen-lock field (#2912).
//     Those two are read only from operator-written Fleet policies named
//     in FleetPolicies: a policy's `response` of "pass" or "fail" becomes
//     true or false, and anything else leaves the field unread (#2921).
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
// osquery.host_posture.v1 schema at 1.2.0 (host_uuid, hostname, platform,
// os_version, disk_encryption_enabled, screen_lock_enabled,
// firewall_enabled, mdm_enrolled, last_seen_at, reason, firewall_source,
// screen_lock_source; 1.3.0 adds disk_used_percent, host_role(_source),
// antimalware_protected/_source and edr_healthy/_source). The schema
// declares additionalProperties:false so this package emits only those
// fields. A posture field the connector did not read is nil and is left
// out of the payload, never sent as false (#2912).
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
//
// The four posture booleans are tri-state: nil means the connector could
// not read the value (the detail fetch failed, or the source has no such
// field), and the cmd layer omits it from the payload. A payload-reading
// check must not see an unread firewall as a firewall that is off (#2912).
type HostPosture struct {
	HostUUID              string
	Hostname              string
	Platform              string // "darwin", "linux", "windows"
	OSVersion             string
	DiskEncryptionEnabled *bool
	ScreenLockEnabled     *bool
	FirewallEnabled       *bool
	MDMEnrolled           *bool

	// FirewallSource and ScreenLockSource name where FirewallEnabled and
	// ScreenLockEnabled were read from, e.g. "fleet_policy:<name>". Empty
	// when the matching boolean is nil, or when the source is the local
	// osquery row.
	FirewallSource   string
	ScreenLockSource string

	// DiskUsedPercent is the share of the host's disk in use, 0-100. Fleet:
	// 100 minus percent_disk_space_available, read only when Fleet also
	// reports a non-zero gigs_total_disk_space. nil when not read. Fleet's
	// host API has no memory-use or CPU-use field, so the connector emits
	// neither (#2826).
	DiskUsedPercent *float64

	// HostRole is "production" when the host carries the operator-named
	// production Fleet label, and empty (not read) otherwise; HostRoleSource
	// is then "fleet_label:<name>".
	HostRole       string
	HostRoleSource string

	// AntimalwareProtected and EDRHealthy are read only from operator-named
	// Fleet policies, like firewall and screen lock: Fleet's host API has no
	// antimalware or EDR field. The policy's query holds what "protected"
	// and "healthy" mean (engine, definitions age, agent health). nil when
	// not read; the *Source fields name the policy.
	AntimalwareProtected *bool
	AntimalwareSource    string
	EDRHealthy           *bool
	EDRSource            string

	// LastSeen is when the host last checked in with Fleet (Fleet's
	// `seen_time`). Zero when unknown. It is not ObservedAt: Fleet lists
	// offline hosts too, so the pull time says only that the connector
	// ran, and ingest rejects an observed_at more than 24h from receipt
	// (internal/evidence/ingest MaxObservedAtSkew), so an offline host's
	// last-seen time cannot be the record's observed_at.
	LastSeen time.Time

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
	// SeenTime is Fleet's last check-in time (RFC 3339, e.g.
	// "2021-08-19T21:14:58Z"). Read from the list so a host whose detail
	// fetch fails still reports when it was last seen.
	SeenTime string `json:"seen_time"`
}

// HostDetail is the per-host detail response, decoded from Fleet's
// {"host": {...}} envelope via rawHostDetail. Only fields the documented
// Get host response carries are declared: there is no firewall or
// screen-lock field, and MDM enrolment is the nested
// mdm.enrollment_status string, not a boolean.
type HostDetail struct {
	ID           uint64 `json:"id"`
	UUID         string `json:"uuid"`
	Hostname     string `json:"hostname"`
	ComputerName string `json:"computer_name"`
	Platform     string `json:"platform"`
	OSVersion    string `json:"os_version"`
	SeenTime     string `json:"seen_time"`
	// DiskEncryptionEnabled is nil when Fleet omits the key, which its
	// docs say happens when it doesn't know the status, and for Linux
	// hosts reporting an unencrypted root volume.
	DiskEncryptionEnabled *bool        `json:"disk_encryption_enabled"`
	MDM                   *HostMDM     `json:"mdm"`
	Policies              []HostPolicy `json:"policies"`
	// PercentDiskSpaceAvailable and GigsTotalDiskSpace are the documented
	// Get host disk keys (example values 71 and 246). A zero or missing
	// total is taken as "Fleet has no disk reading yet", not a full disk.
	PercentDiskSpaceAvailable *float64    `json:"percent_disk_space_available"`
	GigsTotalDiskSpace        *float64    `json:"gigs_total_disk_space"`
	Labels                    []HostLabel `json:"labels"`
}

// HostLabel is the subset of one Get host `labels[]` entry the connector
// reads.
type HostLabel struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
}

// HostPolicy is the subset of one Get host `policies[]` entry the
// connector reads. Response examples in Fleet's docs: "pass", "fail", and
// "" (no result reported for that host).
type HostPolicy struct {
	ID       uint64 `json:"id"`
	Name     string `json:"name"`
	Response string `json:"response"`
}

// FleetPolicies names the operator's Fleet policies (and the one label)
// that answer posture questions Fleet's host API has no field for. An empty
// name means the field is not read. The operator writes and maintains the
// policies and the label; the connector only reads each host's policy
// response and label membership (#2921, #2826).
type FleetPolicies struct {
	Firewall    string
	ScreenLock  string
	Antimalware string
	EDR         string
	// ProductionLabel is a Fleet label, not a policy: a host carrying it is
	// host_role "production".
	ProductionLabel string
}

// HostMDM is the subset of Fleet's host `mdm` object the connector reads.
type HostMDM struct {
	// EnrollmentStatus examples in Fleet's docs: "On (manual)",
	// "On (automatic)", "Pending".
	EnrollmentStatus string `json:"enrollment_status"`
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
// row rather than aborting the whole run. pol names the Fleet policies
// firewall and screen lock are read from; the zero value reads neither.
func PullFromFleet(ctx context.Context, api FleetAPI, now func() time.Time, pol FleetPolicies) ([]HostPosture, error) {
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
			LastSeen:   parseSeenTime(h.SeenTime),
			ObservedAt: now(),
		}
		detail, err := api.GetHost(ctx, h.ID)
		if err != nil {
			row.Result = ResultInconclusive
			row.Reason = "detail fetch failed: " + err.Error()
			out = append(out, row)
			continue
		}
		fillFromDetail(&row, detail, pol)
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
	// one row per local host. A NULL column (table absent on the
	// platform, or no joined row) stays unread. Tests inject the row
	// directly.
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
			DiskUsedPercent:       parsePercent(r["disk_used_percent"]),
			LastSeen:              now(),
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
  d.encrypted                 AS disk_encryption_enabled,
  sc.enabled                  AS screen_lock_enabled,
  a.global_state              AS firewall_enabled,
  m.enrolled                  AS mdm_enrolled,
  CASE WHEN mt.blocks > 0
       THEN ROUND(100.0 * (mt.blocks - mt.blocks_available) / mt.blocks, 1)
  END                         AS disk_used_percent
FROM system_info s
LEFT JOIN os_version o
LEFT JOIN disk_encryption d ON d.name = s.hostname
LEFT JOIN screenlock sc
LEFT JOIN alf a
LEFT JOIN mdm m
LEFT JOIN mounts mt ON mt.path = '/'
LIMIT 1;
`

// evaluate computes pass/fail. v1 baseline: disk encryption AND screen
// lock both observed enabled = PASS. Either observed off = FAIL. Anything
// unread and nothing observed off = INCONCLUSIVE, with the unread fields
// named in the reason. In Fleet mode screen lock is read only from a
// named Fleet policy, so without --screenlock-policy a Fleet host is FAIL
// (disk encryption off) or INCONCLUSIVE, never PASS.
// The connector keeps this rule shallow; the evaluator owns the policy
// ladder.
func evaluate(p HostPosture) (Result, string) {
	switch {
	case isFalse(p.DiskEncryptionEnabled):
		return ResultFail, "disk_encryption_disabled"
	case isFalse(p.ScreenLockEnabled):
		return ResultFail, "screen_lock_disabled"
	case p.DiskEncryptionEnabled == nil && p.ScreenLockEnabled == nil:
		return ResultInconclusive, "disk_encryption and screen_lock not reported"
	case p.DiskEncryptionEnabled == nil:
		return ResultInconclusive, "disk_encryption not reported"
	case p.ScreenLockEnabled == nil:
		return ResultInconclusive, "screen_lock not reported"
	default:
		return ResultPass, "disk_encryption+screen_lock"
	}
}

func isFalse(b *bool) bool { return b != nil && !*b }

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

func fillFromDetail(p *HostPosture, d *HostDetail, pol FleetPolicies) {
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
	if t := parseSeenTime(d.SeenTime); !t.IsZero() {
		p.LastSeen = t
	}
	p.DiskEncryptionEnabled = d.DiskEncryptionEnabled
	if d.MDM != nil {
		p.MDMEnrolled = mdmEnrolled(d.MDM.EnrollmentStatus)
	}
	// Screen lock and firewall: the Get host response has no such field,
	// so they are read only from the operator's named policies, and stay
	// nil (unread) otherwise.
	if v := policyResponse(d.Policies, pol.Firewall); v != nil {
		p.FirewallEnabled = v
		p.FirewallSource = "fleet_policy:" + pol.Firewall
	}
	if v := policyResponse(d.Policies, pol.ScreenLock); v != nil {
		p.ScreenLockEnabled = v
		p.ScreenLockSource = "fleet_policy:" + pol.ScreenLock
	}
	// Antimalware and EDR: likewise no Get host field (#2826).
	if v := policyResponse(d.Policies, pol.Antimalware); v != nil {
		p.AntimalwareProtected = v
		p.AntimalwareSource = "fleet_policy:" + pol.Antimalware
	}
	if v := policyResponse(d.Policies, pol.EDR); v != nil {
		p.EDRHealthy = v
		p.EDRSource = "fleet_policy:" + pol.EDR
	}
	p.DiskUsedPercent = diskUsedPercent(d)
	if pol.ProductionLabel != "" && hasLabel(d.Labels, pol.ProductionLabel) {
		p.HostRole = "production"
		p.HostRoleSource = "fleet_label:" + pol.ProductionLabel
	}
}

// diskUsedPercent derives disk use from Fleet's percent_disk_space_available.
// It is nil when Fleet omits the percentage, when gigs_total_disk_space is
// missing or zero (no disk reading yet, which would otherwise read as a
// full disk), or when the percentage is outside 0-100.
func diskUsedPercent(d *HostDetail) *float64 {
	if d.PercentDiskSpaceAvailable == nil || d.GigsTotalDiskSpace == nil || *d.GigsTotalDiskSpace <= 0 {
		return nil
	}
	avail := *d.PercentDiskSpaceAvailable
	if avail < 0 || avail > 100 {
		return nil
	}
	used := 100 - avail
	return &used
}

// hasLabel reports whether the host carries a label with exactly this name.
func hasLabel(labels []HostLabel, name string) bool {
	for _, l := range labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

// policyResponse reads the named policy's result for one host: "pass" is
// true and "fail" is false. An empty name, no policy of that name, or any
// other response (Fleet reports "" when the host has no result for the
// policy) is nil, never a guess. Names match exactly. If more than one
// policy carries the name (a global and a team policy, say) and their
// results disagree, the result is nil too.
func policyResponse(policies []HostPolicy, name string) *bool {
	if name == "" {
		return nil
	}
	var out *bool
	for _, pl := range policies {
		if pl.Name != name {
			continue
		}
		var v bool
		switch pl.Response {
		case "pass":
			v = true
		case "fail":
			v = false
		default:
			return nil
		}
		if out != nil && *out != v {
			return nil
		}
		out = boolPtr(v)
	}
	return out
}

// mdmEnrolled maps Fleet's mdm.enrollment_status to a boolean. "On (...)"
// is enrolled; "Off" and "Pending" are not; anything else, including an
// empty string, is unread rather than guessed.
func mdmEnrolled(status string) *bool {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == "on" || strings.HasPrefix(s, "on "):
		return boolPtr(true)
	case s == "off" || s == "pending":
		return boolPtr(false)
	default:
		return nil
	}
}

// parseSeenTime parses Fleet's RFC 3339 seen_time. An empty or
// unparsable value is unknown (zero), never the pull time.
func parseSeenTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func boolPtr(b bool) *bool { return &b }

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
// "yes", "on" are true; "0", "false", "no", "off" are false
// (case-insensitive). Anything else, including an empty (NULL) column,
// is unread.
func parseBool(s string) *bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return boolPtr(true)
	case "0", "false", "no", "off":
		return boolPtr(false)
	default:
		return nil
	}
}

// parsePercent reads an osquery-row percentage. An empty (NULL) column, a
// non-number, or a value outside 0-100 is unread.
func parsePercent(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 100 {
		return nil
	}
	return &v
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
