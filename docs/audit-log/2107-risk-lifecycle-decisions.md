# Risk open/closed lifecycle — decisions log (#2107)

A risk is open or closed. `PATCH /v1/risks/{id}/close` takes a reason
(`not_applicable | merged | realised_and_handled`) and an optional note.
`PATCH /v1/risks/{id}/reopen` returns it to open. Each writes one
`risk_audit_log` row in its transaction (`closed`, `reopened`). Closed
risks are left out of the register and everything that counts live
exposure. Hard delete is admin-only. Parent: #2071 (finish the record
lifecycles).

detection_tier_actual: none
detection_tier_target: none

No bug surfaced. Five existing tests asserted contracts this ticket
changes on purpose, and were updated in the same PR: the export column
order (two unit, two integration), the `GET /v1/risks` contract golden,
and the route-table goldens.

## Decisions

**D1: Status is its own column, not a treatment value.** `risks.status`
is a new `risk_status` enum (`open | closed`, default `open`), beside
`treatment`. A closed risk keeps its treatment, so reopening restores the
risk as it was. Closure fields (`closure_reason`, `closure_note`,
`closed_at`, `closed_by`) live on the risk and are cleared on reopen.
The audit row keeps them. The CHECK `risks_closure_consistent` enforces
both directions. An open risk carries no closure. A closed one carries a
reason, a time and a closer.

**D2: Close and reopen take the rule locks, unlike the #2105 transitions.**
The dispatcher asked for the #2105 `transition()` shape, which locks only
the risk row because treatment is not an aggregation input. Status is an
aggregation input, because a closed risk is not a rule candidate (D4). So
close and reopen follow `Delete`'s order (#1867, #2335): rule advisory
locks for the risk's themes, then the row lock, the state check, the write
and its audit row, then the re-evaluation of the rules the risk feeds. The
state checks are server-side. Close works only from open, reopen only from
closed, and anything else is a 409. The reason enum is checked before any
lock (400).

**D3: What "excluded by default" covers.** Every read that counts live
exposure leaves closed risks out:

| Read                                                                   | Change                                                                                                                                                      |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/risks` (register, its `count`, dashboard top risks)           | `ListFilter.IncludeClosed`, off by default; `?include_closed=true` turns it on. The dashboard's top-risks feed reads this list, so it inherits the default. |
| `HeatmapBuckets`                                                       | `status = 'open'`.                                                                                                                                          |
| `RiskCountsByOrgUnit`, `FairExposureByOrgUnit`, `RiskThemeOrgUnitGrid` | `status = 'open'`: org-unit placement counts and the theme heatmap.                                                                                         |
| `ListRisksForBoardBrief` (board brief and board pack top risks)        | `status = 'open' OR closed_at > as_of`: a risk closed after the brief's date was open as of that date, so a re-generated historical brief still shows it.   |
| `ListCandidateRisksForRule`                                            | `status = 'open'` (D4).                                                                                                                                     |
| Calendar risk-review events                                            | `status = 'open'`: a closed risk is not due for review.                                                                                                     |

**Roll-ups count open children only.** The register (#2060) and the board
brief hide a parent that has children linked, because the children already
stand for its exposure. Once a parent's children are all closed, none of
them is listed, so hiding the parent too would make an open risk vanish
from the register, its count, the dashboard and the board top risks.
`ListRiskChildCountsForTenant` therefore counts only open children. The
brief's roll-up test counts children open as of its date
(`c.status = 'open' OR c.closed_at > as_of`). Closing a parent's only child
lists the parent; reopening the child hides it again
(`TestLifecycle_ParentOfClosedChildrenIsNotARollUp`). The review found this.

**Historical briefs read closure from current state.** The schema keeps no
status history, only the current `status` and `closed_at`, so a risk closed
before a brief's date and reopened since reads as open as of that date. A
brief generated on its own date freezes what it showed (canvas §7.5), so
this matters only when a brief is generated later for an earlier date. A
dated status history would fix it, and #2107 does not need one.

Left as they are:

- **Usage counts:** the category and theme usage counts that guard their
  deletion. A closed risk still references its category and themes.
- **Detail reads:** `GET /v1/risks/{id}`, parents and children, and the
  risks linked to a control. A closed risk is still a record.
- **Export:** it carries every risk, with two appended columns, `status`
  and `closure_reason`. An export is a data dump, not the register's view.

**D4: Closed risks do not count toward aggregation rules.** A closed risk
is not live exposure, so it is not a candidate. Closing a child runs the
re-evaluation in the same transaction, so the meta-risk drops the child's
link and score at once, as a delete does (#1861). Reopening it brings the
child back. The engine never closes or reopens a meta-risk. Canvas §6.6
says closing a child never auto-closes the parent, and this keeps that.
Test: `TestEngine_CloseAndReopenRefreshMetaRisk_2107`. Closing a
rule-generated meta-risk does not re-evaluate its own rule, as `Delete`
does not. Otherwise the rule would immediately rewrite it.

**D5: Hard delete is admin-only, which removes grc_engineer's delete.**
`DELETE /v1/risks/{id}` derived `write` on `risks`, so grc_engineer, and
any credential with the approver flag, could hard-delete a risk. The ticket
and #2071 make it admin-only, and the dispatcher confirmed the narrowing
(it is Matt's spec). There are two gates:

- **OPA:** the grc_engineer `risks` write admit excludes `DELETE` on
  exactly `/v1/risks/{id}` (`risk_hard_delete` in
  `policies/authz/grc_engineer.rego` and its embedded copy). Deletes under
  a risk (`/children/{id}`, `/themes/{t}`) and of categories have more
  path segments and are unchanged.
- **Handler:** `DeleteRisk` requires the admin flag or the tenant `admin`
  role (`isTenantAdmin`). It is the testable twin, since test servers run
  without OPA.

**D6: The delete's audit row goes in `risk_audit_log`.** The dispatcher
suggested the unified admin audit log so the row survives a cascade.
There is no cascade. `risk_audit_log` has no foreign key to `risks`, and
its trigger refuses UPDATE and DELETE, so a `deleted` row written in the
delete's transaction outlives the risk. #2367 brings `risk_audit_log`
into the unified log, so the row appears there with no second write path.
The row's `before` keeps the title, category, methodology, treatment and
status.

**D7: Close and reopen are risk-writer actions.** Their paths end in
`close` and `reopen`, which are not promoted actions, so they derive
`write` on `risks` (admin and grc_engineer). The handler adds no approver
gate. Closing is the routine way to retire a risk, not an approval.

A closed risk refuses treatment and acceptance transitions with 409,
"reopen it first". The check is in `lockOpenRisk`, the lock step every
#2105 transition shares, so the REST routes and the MCP
`update_risk_treatment` applier refuse alike. A retired risk's treatment
and acceptance stand as they were until it is reopened. The page also hides
those actions while the risk is closed. The review asked for this; the
first cut left the API open
(`TestLifecycle_ClosedRiskRefusesTreatmentAndAcceptance`).

The closure note is capped at 4000 characters, the close form's
`maxLength`. The store checks it (400) and so does the
`risks_closure_note_length` CHECK.

**D8: UI.** These follow the #2102 detail-page pattern:

- **Close:** "Close risk…" in the overflow opens `/risks/[id]/close`, a
  form route with the reason radios and a note. It returns to the risk
  with a toast.
- **A closed risk:** it shows a Closed badge and its closure, and leads
  with Reopen, a one-click transition confirmed inline
  (`ConfirmInline`). The acceptance and treatment actions are hidden.
- **Register:** it gains "Show closed" (`?closed=1`), beside "Show
  roll-ups", and marks a shown closed risk "Closed".
- **Labels:** the closure reasons are labelled in the shared enum labels
  (`RiskClosureReason`, `RiskStatus`).
