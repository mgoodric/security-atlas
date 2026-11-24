# #3031: grc_engineer files MCP write proposals; admin decides them (decisions log)

Decided by Matt on #3022 (2026-11-24): `grc_engineer` files MCP write
proposals, and only a tenant admin decides them. This log does not
block merge.

- detection_tier_actual: none
- detection_tier_target: none

## D1: How the bundle tells create apart from confirm and reject

`POST /v1/mcp/write-proposals`, `.../{id}/confirm` and `.../{id}/reject` all
derive action `write` on resource `mcp`. `confirm` and `reject` are not
transition segments (`internal/authz/input.go` `transitionActions`), and
`resourceFromPath` folds sub-paths into the first segment.

**Chose: a path predicate in `grc_engineer.rego`** that admits `write` on
`mcp` only for `POST /v1/mcp/write-proposals`. The bundle already splits a
resource this way twice: `risk_hard_delete` and the `metrics/{id}/target`
admit in the same file.

**Rejected: adding `confirm` / `reject` to `transitionActions`.** That would
change the derived action for every route that ends in those segments,
including `/v1/mapping-overlays/remap-proposals/{id}/reject` and others.
It would widen the change well past `mcp`.

## D2: Confirm and reject stay admin-only, narrower than "approver or admin"

The ticket describes the handler gate as `!cred.IsApprover && !cred.IsAdmin`.
On `main` it is `authz.IsTenantAdmin(cred)`
(`internal/api/mcpwriteproposals/handlers.go:215`, `:249`) and has been
since #2428. An approver-flag credential derives `grc_engineer` in OPA
(`internal/authz/input.go` `derivedRolesFor`), and no rego attribute
exposes the flag. Admitting approver-flag keys to confirm would need a new
attribute and would widen access, so neither layer does. Confirm and reject
are exactly as before: a tenant admin at both the engine and the handler.

For a `grc_engineer` the engine refuses confirm and reject first, with its
own `{"error":"forbidden"}`. The handler gate stays as the second layer.
`TestDecideProposal_403ForGRCEngineerAndApproverFlag` pins that it refuses
a live `grc_engineer` and an approver flag on its own.

**Accepted side effect: more credentials can file proposals.** The create
admit is on the `grc_engineer` role, so every credential that derives it
can file an MCP write proposal, not only a user granted `grc_engineer`.
Two derive it without a role grant: an approver-flag credential
(`internal/authz/input.go:153-154`) and a tenant key with no special flags,
which falls to the default case (`internal/authz/input.go:196-202`).
Matt accepted this on 2026-11-24. Deciding stays admin-only, so none of
these credentials can confirm or reject what they file.

## D3: Self-confirmation stays allowed

Nothing refuses it: `Store.ConfirmAs`
(`internal/mcp/writeproposals/store.go:398`) never compares `created_by`
with the confirmer. That is the design, so this ticket does not add a
check:

- In the slice-173 decisions log (D1, threat model T.2 and E.2,
  `docs/audit-log/173-mcp-write-tools-decisions.md`), the proposer is the
  AI. The human who holds the bearer is the confirmer. The AI files under
  the operator's own credential, and the operator confirms through the
  `confirm_write` MCP tool (`internal/mcp/tools/writes.go:453`) with the
  same bearer. A check that refused self-confirmation would block that
  path.
- `created_by` is a credential id, not a person
  (`internal/mcp/writeproposals/appliers.go:163-168`). For a browser it is
  a per-session `jwt:` id. Comparing it with the confirmer would not
  separate two people.
- After this change a `grc_engineer` cannot confirm at all, so it can never
  confirm its own proposal. Only an admin can file a proposal and then
  confirm it.

A person-level separation of proposer and confirmer would need
`created_by` to record a user id. Matt was offered that as an option; it
is not in this ticket's scope.
