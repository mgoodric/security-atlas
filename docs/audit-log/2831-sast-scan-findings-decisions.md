# #2831 — `sast.scan_result` gains findings; cc7_1 detection, pi1_2 and pi1_3 (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
ADR 0028 decision 7 and checks 21, 22 and 23
(`docs/adr/0028-payload-checks-run-in-sql-rego-stays-result-only.md`). The
query shape follows the latest stock SQL checks, cc7_1 remediation (#2829 /
#2956) and cc6_6 MFA on admin apps (#2828 / #2952).

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced. Every bundle case was watched failing against a mutated
query before the final run: making cc7_1's age bound inclusive
(`<= interval '168 hours'`) failed `scan-at-168h-fails` and
`stale-scan-without-findings-fails`, and dropping `authz.idor` from pi1_3's
rule list failed four pi1_3 cases.

There is no SAST connector and none is built (ADR 0028 decision 7), so there
is no source API whose shape the fixtures must match. The schema is the
contract; the fixtures are shaped by it. The old intent is the Rego at
`bf99aaa5`: cc7_1 required, for every in-scope repo, a scan within 7 days
with no finding where `severity == "critical"` and `suppressed != true`;
pi1_2 and pi1_3 required every scan to have no unsuppressed finding whose
`rule_id` is in a fixed set of four.

## D1 — Schema 1.1.0 of `sast.scan_result.v1`, not a new kind

Two optional properties, so 1.1.0 is additive over 1.0.0 (pinned by
`TestSASTScanResult110_AdditiveOver100`). 1.0.0 records stay valid under
their own version; the kind and its category (bound, ADR 0028 decision 9)
are unchanged.

| Field        | Type                                                           | Why                                         |
| ------------ | -------------------------------------------------------------- | ------------------------------------------- |
| `repository` | non-empty string                                               | the key for "latest scan of each repo" (D3) |
| `findings`   | array of `{rule_id, severity, suppressed}`, all three required | what the three checks read                  |

- **`repository` is new beyond the ticket's list.** The old Rego keyed scans
  by `s.repo`, and the pattern takes each entity's latest record. Without a
  key, an old scan with a critical would fail the control after a clean
  rescan. It is optional so 1.1.0 stays additive; a scan without it is left
  out of all three checks, like a keyless record in every other stock SQL
  check.
- **A finding is closed** (`additionalProperties: false`) and carries only
  the three fields the checks read. A pusher wanting paths or messages puts
  the full report at `report_uri`. This keeps a finding near 70 bytes, so
  the 1 MiB inline cap (`ingest.MaxPayloadBytes`) holds roughly ten thousand
  findings. Widening it later is another additive version.
- **`severity` is an enum**: `critical`, `high`, `medium`, `low`, `info`.
  `info` is there because SARIF `note` and most scanners' informational
  level have nowhere else to go.
- **`suppressed` is required.** The old Rego read `suppressed != true`, so a
  missing value meant unsuppressed. Requiring it makes the pusher say so.
  The queries keep the old reading: only JSON `true` suppresses, so a
  tenant-private schema that drops or retypes it counts the finding as
  unsuppressed (`suppressed-must-be-true`).
- **`findings_count` is not cross-checked** against `len(findings)`. It is
  required by 1.0.0 and stays required; a mismatch is not evidence of a
  vulnerability either way.

## D2 — Rule ids are platform ids that the pusher maps onto

pi1_2 and pi1_3 select findings by the eight `rule_id` values the old Rego
named: `injection.sql`, `injection.command`, `deserialization.unsafe`,
`input.schema_violation` and `authz.missing_check`, `authz.idor`,
`authz.privilege_escalation`, `authz.broken_object_level`. Matching is exact
and case-sensitive (`rule-ids-match-exactly`).

No scanner emits these ids; Semgrep, CodeQL and the rest each have their own.
The ticket names `rule id` as the selector, and a mapping step in the CI job
is where the adopter already decides what counts as SQL injection for their
stack. The schema's `rule_id` description and the CLI docs say so. Selecting
by CWE (CWE-89, CWE-78, CWE-502, CWE-20; CWE-862, CWE-639, CWE-269) was
considered: it is scanner-neutral and most SARIF producers carry it. It was
not built, because the ticket and ADR 0028 fix the field as `rule_id`, and
the CWE sets for "input validation" and "authorisation" are themselves a
judgement call. It is an additive follow-up (an optional `cwe` per finding,
OR-ed into the selector) if pushers find the mapping a burden.

The static SQL guard (`schemaQualifiedRef` in `internal/eval/sql.go`) reads
any `word.word`, including inside a string literal, as a schema-qualified
table reference and refuses the query. So each rule id is written as two
concatenated literals (`'injection' || '.sql'`), with a comment saying why.
Teaching the guard to skip string literals would widen the sandbox's
parsing surface in a ticket that is about a schema; it was not done.

## D3 — Query shape (all three)

- **Rows:** a record without a non-empty string `repository` is left out.
  `rank()` over `observed_at` takes each repository's latest scan; a tie
  keeps every tied scan and the worse verdict wins.
- **Scan score, 0/1/2:**
  - `findings` not an array (absent in 1.0.0, or retyped): 1.
  - otherwise the minimum over findings, 2 for an empty array. Each finding:
    not an object, 1; `suppressed` is `true`, 2; then the check's own test.
- **cc7_1 finding test:** `severity` `critical` is 0; `high`, `medium`,
  `low` or `info` is 2; anything else is 1, because an unknown severity may
  be critical.
- **cc7_1 scan age:** the scan must be younger than 168 hours,
  `evaluated_at - observed_at < interval '168 hours'`, else 0, combined with
  the findings score by `least()`. The bound is exclusive ("in the last 7
  days" read as "younger than"), and counted in hours. A stale scan fails
  whatever its findings say (`stale-scan-without-findings-fails`).
- **pi1_2 / pi1_3 finding test:** `rule_id` not a string, 1 (it may be one of
  the four); in the set, 0; otherwise 2.
- **pi1_2 / pi1_3 have no age bound of their own.** The old Rego had none.
  The daily freshness window (7 days) bounds them, and the descriptions say
  so.
- **Combine:** no keyed scan is inconclusive; otherwise the minimum decides.
- **Time:** only `evaluated_at` and `observed_at`. No payload timestamp is
  read, so no RFC 3339 parsing is needed.
- The `findings` array is unnested through
  `jsonb_array_elements(CASE WHEN jsonb_typeof(findings) = 'array' THEN findings ELSE '[]' END)`,
  so a non-array value can never raise an error, whatever order Postgres
  evaluates the outer `CASE` in.

**The 168-hour bound equals the daily window.** `freshnessMaxAgeTable["daily"]` (`internal/eval/state.go`)
is 7 days and includes a record exactly 7 days old, so live the age test
only bites at that one instant. It is still stated in SQL: the bound is the
control's, the window is the engine's, and #2816 asks whether windows are a
deliberate grace period. The bundle cases pin it with an exact
`evaluated_at`; the live-engine test does not try to.

## D4 — One push per control

ADR 0028 decision 9 lists `sast.scan_result.v1` as a bound kind: a record is
stored against the one control its `control_id` names. Three stock controls
now read the same scan, so a pusher pushes it three times, once per control,
each with its own idempotency key. The CLI docs show the loop.

Making the kind routed (stored with `control_id` NULL, reaching every
control whose queries declare it, as `jira.ticket_evidence.v1` and
`osquery.host_posture.v1` are) would let one push serve all three. That
changes a ratified ADR table and the routing cutover's freeze semantics, so
it is not done here. It is the obvious follow-up and is raised on the PR.

## D5 — Supersedes three stock bundles

`soc2_cc7_1_vulnerability_detection` (query
`sast_run_recently_no_unsuppressed_criticals`),
`soc2_pi1_2_input_completeness` (query `input_validation_taint_rules_clean`)
and `soc2_pi1_3_processing_validity` (query
`authorization_taint_rules_clean`) change query language (Rego to SQL),
expression and description, so all three manifest hashes change and all
three bundles are superseded on the next boot. Owners, evidence, exceptions
and risk links stay on the old versions. `stock_manifests_gen.go` gained the
three new hashes. The generator also dropped two shipped hashes
(`soc2_cc1_4_competence_training` `1a157af7…`,
`soc2_cc7_1_vulnerability_remediation` `76366391…`), the bug #2963 fixes;
both were restored by hand, so the file's diff only adds lines.

Upgrade note (also in `changelog.d/2831.changed.md`): push scans at schema
1.1.0 with `repository` and `findings`, once per control. Until then,
1.0.0 records carry no findings and all three checks are inconclusive. The
behaviour change an upgrader sees first: all three passed on a pusher's
`--result pass`, and now ignore it.

## Not changed

- ADR 0028's check table is ratified and not edited.
- Every in-scope repo scanned is still #2834; only received scans are
  checked.
- No connector, CLI flag or SARIF converter is added. A SARIF-to-1.1.0
  converter would be a reasonable `atlas-cli` follow-up; it would have to
  carry D2's rule-id mapping as configuration.
