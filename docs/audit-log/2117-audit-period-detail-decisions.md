# 2117 — Audit-period detail page, with freeze: decisions log

Issue: mgoodric/security-atlas#2117 (parent #2071). `/audits/[id]` shows one
audit period: its header (name, framework, dates, status), the per-control
horizon read, Freeze behind an inline confirm, and the OSCAL export on a
frozen period. Web only; no platform change.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced.
- **detection_tier_target:** none.

## Decisions

### D1 — the control list is the tenant's controls, not a period-scoped set

The platform has no period-scoped control list (the audit workspace's shell
records the same gap). The page lists the tenant's active controls
(`/api/controls-list`) and reads each one's horizon through
`GET /v1/audit-periods/{id}/control-state`, 25 rows at a time. It does not
filter by the period's framework: the period carries a framework version id,
and matching that to a control's framework list on the client would be a
guess.

### D2 — populations, samples and notes come from the workspace component

There is no list of populations or samples per period; both hang off a
control. Opening a control row mounts the audit workspace's
`ControlWorkspace` for that control in this period, so the frozen evidence
summary, sampling, walkthroughs and audit notes reuse the routes and UI the
workspace already has, rather than a second copy on this page.

### D3 — Freeze is offered on every open period; the platform decides

OPA and the handler disagree on whether `grc_engineer` may freeze (#2291,
undecided). The page does not encode either answer: it offers Freeze on an
open period and shows the platform's 403 as a toast. The confirm says the
period's evidence becomes immutable and that the freeze cannot be undone.

### D4 — OSCAL export fetches, then saves

The platform mounts the export route only when the OSCAL bridge is wired.
A native `<a download>` would save chi's bare 404 as a file when the bridge
is absent. The detail page fetches the bundle and saves it through a blob
link, so a bare 404 (or a 502/503) reads as "the OSCAL bridge isn't running"
instead. The period's own 404 (`audit period not found`) and a 409 (not
frozen) stay refusals with the platform's message. The `oscal.export` flag
gates the vendor-claims module, not this route, so the page adds no flag
check (FeatureGate, #2283, owns flag gating).

### D5 — e2e mocks the BFF

Freezing is irreversible, so freezing the shared seeded period would change
it for every other spec. The detail spec mocks the BFF routes on the browser
context, as `oscal-export-e2e.spec.ts` does; the freeze itself is covered by
the Go tier (`internal/audit/period`). The route sweep visits the real seeded
period.
