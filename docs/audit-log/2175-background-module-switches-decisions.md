# 2175 — Background work, MCP proposals and board packs: decisions log

Issue: mgoodric/security-atlas#2175 (spec #2166, ADR 0025). Follows #2346
(module switches), #2454 (module access), #2476 and #2486 (`moduleaccess.Views`),
#2519 (`UserHas`; no live role means no access) and #2510 (MCP `create_risk`).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building this.
- **detection_tier_target:** integration. Each acceptance check is an
  integration test against real Postgres under RLS (see "Acceptance
  checks" below).

## Decisions

### D1 — AC 1 reinterpreted: there is no vendor-review or policy reminder job

AC 1 names a "vendor-review reminder job" and "policy reminders". Neither
exists on `main` (e4e5dd0). Nothing writes a vendor notification, and
`policy_ack_due` and `risk_review_overdue` appear only as labels in the
digest, email and preference code; no code ever creates one. The dispatcher
confirmed the reading below on Matt's behalf and is noting the missing
producers on #2524. No new vendor-review job is built here.

AC 1 is met by gating the senders that do exist:

- The decision-overdue notifier follows Risk and its Decision log feature.
- The personnel overdue-offboarding notifier follows Personnel security.
- The notification digest (email, Slack, webhook) drops the counts of kinds
  whose module is switched off or outside the recipient's access.

Tests:

- **Policies:** a recipient narrowed out of Policies gets no `policy_ack_due`
  count in their digest.
- **Third-party risk:** a regression guard checks that, with the module off,
  `Views.Reaches` answers no for everyone, an admin included. A future vendor
  reminder job has to ask that question.

### D2 — notification kind to owning module

`notify.kindModule` holds this table. A test holds every value to a module
or feature key in the registry.

| Notification type                        | Producer                            | Owning module (key)                                          | Gated where         |
| ---------------------------------------- | ----------------------------------- | ------------------------------------------------------------ | ------------------- |
| `decision.overdue`                       | `decision.Notifier`                 | Risk › Decision log (`decision_log`)                         | Producer and digest |
| `personnel_security.offboarding_overdue` | `personnelsecurity.OverdueNotifier` | Personnel security (`personnel_security`)                    | Producer and digest |
| `policy_ack_due`                         | none today                          | Policies › Policy acknowledgments (`policy_acknowledgments`) | Digest              |
| `risk_review_overdue`                    | none today                          | Risk (`risk`)                                                | Digest              |
| `audit_note.reply`                       | `audit/notifications`               | Audit periods (core)                                         | Never               |
| `audit_period_assignment`                | none today                          | Audit periods (core)                                         | Never               |
| `control.drift`                          | none today                          | Controls (core)                                              | Never               |
| `evidence.staleness`                     | `staleness` rollup                  | Evidence (core)                                              | Never               |
| `access_review_due`                      | `accessreview`                      | no module route (core)                                       | Never               |
| `security_training.overdue`              | `securityawareness`                 | no module route (core)                                       | Never               |
| `backup.failure`                         | `backup`                            | Settings & admin (core)                                      | Never               |

Access reviews and security-awareness training have no HTTP route, so the
registry does not place them in an optional module. They stay core until a
route places them.

### D3 — AC 2 reinterpreted: no core path runs risk aggregation

The aggregation engine (`aggrule.EvaluateOnRiskWrite`) runs only inside a
`risk.Store` write. Those writes come in through `/v1/risks*`, which the
module gate already refuses while Risk is off, and through MCP proposals,
which D5 holds pending. The one core path that changes a risk is evidence
ingest: `ResidualSubscriber` recomputes `residual_score`, and nothing on it
reads a module switch. Roll-ups (`LiveAggregation`, `ListChildren`) are
computed from current children each time they are read.

The AC 2 test therefore checks two things:

- With Risk off, the residual recompute that evidence ingest runs still
  updates the linked risk.
- After Risk is switched back on, the risk shows that residual, and the
  parent's live roll-up is computed from its current children.

No upkeep code changed. The test guards against a later change gating upkeep
on the switch.

### D4 — background jobs: switch read and access read fail differently

Background jobs have no credential. They use `moduleaccess.Views.Reaches`, a
new method combining:

- `SwitchedOn`: the tenant's module switch, plus the feature switch when the
  key is a feature;
- `UserHas`: the recipient's live roles and stored access.

`SwitchedOn` reads switches through `featureflag.Enabled`. When the flag store
fails, that falls back to the switch's seed default, the same as for every
request; the flag store swallows DB errors by design, so a job cannot tell a
failed read from a stored value. An access read that fails is an error: the
notifiers log it and skip that recipient, and the digest sends nothing for
that user. Neither ever falls back to sending to everyone.

A skipped notification writes no dedup marker:

- the decision notifier writes no `overdue_notified` row;
- the personnel notifier writes no notification row.

The skipped notification is therefore sent once the module is switched back
on or the recipient's access is widened. The digest only filters counts, so
the stored notifications are kept and are counted again in the same way.

### D5 — MCP proposals: refused at confirm, before the transaction

`Store.ConfirmAs` reads the proposal, then checks its tool's module through
`Switches.SwitchedOn`. Every Risk tool maps to Risk: `create_risk`,
`update_risk_treatment`, `link_child_risks` and `unlink_child_risk`.
`update_control_state` and `push_evidence` are core.

The check runs before the row-locking transaction, so the switch read never
waits on a held connection. A refusal returns `ErrModuleOff`, which the
confirm route answers with 409 `module_switched_off`, and leaves the row
`ai_proposed` and unapproved. The tiny window in which the switch flips
between the check and the apply is accepted: the applier then writes what
an admin had just confirmed.

`ConfirmAs` is the only apply path. There is no auto-apply, and the MCP
`confirm_write` tool calls the same HTTP route.

Creating a proposal and rejecting one are not gated. The ticket asks only
that applying waits, and a proposal filed while the module is off is still
reviewable once it is switched back on.

### D6 — board packs: omitted sections are recorded in the pack

A pack generated with a module off leaves out that module's sections:

- `vendor_burndown` follows Third-party risk.
- `top_risks` follows Risk. It is the other section built from an optional
  module's data, and the spec's "switched off means gone" covers it.

The left-out keys are stored in the pack content as `omitted_sections`. That
separates "left out on purpose" from an incomplete older pack, which the web
page still shows as "not generated" and which still blocks publishing. The
pack's sections, the narrative, the PDF, the publish gate and the web page
all walk `Pack.Keys()` / `packSectionKeys()`, the fixed keys minus the
omitted ones. Editing an omitted section answers 404 (`ErrUnknownSection`).

Packs generated earlier are stored JSON and are never regenerated, so they
keep every section they had.

## Acceptance checks

| AC  | Test                                                                                                                                                                                                                                                                          |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | `internal/notify/scheduler/modules_integration_test.go`: `TestSweepOnce_DigestDropsPolicyRemindersWithoutAccess` and `TestThirdPartyRiskOff_ReachesNoOne`. Also `internal/decision/modules_integration_test.go` and `internal/personnelsecurity/modules_integration_test.go`. |
| 2   | `internal/risk/modules_integration_test.go`: `TestRiskOff_EvidenceStillRecomputesResidualAndRollUpIsCurrent`                                                                                                                                                                  |
| 3   | `internal/mcp/writeproposals/modules_integration_test.go`: `TestConfirm_CreateRiskWaitsForRiskSwitch`                                                                                                                                                                         |
| 4   | `internal/board/modules_integration_test.go`: `TestPackGenerator_VendorBurndownFollowsSwitchAtGeneration`                                                                                                                                                                     |
