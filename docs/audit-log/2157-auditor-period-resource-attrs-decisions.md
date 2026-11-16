# 2157 — Auditor period rules match through the authz middleware: decisions log

Issue: mgoodric/security-atlas#2157. The auditor rules in
`audit_periods.rego` and `scope_cells.rego` compare the caller's
`input.user.attrs.audit_period_ids` (filled by `auditor.DBAttrsResolver`)
with `input.resource.attrs.audit_period_id` / `scope_cell_id`. Only one
production path filled `input.resource.attrs`: the audit-notes write
hydrator from #2193. So an auditor got 403 on populations, samples and
annotations, and, since #2381 (PR #2390) stopped auditors deriving
`control_owner`, on `GET /v1/evidence` and `/v1/evidence/{id}` too.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found by reading the code during
  the module-toggles design grill; the rego unit tests
  (`internal/authz/slice025_test.go`) set `in.Resource.Attrs` by hand, so
  they passed while the HTTP path denied.
- **detection_tier_target:** integration.
  `TestAuditorWorkflow2157_AssignedPeriodThroughAuthzmw` drives the real
  middleware chain (JWT, tenancy, OPA with the DB roles and attrs
  resolvers). On the old wiring and rego it fails at the first
  period-scoped write:
  `auditor POST /v1/samples in the assigned period: 403 map[]; want 201`.

## Decisions

### D1 — the hydrator resolves the resource side; the rules intersect

`internal/api/audit.ResourceAttrs` (an `authzmw.ResourceAttrsFunc`, chained
after the #2193 audit-notes one by `authzmw.ChainResourceAttrs`) looks up
the period a route's row belongs to and puts it on
`input.resource.attrs`. It does not know who is asking, so it runs for
every caller on these routes (one or two indexed reads under the caller's
RLS tenant). Rego does the comparison with the assigned periods, as it
already did for audit-notes.

| Route                                                             | Attribute                                                                                                         |
| ----------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `GET /v1/populations/{id}`                                        | `audit_period_id`: the population's                                                                               |
| `POST /v1/samples`                                                | `audit_period_id`: the body `population_id`'s population's                                                        |
| `GET /v1/samples/{id}`, `GET`/`POST /v1/samples/{id}/annotations` | `audit_period_id`: the sample's population's                                                                      |
| `GET /v1/evidence/{id}`                                           | `audit_period_ids`: every period with an attached population containing the record; `scope_cell_id`: the record's |
| `GET /v1/evidence?control_id=`                                    | `audit_period_ids`: every period with an attached population covering the whole control                           |

### D2 — an evidence record belongs to a period through its populations

An auditor assignment is period-only (#2118); nothing fills the user-side
`scope_cell_ids`, so `scope_cells.rego` cannot fire. In v1 an audit period
is tied to the evidence an auditor tests through the populations attached
to it. A record is in a period when an attached population contains it,
using the same bounds a sample draw uses: same control, `observed_at`
inside the window and at or before `frozen_at`, and a scope cell the
population's predicate selects (a match-every-cell predicate selects any,
including no cell). So the auditor reads exactly the evidence they could
sample, no more.

Rejected: admitting every record of a control that has a period
population. It would hand out records outside the window, and cells the
population excludes, which is the "out-of-scope cell gets 403" case.

A record can sit in several periods' populations, so the evidence
attribute is a list (`audit_period_ids`) and a new
`evidence_period_matches` rule intersects it with the assigned list. The
single-valued `audit_period_id` rules for samples and populations are
unchanged.

### D3 — the evidence list: one control, whole-control populations only

`GET /v1/evidence` without `control_id` is a tenant-wide list, so the
hydrator gives it no period and the auditor is refused. With `control_id`,
the handler returns every record of that control and **ignores**
`scope_cell_id` (controldetail `Evidence`), so admitting it on a
cell-scoped population would leak the cells that population excludes. The
list is therefore admitted only through a population with a
match-every-cell predicate. A cell-scoped period's control is read record
by record (D2) or through its samples.

Known widening, accepted: a whole-control population's list is not cut to
the population's time window; the auditor sees that control's evidence
outside the window too. Narrowing the list itself would mean the handler
learning about audit periods, which is a larger change than this fix.
Records outside the window stay refused on the detail read.

### D4 — fail closed

A malformed id, a row absent under the caller's tenant (unknown or
another tenant's), a population attached to no period, a malformed draw
body, or a list without `control_id` leaves the period attribute unset,
and the rules deny. A lookup error is returned to the middleware, which
answers 500 and does not decide. No tenant on the request means no lookup.

### D5 — auditor sample writes include the draw

`auditor.rego` already allowed `write` on `samples` for an assigned period;
`POST /v1/samples` resolves to that resource, so the hydrator gives it the
body population's period. An auditor can therefore draw a sample from an
assigned period's population, as well as annotate one. Creating a
population (`POST /v1/populations`) stays denied: no auditor rule writes
`populations`.

### D6 — `scope_cells.rego`'s evidence rule gets `is_read`

The rule allowed any action on `evidence` once a scope cell matched, so if
`scope_cell_ids` were ever filled it would have admitted
`POST /v1/evidence:push`. It is inert today (D2), but the hydrator now sets
`scope_cell_id` on record reads, so the rule is bounded to reads.
`TestDecide2157_AuditorEvidenceByPeriod` pins it ("push with a matching
scope cell": allowed on the old bundle, denied now).

### D7 — the Playwright spec is replaced, not uncommented

`web/e2e/audit-workspace.spec.ts` was slice 042's contract, every step
commented out against seed data that never existed (an assignment to an
email address, which no principal carries). It now runs the #2157 workflow
against the real platform: admin assigns via the period page (#2118), the
auditor lands on the period in `/audit`, reads the population and in-scope
evidence, draws a sample, is refused the unselected cell, the unassigned
period and the tenant-wide list, and loses them all after removal. Slice
042's other placeholder ACs (walkthrough recorder, comment threads, tab
persistence) were never assertions and are not carried over. The fixture
keeps slice 082's frozen period because `route-sweep.spec.ts` opens it.

## Found while building, not fixed here

- **No JWT can attach a population to a period over HTTP.**
  `POST /v1/audit-periods/{id}/populations/{popID}` checks `canWrite(cred)`
  (`IsAdmin` or a `grc_engineer` owner role). A tenant admin JWT carries
  `OwnerRoles=["admin"]` and `IsAdmin=false` (jwtmw sets `IsAdmin` only for
  super_admin), so the handler refuses it; a `grc_engineer` JWT passes the
  handler, but rego refuses a `grc_engineer` write on `audit-periods`. The
  integration test attaches with the store's own UPDATE and says why.
  Reported on #2157 for a ticket of its own.
