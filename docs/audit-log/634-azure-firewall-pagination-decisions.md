# 634 — Azure Firewall rule-collection-group cursor pagination: decisions log

Slice type: STANDARD. This file records the build-time engineering calls for the
nextLink cursor-pagination follow-on to slice 614, plus the detection-tier
classification (slice 353 / Q-13). Parent: slice 614 (its decisions-log D5
deferred this work).

## Scope recap

Add ARM `nextLink` cursor pagination to the slice-614 Azure-Firewall reads —
follow the continuation on BOTH the `firewallPolicies` list AND each policy's
`ruleCollectionGroups` list — so a large estate's full rule set is collected, not
just the first bounded page. Keep the per-run cap + add page caps as the DoS
backstop. No new evidence kind, no schema change, no widening of the ARM Reader
scope, no change to the over-collection guard.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** unit. No product-behavior bug surfaced during the
  slice; the only build-time signal was the new test design itself. The
  multi-page walk and the self-pointing-nextLink loop-termination behaviour were
  both exercised at the unit tier (`go test` against a faked ARM surface) before
  push, and `go test -cover` confirmed the package stayed above the 90 floor
  (measured 93.3%). There were no fix-forward defects.
- **detection_tier_target:** unit. A pagination-walk correctness bug (dropping
  later-page records) or a non-terminating-cursor regression is exactly the class
  of issue the per-package unit gate exists to catch — both are covered by
  dedicated unit cases (`...FollowsPolicyListNextLink`, `...FollowsGroupNextLink`,
  `...GroupNextLinkLoopTerminates`). The integration tier is the safety net but
  the cursor logic is fully determinable with a faked HTTP surface, so the unit
  tier is the correct home.

## Decisions made

### D1 — Cursor idiom: follow the server-issued `nextLink` absolute URL verbatim

- **Options:** (a) reconstruct the next page URL from `BaseURL` + a parsed
  skiptoken; (b) follow the server-issued `nextLink` absolute URL verbatim.
- **Chosen:** (b). ARM (like Microsoft Graph) returns `nextLink` as an absolute
  URL carrying an opaque skiptoken that must be requested as-is — reconstructing
  it is fragile and unnecessary. This mirrors the canonical in-repo idiom in
  `connectors/intune/internal/devices/swclient.go` (`@odata.nextLink`, slice 590):
  a `for page := 0; page < maxPages; page++` loop, break when the cursor is empty,
  the page cap as the loop-termination backstop. No sibling **Azure** kind
  implements nextLink yet (grepped `connectors/azure` for `nextLink`/`NextLink`:
  none), so the Intune idiom is the mirror.

### D2 — Two `nextLink` follows: the policy list AND each policy's group list

- The slice spec (AC-1) names both surfaces. `ListFirewallPolicies` now walks the
  `firewallPolicies` list pages following `nextLink`; for each policy,
  `listRuleCollectionGroups` walks the `ruleCollectionGroups` pages following
  `nextLink`. Both walks are GET-only — the slice-614 client test asserting every
  request is a GET stays green, and the nextLink follow-ups are also GET.

### D3 — Cap / loop-termination guard (P0-634-2)

- **Record caps kept UNCHANGED:** `maxRuleCollectionGroupsPerRun` (2000, run-wide)
  and `maxRuleCollectionGroupsPerPolicy` (200, per-policy) are retained verbatim
  as the DoS backstop. Pagination changes only HOW MANY pages are read to reach a
  cap, not the cap value. The walk paginates UP TO the cap, then stops and reports
  what it gathered (cap-hit is not an error — the policy is reported, its list is
  honestly truncated; the connector does not error or mark INCONCLUSIVE on a
  cap-stop).
- **Page caps added:** `maxFirewallPolicyPages` (100) and
  `maxRuleCollectionGroupPages` (100) are the loop-termination backstops. A
  malicious or buggy `nextLink` that points to itself — or a chain of empty pages
  that each carry a recurring cursor, where the record caps would never fire —
  terminates after the page cap rather than looping forever. This is the required
  hard page cap: a self-pointing cursor MUST terminate. The
  `...GroupNextLinkLoopTerminates` test pins it with a self-referential `nextLink`
  and asserts the walk stops at the page cap.
- **Why a page cap in addition to the record cap:** the record cap alone does not
  bound a cursor that returns empty (or all-truncated) pages forever — only the
  page cap guarantees termination independent of how many records each page
  carries.

### D4 — Partial-read honesty on a mid-walk error

- If a later `ruleCollectionGroups` page errors (throttle / decode), the per-policy
  read returns the error string and DISCARDS the groups gathered on earlier pages,
  so the policy is verdicted INCONCLUSIVE (the slice-614 fail-soft contract) rather
  than reported as a complete-but-silently-partial set. One throttled policy still
  does not fail the whole run. This preserves the slice-614 fail-soft semantics
  while being honest that a partial walk is not a complete read.

## Over-collection-guard-unchanged confirmation

This slice adds NO struct field. The slice-614 reflection-based structural
over-collection test (`TestStructs_RuleConfigOnly_NoTrafficSecretOrThreatIntelFields`)
is unchanged and stays green: the record structs still carry rule CONFIGURATION
ONLY — no field capable of holding flow logs, packet captures, traffic contents,
NAT-rule secrets, threat-intel feeds, or route tables. The change is purely to the
read loop (HOW MANY pages), not the record shape (WHAT fields). No evidence kind
was added or changed, so the schemaregistry drift/bijection test
(`internal/control/evidence_kind_drift_test.go`) also stays green. ARM **Reader**
still suffices; the client remains GET-only.

## Neutral-fixture note

The multi-page and loop fixtures use obviously-fake ids (`00000000-...`) and a
fake `$skiptoken` continuation (`00000000-0000-0000-0000-00000000page2`) pointing
back at the 127.0.0.1 httptest server (not a real `management.azure.com`
endpoint). No real tokens / GUIDs / vendor secrets — GitGuardian-neutral.

## Revisit once in use

- The page caps (100/100) bound a policy walk to `100 × server-page-size` records
  before the record cap (200/2000) bites; on a real ARM page size the record cap
  fires first. If an estate legitimately needs more than 2000 rule-collection
  groups per run, that is a cap-tuning conversation (a new slice), not a
  pagination bug — the cap is the deliberate DoS backstop.
