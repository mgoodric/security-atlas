// Package metrics derives SERVICE-/TEAM-level incident-response performance
// AGGREGATES (MTTA / MTTR — mean + percentiles, plus counts) over a bounded
// look-back window via the read-only PagerDuty REST API, and emits one
// pagerduty.response_metrics.v1 record per service. It proves incidents are
// acknowledged and resolved within target windows (SOC 2 CC7.4 "responds to
// identified security incidents"; SCF IRO-02 Incident Handling, MON-02
// Continuous Monitoring) at the PROGRAM level — not a per-engineer scorecard.
//
// The load-bearing guard (P0-539 / threat-model I — DOMINANT): per-responder
// timing data profiles named individuals (a privacy + works-council concern in
// some jurisdictions). This package collects timing FACTS only — for each
// incident, the time from creation to FIRST acknowledgment and (if resolved)
// from creation to resolution — and immediately rolls them up to a
// SERVICE-level aggregate. It NEVER materializes or emits which RESPONDER
// acknowledged or resolved an incident: the acknowledgment's `acknowledger`
// (the responder identity) is structurally absent from every connector-side
// type, and json.Decode discards JSON keys with no matching struct field, so
// the responder identity never enters memory as connector data even when the
// PagerDuty payload carries it.
//
// The structural guarantee: RawTiming / RawAck / ServiceMetrics have NO field
// capable of holding a responder identity (name / email / id / contact) BY
// CONSTRUCTION. A reflection guard (metrics_guard_test.go) fails the build if a
// per-responder-identity field is ever added; a drop test feeds source data
// WITH named acknowledgers and proves no per-named-responder identity becomes
// the grain of any emitted metrics record.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// MaxServices caps how many service-aggregate records one run will emit
// (threat-model D — DoS guard). The bounded look-back window plus the per-page
// limit plus this hard cap keep a run bounded regardless of how many incidents
// the source returns. A program with more than this many distinct services in
// one window is far outside the solo-leader persona this connector targets.
const MaxServices = 500

// API is the narrow surface Collect depends on. The concrete implementation
// issues read-only PagerDuty API calls; tests pass a fake. v0 reads a bounded
// page set; cursor pagination beyond the cap is a documented follow-on
// (threat-model D).
type API interface {
	// ListIncidentTimings returns one RawTiming per incident in [since, until],
	// each carrying ONLY the incident's service identity and the timestamps
	// needed for MTTA / MTTR — never which responder acted.
	ListIncidentTimings(ctx context.Context, since, until time.Time) ([]RawTiming, error)
}

// RawAck is the narrow, identity-free view of one acknowledgment event. There
// is NO Acknowledger / Responder / User / Name / Email / Contact field here BY
// CONSTRUCTION (P0-539): we read ONLY when the acknowledgment happened, to
// derive time-to-acknowledge. Which individual acknowledged is deliberately
// never decoded — it would turn the ledger into an individual-performance
// surveillance store (threat-model I, DOMINANT).
type RawAck struct {
	// At is when the acknowledgment occurred. The only fact MTTA needs.
	At time.Time
}

// RawTiming is the narrow, identity-free view the PagerDuty client returns for
// one incident's response timing. It carries the affected service's opaque id
// (the aggregation grain) and the timestamps needed to derive MTTA / MTTR. The
// HTTP client maps the API response into this shape, discarding every responder
// identity (the acknowledger, the assignee, the resolver) at the decode
// boundary. Tests construct it directly. There is NO responder-identity field
// here BY CONSTRUCTION (P0-539).
type RawTiming struct {
	// ServiceID is the opaque PagerDuty service identifier — the aggregation
	// grain. A service / team aggregate, never a per-responder one.
	ServiceID string
	// CreatedAt is when the incident was triggered.
	CreatedAt time.Time
	// Acks are the acknowledgment events, identity-free. The earliest `At`
	// drives time-to-acknowledge.
	Acks []RawAck
	// ResolvedAt is when the incident was resolved; zero if still open.
	ResolvedAt time.Time
}

// ServiceMetrics is the normalized SERVICE-level aggregate for one service over
// the window. It carries MTTA / MTTR central tendency + percentiles + counts —
// NEVER a responder identity. NO field can hold an individual responder BY
// CONSTRUCTION (P0-539). All duration fields are whole seconds.
type ServiceMetrics struct {
	// ServiceID is the opaque service identifier this aggregate is for.
	ServiceID string

	// IncidentCount is the total number of incidents observed for the service
	// in the window (the denominator that gives the aggregates meaning).
	IncidentCount int

	// AcknowledgedCount is how many of those incidents were acknowledged at
	// least once (the MTTA sample size).
	AcknowledgedCount int
	// ResolvedCount is how many of those incidents were resolved (the MTTR
	// sample size).
	ResolvedCount int

	// Time-to-acknowledge aggregates over AcknowledgedCount incidents, seconds.
	// Zero when AcknowledgedCount == 0.
	MTTASecondsMean int64
	MTTASecondsP50  int64
	MTTASecondsP90  int64
	MTTASecondsP95  int64

	// Time-to-resolve aggregates over ResolvedCount incidents, seconds. Zero
	// when ResolvedCount == 0.
	MTTRSecondsMean int64
	MTTRSecondsP50  int64
	MTTRSecondsP90  int64
	MTTRSecondsP95  int64
}

// Collect lists per-incident timings in [since, until], aggregates them to
// SERVICE-level MTTA / MTTR aggregates, and returns one ServiceMetrics per
// service. The per-responder identity is never an aggregation grain — the only
// grain is ServiceID (P0-539). Separated from record-building so the cmd layer
// owns the observed-at clock. The result is hard-capped at MaxServices (DoS
// guard) and ordered by ServiceID for deterministic output.
func Collect(ctx context.Context, api API, since, until time.Time) ([]ServiceMetrics, error) {
	if api == nil {
		return nil, errors.New("metrics: API is nil")
	}
	raw, err := api.ListIncidentTimings(ctx, since, until)
	if err != nil {
		return nil, fmt.Errorf("list pagerduty incident timings: %w", err)
	}

	// Accumulate per-service samples. The map key is the opaque service id —
	// the ONLY grain. No code path here keys on, stores, or emits a responder.
	type accum struct {
		count int
		atta  []int64 // time-to-acknowledge samples, seconds
		ttr   []int64 // time-to-resolve samples, seconds
	}
	byService := map[string]*accum{}
	order := make([]string, 0)

	for _, in := range raw {
		svc := strings.TrimSpace(in.ServiceID)
		if svc == "" || in.CreatedAt.IsZero() {
			// An incident with no service grain or no created-at anchor cannot
			// contribute to a service aggregate; drop it.
			continue
		}
		a, ok := byService[svc]
		if !ok {
			a = &accum{}
			byService[svc] = a
			order = append(order, svc)
		}
		a.count++

		if ackAt, ok := firstAck(in.Acks); ok {
			if d := ackAt.Sub(in.CreatedAt); d >= 0 {
				a.atta = append(a.atta, int64(d/time.Second))
			}
		}
		if !in.ResolvedAt.IsZero() {
			if d := in.ResolvedAt.Sub(in.CreatedAt); d >= 0 {
				a.ttr = append(a.ttr, int64(d/time.Second))
			}
		}
	}

	sort.Strings(order)
	out := make([]ServiceMetrics, 0, len(order))
	for _, svc := range order {
		a := byService[svc]
		sm := ServiceMetrics{
			ServiceID:         svc,
			IncidentCount:     a.count,
			AcknowledgedCount: len(a.atta),
			ResolvedCount:     len(a.ttr),
			MTTASecondsMean:   mean(a.atta),
			MTTASecondsP50:    percentile(a.atta, 50),
			MTTASecondsP90:    percentile(a.atta, 90),
			MTTASecondsP95:    percentile(a.atta, 95),
			MTTRSecondsMean:   mean(a.ttr),
			MTTRSecondsP50:    percentile(a.ttr, 50),
			MTTRSecondsP90:    percentile(a.ttr, 90),
			MTTRSecondsP95:    percentile(a.ttr, 95),
		}
		out = append(out, sm)
		if len(out) >= MaxServices {
			break
		}
	}
	return out, nil
}

// firstAck returns the earliest acknowledgment timestamp, if any. MTTA measures
// time-to-FIRST-acknowledge; later acks (re-assignments) do not change it.
func firstAck(acks []RawAck) (time.Time, bool) {
	var earliest time.Time
	found := false
	for _, ack := range acks {
		if ack.At.IsZero() {
			continue
		}
		if !found || ack.At.Before(earliest) {
			earliest = ack.At
			found = true
		}
	}
	return earliest, found
}

// mean returns the integer-second mean of the samples, or 0 for an empty set.
func mean(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	var sum int64
	for _, x := range xs {
		sum += x
	}
	return sum / int64(len(xs))
}

// percentile returns the p-th percentile (0..100) of xs in seconds using the
// nearest-rank method, or 0 for an empty set. Nearest-rank is chosen over
// linear interpolation for explainability: the result is always an actually
// observed sample value, which an auditor can reconcile against a single
// incident. The input is copied before sorting so the caller's slice order is
// preserved.
func percentile(xs []int64, p int) int64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	sorted := make([]int64, n)
	copy(sorted, xs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	// Nearest-rank: rank = ceil(p/100 * n), 1-based; clamp to [1, n].
	rank := int(math.Ceil(float64(p) / 100.0 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}
