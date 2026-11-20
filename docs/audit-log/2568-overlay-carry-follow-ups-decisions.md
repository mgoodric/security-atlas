# 2568 — SCF overlay carry follow-ups: decisions log

Issue: mgoodric/security-atlas#2568, from the review of #2562 (#2554, the
carry-forward of tenant overlay edges on SCF import). Three follow-ups: guard
the role switch, isolate one tenant's failure, and record that `kept_legacy`
is final.

## Detection-tier classification

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

No bug reached production. Review found that no test failed if the role
switch was removed, and that one tenant's error ended every later tenant's
carry. Both are now covered by integration tests.

## D1 — Check the role inside the transaction, not only switch it

`carryOverlaysInTenant` runs `SET LOCAL ROLE atlas_app`, then reads
`current_user` and refuses to carry unless it is `atlas_app`. If the switch is
removed or has no effect, the carry would otherwise run as the migrate role,
which has BYPASSRLS. That carry could read and write every tenant's rows.

A check in the function was chosen over a test hook alone. It holds in
production, not only under test, and it fails the tenant before any read.
`TestOverlayCarry_RunsAsAtlasAppUnderEachTenant` also observes the role and
`app.current_tenant` from inside each tenant's transaction through a test hook
(`carryTenantHook`, nil in production).

Mutation check: with the `SET LOCAL ROLE` replaced by `SELECT 1`, six overlay
tests fail, each reporting `running as "atlas_migrate", not atlas_app`.

## D2 — A failed tenant is reported, and the other tenants still carry

Before this change, any error other than `ErrRemapEdgeChanged` (which
`CarryRemap` already treats as a skip) in one tenant ended the loop. Every
later tenant then went uncarried until a re-run. Now the loop records the
tenant and the error in `OverlayRemapReport.Failed` and continues.
`QueueOverlayRemapProposals` returns an error only when queueing fails.

The CLI prints `failed=<n>` on the summary line, then one line per failed
tenant with its error. It exits non-zero. The import itself succeeded, but
the operator has to re-run it to carry the failed tenants. A zero exit would
hide that in automation, where nobody reads the log.

Each tenant's carry is still one transaction (#2554 D2), so a failed tenant
rolls back as a whole and its proposals stay open. The re-run carries them.
`TestOverlayCarry_OneTenantsFailureDoesNotStopTheRest` fails one tenant
through the hook, checks that the other is carried and the failed one stays
open, then clears the fault and checks that a re-run carries it.

## D3 — `kept_legacy` stays final; no reopen

#2554 D5 closes a same-code proposal as `kept_legacy` when its target pair
already exists. If the blocking overlay or catalog edge is later retired, the
proposal stays closed. `kept_legacy` is final for a person's decision too
(`TestOverlayRemapDecide_RejectIsRevisitableKeepLegacyIsFinal`), and the
import's closure uses the same decide path.

The tenant's way back is to create the overlay edge on the current anchor
directly (`POST /v1/mapping-overlays`). Once the blocking edge is retired,
that is the same pair the proposal would have made.

**Why not let an import-closed proposal be reopened.** A reopen would need a
new transition out of a final status, kept to proposals whose decider is
`system:scf-import`. It would also need its own audit row, an API verb, and
UI. The case is narrow: a same-code edge whose exact target pair the tenant
already held, and whose blocker the tenant later retired. The direct create
covers it in one call. If tenants run into it, a reopen can be added as a
claim then.
