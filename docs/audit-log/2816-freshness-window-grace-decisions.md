# Issue #2816 — freshness windows (quarterly 120d, annual 400d) vs the stock checks' 90/365d

Build-time calls made for
[#2816](https://github.com/mgoodric/security-atlas/issues/2816), a child of
[#1860](https://github.com/mgoodric/security-atlas/issues/1860). The engine
treats `quarterly` as 120 days and `annual` as 400 days. The stock SOC 2
checks that #1858 replaced asked for 90 days (access review, asset-disposal
log) and 365 days (training, continuity plan), and their descriptions still
said so.

`detection_tier_actual = manual_review` (the #1858 review read the old
expressions against the engine table) · `detection_tier_target = unit` (a
stock-bundle test could compare a description's stated window with its
class's window; not built here, see D4).

## Where 120 and 400 came from

| Site                                            | Commit                                                           | What it says                                                                                                                             |
| ----------------------------------------------- | ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `Plans/canvas/02-primitives.md` §2.3 table      | `6f4932ac` (2026-05-10, initial canvas), split out in `614455f2` | `quarterly 120 d`, `annual 400 d`, with no stated rationale. Unchanged since.                                                            |
| `internal/eval/state.go` `freshnessMaxAgeTable` | `2a07bdca` (2026-05-13, slice 012)                               | Copies the canvas table verbatim and cites it ("the canvas §2.3 freshness model").                                                       |
| `internal/db/queries/calendar.sql`              | `6c9c9ab0` (2026-05-16, slice 094)                               | Replicates the same mapping in SQL for due dates.                                                                                        |
| `internal/policy/acknowledgment.go`             | `456d9e38` (2026-05-13, slice 023)                               | Deliberately uses 365 d for the user-facing ack window and says the canvas's 400 d "governs EvidenceRecord.valid_until" and is distinct. |

No commit message, ADR, slice doc or canvas sentence says why the numbers are
wider than the cadence. Searched: `git log -S` on both constants, `Plans/`
and `docs/adr/` for "grace" and "13 months". So the history does not show the
grace was _argued_ for. The table's shape shows it was _intended_: every class
is its cadence plus slack, never the bare cadence (daily 7 d, weekly 30 d,
monthly 90 d, quarterly 90 + 30 d, annual 365 + 35 d). A 400-day annual window
is about 13 months, the usual tolerance an auditor applies to an annual
activity (a pen test or BCP review done a few weeks late in the next cycle is
still in period). That last point is general audit practice, not something
the repo cites.

## D1 — Keep the windows as grace; do not narrow them to the cadence

**Decision.** Keep `quarterly = 120 d` and `annual = 400 d`. Document them as
cadence-plus-grace where control authors read (control-bundle spec §2.2.1,
canvas §2.3) and make the stock descriptions state the effective window.

**Why.**

- The table is the ratified canvas model, consistent across all six classes,
  and two other code sites (calendar SQL, the ack note) were built against it.
  Narrowing two classes would leave `daily`/`weekly`/`monthly` with grace and
  `quarterly`/`annual` without, which is less coherent, not more.
- Narrowing is a tenant-visible behaviour change in the strict direction:
  every quarterly or annual control with evidence aged 91-120 or 366-400 days
  would flip from fresh to stale on upgrade, with no change on the tenant's
  side. That should be a product decision with a migration note, not a
  side-effect of reconciling descriptions.
- The old 90/365 numbers lived inside Rego expressions that could never run
  (#1858, #1728): they were the intent of one author per bundle, never
  enforced behaviour. The engine table is what tenants have always got.

**Rejected: align to 90/365.** Would need a test per cadence and a
behaviour-change changelog entry (the ticket's "if aligned" branch). Not taken
for the reasons above. If Matt wants strict windows, the change is one table in
`internal/eval/state.go` plus the mirrored `CASE` in `calendar.sql`.

## D2 — Stock descriptions name the effective window and the grace

Query and control descriptions that named the nominal window now name the
effective one, e.g. "within 120 days (quarterly plus 30-day grace)". Changed:

| Bundle                              | Class     | Was   | Now   |
| ----------------------------------- | --------- | ----- | ----- |
| `soc2_cc6_3_periodic_access_review` | quarterly | 90 d  | 120 d |
| `soc2_cc6_5_asset_disposal`         | quarterly | 90 d  | 120 d |
| `soc2_p4_2_retention_schedule`      | quarterly | 90 d  | 120 d |
| `soc2_p6_2_disclosure_log`          | quarterly | 90 d  | 120 d |
| `soc2_p6_4_vendor_privacy_terms`    | quarterly | 90 d  | 120 d |
| `soc2_cc9_1_business_continuity`    | annual    | 365 d | 400 d |
| `soc2_p1_1_privacy_notice`          | annual    | 365 d | 400 d |
| `soc2_p8_1_privacy_monitoring`      | annual    | 365 d | 400 d |
| `soc2_cc1_4_competence_training`    | annual\*  | 365 d | 400 d |

Query `id`s (`every_in_scope_system_reviewed_in_90d`,
`training_completion_within_365d`, ...) are left as they are: an id is a key,
and renaming it is a separate change with its own blast radius.

Not changed: `soc2_cc9_2_vendor_risk_assessment`'s "within the last 12
months" (the age of the vendor's report, a payload property, not evidence
freshness) and `soc2_cc7_1_vulnerability_remediation`'s 30/90-day SLAs
(remediation deadlines, not freshness).

## D3 — `soc2_cc1_4_competence_training` moves from `monthly` to `annual`

**This is a behaviour change.** The training control declared
`freshness_class: monthly` (90 d) since the SOC 2 kit landed (`1192b16b`),
while its title, description and both queries ask for annual training within
365 days. Its `security_awareness.training_completion.v1` records carry
`observed_at` = the completion date (`internal/securityawareness/store.go`),
so under `monthly` a completion 91 days old went stale and the control fell to
`no_evidence`/`inconclusive` for nine months of every training year. Stating
"effective window 90 days" in an annual-training description would document
the bug rather than fix it, so the class is corrected to `annual` (400 d).
Recorded in `changelog.d/2816.changed.md` as a tenant-visible change: training
evidence now stays fresh for 400 days instead of 90.

## D4 — No new window test

The ticket asks for a per-cadence pinning test only on the "aligned" branch.
`internal/eval/state_test.go` already pins every class's duration (including
`quarterly 120d` and `annual 400d`), so the kept values are already guarded. A
test that parses stock descriptions for a stated day count and compares it to
the class was considered and not built: it would key on free text.
