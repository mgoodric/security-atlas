// Package routed names the evidence kinds that are routed to controls by
// evidence type (#2924).
//
// A connector observes a system, not a control: one github.repo_protection.v2
// record answers both cc8_1 bundles, one osquery.host_posture.v1 record
// answers cc6_6 and cc4_1. A record of a routed kind is therefore stored bound
// to no control (control_id NULL); any wire control_id is kept only as
// provenance in control_ref. It is evidence for every control in the tenant
// whose evidence queries declare its kind.
//
// The three categories (ADR 0028):
//
//   - bound: every kind not listed here or in internal/evidence/unbound. A
//     record reaches the control its control_id names, and no other.
//   - routed by type: this list. Pushed by connectors under the ordinary
//     credential and scope checks.
//   - owned: internal/evidence/unbound. Also control-free, but written only by
//     its platform handler; a push of one is refused (ingest.OwnedKind).
//
// Every ledger reader that resolves a control's evidence applies one rule in
// SQL, with ControlFreeKinds() bound as its unbound_kinds parameter:
//
//	control_id IS NULL
//	AND evidence_kind = ANY(unbound_kinds)
//	AND <the control's evidence_queries> @> [{"evidence_kind": evidence_kind}]
//
// Readers that also match control_ref against the control's UUID bind Kinds()
// as routed_kinds and skip that leg for a routed kind (#2931): a routed record
// pushed with an explicit control UUID keeps it in control_ref as provenance
// only, so it reaches a control through the rule above or not at all.
//
// The list is closed: TestStockBundleKindsAreCategorized fails when a stock
// bundle reads a kind that is in no category.
package routed

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/evidence/unbound"
)

// kinds is the closed list of routed connector kinds.
var kinds = []string{
	"1password.org_policy.v2",
	"aws.s3.bucket_encryption_state.v1",
	"github.audit_event.v1",
	"github.repo_protection.v1",
	"github.repo_protection.v2",
	"jira.ticket_evidence.v1",
	"k8s.networkpolicy_coverage.v1",
	"monitoring.host_capacity.v1",
	"okta.app_assignment.v1",
	"okta.mfa_policy.v2",
	"okta.user_lifecycle.v1",
	"osquery.host_posture.v1",
	"sast.scan_result.v1",
	"slack.retention_settings.v1",
}

// lateKinds are the routed kinds that were bound first, with records already
// in the ledger, when they became routed. Each has its own cutover row in
// evidence_routing_kind_cutover, written by the migration that routed it,
// and a pinned read compares against that instant instead of the first
// cutover: a period frozen between the two read none of the kind's
// control-free records and keeps reading none. A kind routed from its first
// record needs no entry here.
var lateKinds = []string{
	"sast.scan_result.v1", // #2973, migration 20261304000000
}

// Kinds returns a copy of the routed kinds, never nil.
func Kinds() []string {
	return append([]string{}, kinds...)
}

// Kind reports whether kind is routed by type.
func Kind(kind string) bool {
	return slices.Contains(kinds, kind)
}

// ControlFreeKinds returns every kind whose control-free records a control
// reads by kind: the owned unbound kinds plus the routed kinds. Ledger
// readers bind it as unbound_kinds.
func ControlFreeKinds() []string {
	return append(unbound.Kinds(), kinds...)
}

// Within returns the routed kinds in kinds, never nil. A reader bound to a
// pinned set binds Within(set) as routed_kinds, so a read pinned before the
// cutover keeps the control_ref leg for every kind, as it did before routing
// shipped (#2931), and a read at or after it closes the leg for routed kinds.
func Within(kinds []string) []string {
	out := []string{}
	for _, k := range kinds {
		if Kind(k) {
			out = append(out, k)
		}
	}
	return out
}

// Cutovers holds when each routed kind started routing: the first cutover
// (evidence_routing_cutover, #2924) for every kind routed then, and its own
// instant for each of lateKinds (evidence_routing_kind_cutover, #2973).
type Cutovers struct {
	First time.Time
	Late  map[string]time.Time
}

// For returns the instant kind started routing.
func (c Cutovers) For(kind string) time.Time {
	if t, ok := c.Late[kind]; ok {
		return t
	}
	return c.First
}

// KindsAsOf returns the control-free kinds for a read pinned at anchor. A
// frozen audit period anchors on its frozen_at; a population on the earlier
// of its created_at and frozen_at. The owned kinds are always in the set; a
// routed kind is in it only when anchor is at or after the instant that kind
// started routing (Cutovers.For). So a period frozen or a population drawn
// before a kind was routed keeps exactly the records it had. Unanchored (an
// open period, a live read), it is ControlFreeKinds.
//
// Records bound to a control are unaffected either way: they match by
// control_id, not by this list.
func KindsAsOf(c Cutovers, anchor time.Time) []string {
	if anchor.IsZero() {
		return ControlFreeKinds()
	}
	out := unbound.Kinds()
	for _, k := range kinds {
		if !anchor.Before(c.For(k)) {
			out = append(out, k)
		}
	}
	return out
}

// Cutover reads every routing cutover inside the caller's transaction. A
// missing first cutover, or a late kind without its own row, is an error,
// never "no cutover": a pinned read must not silently widen.
func Cutover(ctx context.Context, q *dbx.Queries) (Cutovers, error) {
	first, err := q.GetEvidenceRoutingCutover(ctx)
	if err != nil {
		return Cutovers{}, fmt.Errorf("routed: read evidence routing cutover: %w", err)
	}
	if !first.Valid {
		return Cutovers{}, fmt.Errorf("routed: evidence routing cutover is NULL")
	}
	rows, err := q.ListEvidenceRoutingKindCutovers(ctx)
	if err != nil {
		return Cutovers{}, fmt.Errorf("routed: read per-kind routing cutovers: %w", err)
	}
	return cutoversFrom(first.Time, rows)
}

// cutoversFrom builds Cutovers from the stored rows, refusing a late kind
// without a valid row.
func cutoversFrom(first time.Time, rows []dbx.EvidenceRoutingKindCutover) (Cutovers, error) {
	c := Cutovers{First: first, Late: map[string]time.Time{}}
	for _, r := range rows {
		if r.CutAt.Valid && slices.Contains(lateKinds, r.EvidenceKind) {
			c.Late[r.EvidenceKind] = r.CutAt.Time
		}
	}
	for _, k := range lateKinds {
		if _, ok := c.Late[k]; !ok {
			return Cutovers{}, fmt.Errorf("routed: no routing cutover for %s in evidence_routing_kind_cutover", k)
		}
	}
	return c, nil
}

// KindsForPeriod returns the kinds for a read over an audit period whose
// frozen_at is frozenAt (invalid while the period is open).
func KindsForPeriod(ctx context.Context, q *dbx.Queries, frozenAt pgtype.Timestamptz) ([]string, error) {
	if !frozenAt.Valid {
		return ControlFreeKinds(), nil
	}
	cut, err := Cutover(ctx, q)
	if err != nil {
		return nil, err
	}
	return KindsAsOf(cut, frozenAt.Time), nil
}

// KindsForPopulation returns the kinds for a draw over pop: pinned when the
// population was created, or frozen, before the cutover.
func KindsForPopulation(ctx context.Context, q *dbx.Queries, pop dbx.Population) ([]string, error) {
	cut, err := Cutover(ctx, q)
	if err != nil {
		return nil, err
	}
	return PopulationKindsAt(cut, pop), nil
}

// PopulationKindsAt is KindsForPopulation with the cutovers already read.
func PopulationKindsAt(cutover Cutovers, pop dbx.Population) []string {
	return KindsAsOf(cutover, populationAnchor(pop))
}

func populationAnchor(pop dbx.Population) time.Time {
	var anchor time.Time
	if pop.CreatedAt.Valid {
		anchor = pop.CreatedAt.Time
	}
	if pop.FrozenAt.Valid && (anchor.IsZero() || pop.FrozenAt.Time.Before(anchor)) {
		anchor = pop.FrozenAt.Time
	}
	return anchor
}
