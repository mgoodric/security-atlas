# ADR 0028: Payload Checks Run in SQL; Rego Stays Result-Only

**Status:** Accepted

**Date:** 2026-11-20

**Decides:** how stock and tenant control bundles check fields inside evidence payloads, what each evidence-query language receives, and which of the 27 stock SOC 2 checks reduced to a roll-up in #1858 can come back, and when.

**Ticket:** [#2814](https://github.com/mgoodric/security-atlas/issues/2814), under [#1860](https://github.com/mgoodric/security-atlas/issues/1860). Decisions settled in Matt's grill of 2026-11-20 (Q5–Q10, Q31, Q32); facts read at `origin/main` `24dfe7dd` and re-checked at `bc320ea1`.

**Numbering note:** `0003` is used twice in this directory (`0003-audit-period-freeze-hash-inputs.md` and `0003-oauth-authorization-server.md`). This ADR is 0028. Whoever writes the next ADR should list `docs/adr/` on current `main`, and check open PRs, before choosing a number.

---

## Context

A control bundle's `evidence_queries[]` can be written in three languages. Each one receives the in-window evidence records for one (control, scope cell), but each sees a different part of a record:

| Language  | Evaluator                   | What the expression sees                                                                                                                                                                                                                                                                  |
| --------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| JSON-path | `internal/eval/jsonpath.go` | One record's payload at a time, in process. A truthy match passes the record.                                                                                                                                                                                                             |
| SQL       | `internal/eval/sql.go`      | A single relation, `evidence(result, observed_at, payload)` (plus `evaluated_at` since the d1 amendment), built from the records as a JSONB parameter. It runs in a read-only subtransaction with `search_path` set to `''` and a 5 s `statement_timeout`, behind a static DDL/DML guard. |
| Rego      | `internal/eval/rego.go`     | `{records: [{result, observed_at}]}`, under a capability set with network and runtime builtins removed. In practice only `result` is set (see decision 3).                                                                                                                                |

Before #1858 (`3f0ae1fb`, fixing #1728), the 27 Rego queries in 26 `controls/soc2/` bundles were written as `allow` rules over payload-shaped input: disk below 85%, a phishing-resistant factor, revocation within 24 h, and so on. None of them could run. Rego never receives the payload, the field names didn't match the registered schemas, and the policies didn't assign `result`. #1858 replaced each one with a roll-up of the connector's per-record verdict (`docs/audit-log/1728-stock-control-rego-decisions.md`). The intent survives only in descriptions and in history at `bf99aaa5`, the parent of that commit.

#1860 asks how those checks should come back. The choice is between widening Rego's input to include payloads, moving the checks to SQL or JSON-path, or having connectors encode them in each record's `result`.

Three further facts shape the answer:

- **The engine ignores a query's `evidence_kind`.** Every query on a control receives all of that control's in-window records, whatever their kind. This is harmless while Rego sees only `result`. It is wrong as soon as a query reads payload fields, because a query written for one kind misreads another. cc1_4 already has two queries over two kinds.
- **A query sees only the records its control receives.** No query can see a reference set of users, endpoints, on-call staff, in-scope systems or repos. The SQL sandbox forbids naming any table except `evidence`.
- **Some evidence kinds have no producer.** `sast.scan_result.v1` is registered and read by stock checks, but no connector emits it.

## Decision

1. **Payload checks are written in SQL. JSON-path stays for single-field checks.** The SQL evaluator already receives each record's payload inside the strictest sandbox we have: no base tables, read-only, empty `search_path`, a statement timeout, and a static guard. A payload check is a predicate over several fields, often with a window on `observed_at` or an aggregate across records ("every host reported in 24 h"), and SQL expresses all three. JSON-path stays for a check that tests one field per record. Its truthy-match semantics can't state "A and B, or C" across fields, or anything across records. Nothing about either sandbox is widened.

   **Amendment ([#2911](https://github.com/mgoodric/security-atlas/issues/2911), Matt, 2026-11-21):** the SQL sandbox gains exactly one thing, the evaluation instant, as a constant `evaluated_at` column on `evidence`. A windowed check ("every host reported within 24 h", cc4_1) compared against `now()` reads the wall clock, so a replay, an audit-period re-evaluation (invariant 10) and a bundle test with pinned dates each get a different verdict from the one first given. `evaluated_at` is the engine's evaluation time, or the as-of horizon when that is earlier, and the case's pinned `evaluated_at` in a bundle test. Like `observed_at` into Rego (decision 3), it carries no tenant content. Rego gets the same instant as `time.now_ns()`, through `rego.EvalTime` ([#2890](https://github.com/mgoodric/security-atlas/issues/2890)), so the two languages share one evaluation-time source. Stock SQL checks must not call `now()`, `current_timestamp`, `clock_timestamp()` or their relatives; a test refuses them.

2. **Rego stays result-only. Its sandbox contract is not widened.** Rego is a roll-up over per-record results: precedence, quorum, "N of M passed". Giving it payloads would change the slice 054 sandbox contract in `rego.go` (which fields, size limits, PII in policy input) to get a capability SQL already has under tighter limits. One language holds payload access, so there is one place to audit what a control reads.

   **Rejected: connectors decide per control** (option (c) in #1860). A connector emits one `result` per record, and that one verdict serves every control on that evidence kind. Those controls have different intents. osquery's host-posture verdict is disk encryption plus screen lock, yet cc4_1 (reporting freshness), cc6_6 (host firewall) and a1_1 (capacity) all read that kind. A connector can't produce a verdict for each control, and it shouldn't hold control logic that a control's own bundle should state and version.

3. **Rego's `observed_at` is populated, as documented.** The package doc and the control-bundle spec say Rego's input is `{records: [{result, observed_at, …}]}`. `regoInputRecord` has an `ObservedAt` field, but `evalRegoQuery` builds each element as `regoInputRecord{Result: r.result}` (`internal/eval/rego.go:124`), so policies see the zero time. **Choice: populate it** from the record's `observed_at`, rather than correcting the docs to drop it. It isn't payload, it carries no tenant content beyond a timestamp, and the engine already holds it. A roll-up that weighs recency ("the latest record passed") needs it. `hash` is **not** added: the package doc stops claiming it. This is implemented in [#2819](https://github.com/mgoodric/security-atlas/issues/2819), which must also show that no payload field reaches Rego.

4. **Checks evaluate only the records the control receives.** A query decides over what was pushed to its control, in its window. Completeness against an inventory ("every active user", "every managed endpoint", "every in-scope repo") is out of scope here. It needs reference sets that are versioned for point-in-time replay (invariant 2) and audit-period freezing (invariant 10), and a way to hand them to a query without breaking the SQL sandbox's no-other-tables rule. That is a design of its own: [#2834](https://github.com/mgoodric/security-atlas/issues/2834). Until it lands, these checks lose their "every X" half and check only the records received:

   | Check                                     | "Every X" half deferred to #2834                          |
   | ----------------------------------------- | --------------------------------------------------------- |
   | cc1_4 training tickets / training records | every active user has a current completion                |
   | cc1_5 accountability acknowledgment       | every active user acknowledged the core policies          |
   | cc5_3 policies and procedures             | every active user acknowledged the version in force       |
   | cc4_1 continuous monitoring               | every managed endpoint reported                           |
   | cc6_3 periodic access review              | every in-scope system was reviewed                        |
   | cc6_3 privileged account management       | privileged-vault members within the privileged role count |
   | cc7_1 vulnerability detection             | every in-scope repo was scanned                           |
   | cc7_4 incident response                   | every on-call person acknowledged the IR plan             |

5. **Each query sees only records of its declared `evidence_kind`.** The engine filters the control's in-window records to the kind in the query's manifest before it evaluates that query. A query that declares no kind keeps today's behaviour (all of the control's records), and the bundle validator warns on it. This comes before any SQL payload check that shares a control with another kind. Implemented in [#2818](https://github.com/mgoodric/security-atlas/issues/2818).

6. **A stock control's description states what runs.** A description that claims a check the engine doesn't run tells an auditor something false. The project's honesty bar is that the tool is diligenced itself. Every stock description says what evaluates today: for a roll-up, the connector's verdict and what that verdict means for the kind. Each intended check that is pending is named, with the field or source it waits on. A description-only edit updates the bundle in place (#1908). Implemented in [#2820](https://github.com/mgoodric/security-atlas/issues/2820), and kept true by every check ticket below as its query lands.

7. **A kind with no producer gets a schema, not a connector.** `sast.scan_result.v1` gains a findings list (severity, rule id, suppressed) in a new schema version. The schema is the contract, and any pusher can satisfy it: `atlas-cli evidence push`, a CI job, or the adopter's own scanner. We don't build a SAST connector, because the adopter's scanner already knows its findings and a bundled connector would have to pick a scanner (invariant 3: connectors are peers emitting through `Push`). Implemented in [#2831](https://github.com/mgoodric/security-atlas/issues/2831).

8. **A schema change is a new schema version.** Every tier-2 ticket below adds fields to a kind whose schema sets `additionalProperties: false`. Older records stay valid under their own version, and a query that reads a new field reads only records of the version that carries it.

9. **Connector evidence is routed to controls by evidence type** ([#2924](https://github.com/mgoodric/security-atlas/issues/2924), Matt, 2026-11-21). A connector observes a system, not a control: one `github.repo_protection.v1` record answers both cc8_1 bundles, and one `osquery.host_posture.v1` record answers cc6_6 and cc4_1. Before this, a connector bound each record to one control through a `--*-control` flag. That flag defaulted to an SCF anchor (`scf:TDA-06`, `scf:END-03`), which no control resolves, so on default flags every stock SQL check got no records and stayed inconclusive. Each evidence kind is now in one of three categories:

   | Category       | Kinds                                                                | Stored as                                                                         | Reached by                                                 | Who may write it                                     |
   | -------------- | -------------------------------------------------------------------- | --------------------------------------------------------------------------------- | ---------------------------------------------------------- | ---------------------------------------------------- |
   | Bound          | every other kind (`manual.upload.v1`, `access_review.completion.v1`) | `control_id` of the named control                                                 | that control only                                          | any push credential scoped to the kind               |
   | Routed by type | `internal/evidence/routed`                                           | `control_id` NULL; any wire `control_id` kept in `control_ref` as provenance only | every control in the tenant whose queries declare the kind | any push credential scoped to the kind               |
   | Owned          | `internal/evidence/unbound` (`policy.acknowledgment.v1`)             | `control_id` NULL                                                                 | every control in the tenant whose queries declare the kind | its platform handler only; a push is refused (#2873) |

   Every ledger reader applies one control-free rule, `control_id IS NULL AND evidence_kind = ANY(<owned ∪ routed>) AND <the control's evidence_queries declare the kind>`, inside the tenant's RLS context and with `tenant_id` in the query too. Decision 5's per-query kind filter and the freshness window apply unchanged. An audit period frozen, or a population created or frozen, before the routing cutover (recorded by the migration in `evidence_routing_cutover`) binds only the owned kinds, so its reads and re-draws are exactly what they were before routing existed (invariant 10). A routed kind may be pushed with an empty `control_id`, and the connector flags for routed kinds default to empty. `TestStockBundleKindsAreCategorized` fails when a stock bundle reads a kind that is in no category. Decisions: `docs/audit-log/2924-evidence-routed-kinds-decisions.md`.

   Amended by [#2973](https://github.com/mgoodric/security-atlas/issues/2973): `sast.scan_result.v1` moved from bound to routed, so one scan reaches cc7_1 detection, pi1_2 and pi1_3. A kind that was bound, with records already in the ledger, before it was routed gets its own cutover row in `evidence_routing_kind_cutover`, and a pinned read compares against that instant. A period frozen after the first cutover but before the kind was routed keeps reading none of its control-free records. Decisions: `docs/audit-log/2973-sql-guard-literals-sast-routed-decisions.md`.

   Amended by [#3019](https://github.com/mgoodric/security-atlas/issues/3019): every routed kind is in exactly one class. It was routed at the first cutover (#2924), or it is in `lateKinds` with its own cutover row, or it is in `firstRecordKinds`: added after the first cutover but routed from its first record, so no bound record of it predates routing, with a one-line justification. `TestEveryRoutedKindIsClassified` fails on an unclassified kind, so a kind cannot fall back to the first cutover by omission. A row in `evidence_routing_kind_cutover` for a kind not in `lateKinds` makes `routed.Cutover` fail rather than be ignored.

## The 27 checks, by tier

The 27 Rego queries reduced to a roll-up in #1858 are in 26 bundles; cc1_4 has two. The old expressions are at `bf99aaa5`. #2820's decisions log lists each one with its permalink.

- **Tier 1:** the fields are already emitted, so the check moves to SQL now.
- **Tier 2:** a connector or schema must emit fields first.
- **Needs a design:** depends on a source or a join that doesn't exist yet.

A check whose fields are split is listed once, under the tier of what lands first, with the rest noted.

### Tier 1: fields already emitted (13)

| #   | Check                               | Evidence kind                               | SQL check over received records                                                    | Ticket                                                                                                                                                                              |
| --- | ----------------------------------- | ------------------------------------------- | ---------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | cc6_1 logical access baseline       | `okta.mfa_policy.v1`                        | an MFA-required policy applies to everyone and allows a phishing-resistant factor  | [#2821](https://github.com/mgoodric/security-atlas/issues/2821) (tracer, sets the pattern)                                                                                          |
| 2   | cc6_6 boundary protection           | `osquery.host_posture.v1`                   | every host-posture record has the host firewall enabled                            | [#2822](https://github.com/mgoodric/security-atlas/issues/2822)                                                                                                                     |
| 3   | cc4_1 continuous monitoring         | `osquery.host_posture.v1`                   | every host seen has reported in the last 24 h (inventory half: #2834)              | [#2822](https://github.com/mgoodric/security-atlas/issues/2822)                                                                                                                     |
| 4   | cc8_1 change management             | `github.repo_protection.v1`                 | at least one approving review, protection enforced on admins (rest: tier 2, #2827) | [#2823](https://github.com/mgoodric/security-atlas/issues/2823)                                                                                                                     |
| 5   | cc8_1 configuration change control  | `github.repo_protection.v1`                 | at least one approving review (`iac`-topic scoping: tier 2, #2827)                 | [#2823](https://github.com/mgoodric/security-atlas/issues/2823)                                                                                                                     |
| 6   | cc6_3 periodic access review        | `access_review.completion.v1`               | a completed review within 90 days (inventory half: #2834)                          | [#2824](https://github.com/mgoodric/security-atlas/issues/2824)                                                                                                                     |
| 7   | cc1_4 training completion records   | `security_awareness.training_completion.v1` | completions within 365 days from an allowed source (inventory half: #2834)         | [#2824](https://github.com/mgoodric/security-atlas/issues/2824) (needs #2818)                                                                                                       |
| 8   | cc6_5 asset disposal                | `manual.upload.v1`                          | a disposal log uploaded within 90 days                                             | [#2824](https://github.com/mgoodric/security-atlas/issues/2824)                                                                                                                     |
| 9   | cc9_1 business continuity           | `manual.upload.v1`                          | a continuity plan uploaded within 365 days                                         | [#2824](https://github.com/mgoodric/security-atlas/issues/2824)                                                                                                                     |
| 10  | cc6_7 encryption                    | `aws.s3.bucket_encryption_state.v1`         | default SSE with an allowed algorithm (AES256, aws:kms)                            | [#2825](https://github.com/mgoodric/security-atlas/issues/2825) (blocked by [#2815](https://github.com/mgoodric/security-atlas/issues/2815): connector keys don't match the schema) |
| 11  | cc1_5 accountability acknowledgment | `policy.acknowledgment.v1`                  | acknowledgements cover the five core policies (inventory half: #2834)              | [#2832](https://github.com/mgoodric/security-atlas/issues/2832) (blocked by [#2817](https://github.com/mgoodric/security-atlas/issues/2817): the records never reach the control)   |
| 12  | cc5_3 policies and procedures       | `policy.acknowledgment.v1`                  | acknowledgements reference the policy version in force (inventory half: #2834)     | [#2832](https://github.com/mgoodric/security-atlas/issues/2832) (blocked by #2817)                                                                                                  |
| 13  | cc7_4 incident response             | `policy.acknowledgment.v1`                  | the IR plan has been acknowledged (inventory half: #2834)                          | [#2832](https://github.com/mgoodric/security-atlas/issues/2832) (blocked by #2817)                                                                                                  |

The windowed checks (6–9) are tighter than the engine's freshness windows, which are 120 days for quarterly and 400 for annual. The SQL window is the effective bound. Whether those freshness windows are a deliberate grace period is [#2816](https://github.com/mgoodric/security-atlas/issues/2816).

### Tier 2: a connector or schema must emit fields first (10)

| #   | Check                               | Fields needed                                                                                     | Ticket                                                          |
| --- | ----------------------------------- | ------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| 14  | a1_1 capacity monitoring            | osquery: disk, memory and CPU p95 utilisation, host role                                          | [#2826](https://github.com/mgoodric/security-atlas/issues/2826) |
| 15  | cc6_8 malware protection            | osquery: antimalware active, definitions age                                                      | [#2826](https://github.com/mgoodric/security-atlas/issues/2826) |
| 16  | cc7_2 anomaly detection             | osquery: EDR installed, running, healthy                                                          | [#2826](https://github.com/mgoodric/security-atlas/issues/2826) |
| 17  | cc6_6 MFA on admin apps             | Okta app assignment: app classification, sign-on policy requires MFA                              | [#2828](https://github.com/mgoodric/security-atlas/issues/2828) |
| 18  | cc1_4 training tickets              | Jira: labels, resolution status and date (inventory half: #2834)                                  | [#2829](https://github.com/mgoodric/security-atlas/issues/2829) |
| 19  | cc7_1 vulnerability remediation     | Jira: severity, created and resolved dates, for the SLA by severity                               | [#2829](https://github.com/mgoodric/security-atlas/issues/2829) |
| 20  | cc6_3 privileged account management | 1Password: privileged-vault sharing default, audit events, member count (role-count bound: #2834) | [#2830](https://github.com/mgoodric/security-atlas/issues/2830) |
| 21  | cc7_1 vulnerability detection       | `sast.scan_result`: findings with severity, rule, suppressed (in-scope-repo half: #2834)          | [#2831](https://github.com/mgoodric/security-atlas/issues/2831) |
| 22  | pi1_2 input completeness            | `sast.scan_result`: findings for the input-validation rules                                       | [#2831](https://github.com/mgoodric/security-atlas/issues/2831) |
| 23  | pi1_3 processing validity           | `sast.scan_result`: findings for the authorisation rules                                          | [#2831](https://github.com/mgoodric/security-atlas/issues/2831) |

The remaining halves of checks 4 and 5 are also tier 2: required status checks, force-push setting, topics and deploys-to-prod, in [#2827](https://github.com/mgoodric/security-atlas/issues/2827).

### Needs a design (4)

| #   | Check                                  | What is missing                                                                                           | Ticket                                                                            |
| --- | -------------------------------------- | --------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| 24  | cc6_2 new user provisioning            | **HR source:** hire records to authorise each provisioned account                                         | [#2833](https://github.com/mgoodric/security-atlas/issues/2833)                   |
| 25  | cc6_3 access modification / revocation | **HR source:** termination records to time deactivation within 24 h                                       | [#2833](https://github.com/mgoodric/security-atlas/issues/2833)                   |
| 26  | cc3_4 change risk reassessment         | **PR-review join:** each protected-branch push in `github.audit_event.v1` joined to a non-author approval | [#2834](https://github.com/mgoodric/security-atlas/issues/2834) or its own design |
| 27  | cc7_3 security event evaluation        | **Event acknowledgements:** an acknowledgement within 24 h for each security-tagged audit event           | [#2834](https://github.com/mgoodric/security-atlas/issues/2834) or its own design |

The **inventory** design ([#2834](https://github.com/mgoodric/security-atlas/issues/2834)) covers no check on its own. It is the deferred "every X" half of the eight checks in decision 4.

Until a check's query lands, its description says it is pending (decision 6).

## Alternatives considered

| Alternative                                                       | Why rejected                                                                                                                                                                                                                                                         |
| ----------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Give Rego payload access                                      | It widens the slice 054 sandbox contract (field selection, size limits, PII in policy input) for a capability SQL already has under tighter limits: no tables, read-only, timeout. Two languages with payload access means two places to audit what a control reads. |
| (c) Connectors encode each control's check in the record `result` | One record's `result` serves every control on its kind, and those controls want different checks. Control logic would live in connector releases, not in the versioned bundle an auditor reads.                                                                      |
| JSON-path for every payload check                                 | It evaluates one record at a time, with truthy-match semantics. It can't express multi-field predicates, windows across records, or "every host seen reported in 24 h".                                                                                              |
| Correct the docs instead of populating Rego `observed_at`         | The field is declared and documented, the value is in hand, and it isn't payload. Dropping it would remove the one non-result signal a roll-up can use, and save nothing.                                                                                            |
| Pass an inventory snapshot to queries now                         | Reference sets need their own source, versioning for replay and freezing, and a sandbox shape. Bolting that onto this decision would ship completeness checks with no point-in-time story. Deferred to #2834.                                                        |
| Build a SAST connector to produce `sast.scan_result`              | It would have to pick a scanner, and adopters already have one. A schema any pusher can satisfy is the smaller contract and matches invariant 3.                                                                                                                     |

## Consequences

- SQL is the one language that reads payload fields, so a reviewer looks for payload access in one place. A new payload check copies the cc6_1 tracer's pattern (#2821): query shape, bundle fixtures, an evaluation test, and a description update.
- Rego policies keep their current input, plus a real `observed_at`. A Rego policy that relied on the zero time would change behaviour. No stock policy does: every one is a roll-up over `result`.
- Per-query `evidence_kind` filtering (#2818) has to land before any control mixes kinds across payload queries. A query with no declared kind keeps today's behaviour, with a validator warning.
- Thirteen checks can come back now, ten need a connector or schema change first, and four wait on a design. Eight of the 27 stay received-records-only until the inventory design lands, and their descriptions say so.
- Tenant-authored bundles get the same rule: payload checks in SQL, roll-ups in Rego, single-field tests in JSON-path.
