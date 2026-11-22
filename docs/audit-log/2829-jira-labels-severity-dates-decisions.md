# #2829 — Jira emits labels, severity and dates; cc1_4 training tickets and cc7_1 remediation SLA (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
ADR 0028 checks 18 and 19 (`docs/adr/0028-payload-checks-run-in-sql-rego-stays-result-only.md`).
The query shape follows the latest stock SQL checks, cc6_6 MFA (#2828 /
#2952) and cc8_1 (#2827 / #2946).

- detection_tier_actual: manual_review
- detection_tier_target: integration

One bug surfaced while building, found by re-reading the query before its
first run: cc1_4's `done` was `payload->'resolution' = '"Done"'`, which is
NULL for an open ticket, so an open ticket fell through to "resolved Done,
date unreadable" and was inconclusive instead of failing. It is now
`coalesce(…, false)`, and the bundle case `open-ticket-only-fails` pins it.
A first draft of cc7_1 had the same shape of bug (an unreadable
`created_at` scored 0 through `least()` rather than 1) and was restructured
before it ran.

Jira's shapes come from Jira Cloud's REST v3 OpenAPI description
(`developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json`, read
2026-11-21): the search envelope, the user's `accountId`, the `priority`
object (`name`, `id`), and the timestamp format of `created`,
`2022-10-06T07:32:47.000+0000`. Issue `fields` is an untyped map there, so
`labels` (string array), `resolutiondate` (same format as `created`) and a
select custom field's `{value}` object are Jira's documented system and
custom-field shapes but have no example in the spec. Nothing was checked
against a live site. The old intent is the Rego at `bf99aaa5`: cc1_4 matched
`"security-training" in t.labels`, `t.resolution == "Done"` and a
`resolution_date` within 365 days per user; cc7_1 had SLA days 7 / 30 / 90
per `severity`, open measured from `created_at` to now, closed from
`created_at` to `resolved_at`.

## D1 — Schema 1.1.0 of `jira.ticket_evidence.v1`, not a new kind

Six optional fields, so 1.1.0 is additive over 1.0.0 (`CheckAdditiveOver`,
pinned by `TestJiraTicketEvidence110_AdditiveOver100`). The kind, its
routing (#2924; it is a routed kind read by cc1_4, cc7_1, p5_1 and p6_3)
and the 1.0.0 rows in the ledger are unchanged. The connector pushes 1.1.0
for Jira and Linear; Linear fills none of the new fields.

| Field                 | Type                                      | Source                              |
| --------------------- | ----------------------------------------- | ----------------------------------- |
| `assignee_account_id` | non-empty string                          | `fields.assignee.accountId`         |
| `labels`              | array of non-empty strings                | `fields.labels`                     |
| `priority`            | non-empty string                          | `fields.priority.name`              |
| `severity`            | `critical` \| `high` \| `medium` \| `low` | `--severity-field`, normalised (D2) |
| `created_at`          | RFC 3339 UTC                              | `fields.created`                    |
| `resolved_at`         | RFC 3339 UTC                              | `fields.resolutiondate`             |

Each is omitted when Jira has no value, never sent as a default.
`assignee_account_id` is new beyond the ticket's list: training is checked
per person, and a display name is neither unique nor stable. The personal
data inventory's `jira.ticket_evidence` row already covers assignees.

Jira's timestamps are not RFC 3339 (the offset has no colon), so the
connector converts them to UTC RFC 3339 and drops one it cannot parse. The
queries therefore read only RFC 3339; an un-normalised value is unreadable
(`jira-offset-without-colon-is-inconclusive`).

## D2 — Severity is read from one operator-named field and normalised

Jira has no system severity field; teams use priority or a custom field.

- `--severity-field` names the field: `priority` by default, or a custom
  field id. A string, a `{value}` option or a `{name}` object is read.
- The value maps case-insensitively: `critical`, `high`, `medium`, `low`;
  plus `Highest` → critical and `Lowest` → low, the two names in Jira's
  default priority scheme that are not already severities. The older scheme
  in the spec's examples (Blocker, Critical, Major, Minor, Trivial) is not
  mapped beyond `Critical`: Major or Minor meaning high or medium is a guess
  the connector should not make. Any other value leaves `severity` out, and
  `priority` keeps the raw name.
- A mapping flag (`--severity-map Major=high,…`) was considered and not
  built. It is a one-flag follow-up if an operator's scheme needs it.

## D3 — Which tickets each check reads: a label

`jira.ticket_evidence.v1` is routed by kind, so every control querying it
reads every Jira ticket the connector pushes: change tickets, training
tickets and vulnerability tickets alike. Each check therefore selects its
tickets by label on the ticket's latest record:

- cc1_4: `security-training`, the label the old Rego named.
- cc7_1: `vulnerability`. The old Rego took `input.vuln_tickets` and named
  no selector, so this one is new. A label is the selector the operator
  controls in Jira and that works on every project type; an issue type or a
  project key would need a flag per check.

A ticket whose latest record drops the label leaves the check
(`label-removed-in-latest-record-leaves-scope`).

## D4 — cc7_1 query

- **Rows:** a record without a non-empty string `ticket_key` is left out.
  `rank()` over `observed_at` takes each ticket's latest record; a tie keeps
  every tied record and the worse verdict wins.
- **Scope:** labelled `vulnerability`, and `severity` is not `low`. Low has
  no SLA in the ticket, so it is outside the check.
- **SLA:** hours, not days (#2824 D2): 168, 720, 2,160.
- **Met, scored 0/1/2:**
  - 1 (unreadable) when `created_at` is unreadable; when `resolved_at` is
    present but unreadable or before `created_at`; or when `resolution` is
    set but `resolved_at` is absent.
  - otherwise a resolved ticket (one with `resolved_at`) is 2 when
    `resolved_at ≤ created_at + SLA`, else 0;
  - an open ticket is 2 when `evaluated_at ≤ created_at + SLA`, else 0.
- **Verdict:** `critical`/`high`/`medium` take that score. No `severity` is
  `greatest(score at 168 h, 1)`: it may be critical, so it passes only
  within the strictest SLA and otherwise cannot fail. A mistyped `severity`
  (a tenant-private schema could widen the enum) is 0.
- **Combine:** no in-scope ticket is inconclusive; otherwise the minimum
  decides.
- **Boundaries are inclusive for both.** The ticket says an open ticket
  "must be younger than its SLA". An open ticket at exactly its SLA has not
  missed it yet, and would pass if resolved at that instant, so failing it
  would be stricter than the resolved rule. Each boundary is pinned at the
  instant and one second past it.
- **Resolution kind is not read.** Won't Do counts as resolved at its
  `resolved_at`. Accepting a risk is an exception's job, not a resolution
  name's, and names vary by workflow.
- **Time:** only `evaluated_at` and the payload timestamps. Each timestamp
  passes the anchored RFC 3339 regex and `pg_input_is_valid`, and one more
  than an hour after `evaluated_at` is unreadable.
- **Window:** weekly, 30 days of `observed_at`. A ticket the connector
  stops reporting (a JQL that excludes closed tickets, say) drops out after
  that. The description says so.

## D5 — cc1_4 training-ticket query

- **Rows:** keyed by `ticket_key` as in D4, then kept only when the latest
  record is labelled `security-training` and names an
  `assignee_account_id`. An unassigned training ticket cannot be attributed
  to a person, so it is left out rather than failing.
- **Ticket score:** 2 when `resolution` is `Done` and `resolved_at` is
  within 8,760 hours before `evaluated_at`; 1 when Done but `resolved_at` is
  absent or unreadable; otherwise 0 (open, Won't Do, or Done too long ago).
  A tie on one ticket takes the minimum.
- **Per person:** the maximum over the person's tickets. A person needs one
  current ticket, so last year's resolved ticket and this year's open one
  together pass while the old one is inside 365 days, and fail once it is
  not. This is the old Rego's `some t in input.training_tix`.
- **Combine:** no kept ticket is inconclusive; otherwise the minimum over
  people decides.
- **An open training ticket alone fails.** A new hire whose first ticket is
  still open has not been trained; CC1.4 asks for training on hire. If
  Matt wants a grace period for new tickets, it is one arm: score an open
  ticket younger than N hours as 1.
- **Resolution, not status.** The old Rego and the ticket both say
  "resolved Done"; `status` names vary by workflow and `resolution` is what
  Jira sets on resolving.
- **Window:** annual, 400 days of `observed_at`, as for completions.
- The `security_awareness.training_completion.v1` query is unchanged, and
  the engine still combines the two (any fail fails, otherwise any pass
  passes). Every active user holding one is still #2834.

## D6 — Supersedes both stock bundles

`soc2_cc7_1_vulnerability_remediation` (query `vuln_tickets_within_sla`)
and `soc2_cc1_4_competence_training` (query
`training_completion_within_365d`) change query language (Rego to SQL),
expression and description, so both manifest hashes change and both
bundles are superseded on the next boot. Owners, evidence, exceptions and
risk links stay on the old versions. `stock_manifests_gen.go` was
regenerated.

Upgrade note (also in `changelog.d/2829.changed.md`): upgrade the Jira
connector with the platform, label vulnerability tickets `vulnerability`
and training tickets `security-training`, and set `--severity-field` when
severity is a custom field. Until then, 1.0.0 records carry no labels and
both Jira checks are inconclusive. The behaviour change an upgrader sees
first: cc7_1 used to pass on any closed ticket in the window, and on 1.0.0
records it is now inconclusive.

## Not changed

- ADR 0028's check table is ratified and not edited.
- The connector still calls `GET /rest/api/3/search`, which Atlassian is
  replacing with `/search/jql`; that is outside this ticket.
- Linear does not emit the new fields.
