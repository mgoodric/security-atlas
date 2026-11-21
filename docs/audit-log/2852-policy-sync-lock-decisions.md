# 2852 — Policy sync: synced policies locked in the API, badge and drift in the web: decisions log

Issue: mgoodric/security-atlas#2852, under the Confluence policy sync spec
(#2813). Builds on #2850 (`POST /v1/policies:sync`) and #2851 (version
chaining, `sync_drift`).

## Detection-tier classification

- **detection_tier_actual:** integration. The 2850 migration test
  (`TestPolicySyncMigration`) failed once the new tests had written
  `controls_updated` audit rows to the shared database: it cleared only
  `synced` rows before applying its down migration. Fixed in the test (D6).
- **detection_tier_target:** integration.

## Decisions

### D1 — the lock lives in the store, inside each mutation's transaction

`checkNotSynced` (`internal/policy/store.go`) runs in the transaction of
every in-tool mutation, ahead of its own state checks: `Publish`,
`DiscardDraft` and the shared `transition` (submit, approve, draft edit).
`SetNextReviewAt` checks the row it has already read, and `Create` refuses
a synced predecessor in `checkPredecessor`. One error, `ErrManagedBySync`,
answers 409 "managed in Confluence" through one handler helper. Putting it in
the store means no route can reach a synced row around the check, and the
refusal takes precedence over "wrong state". A synced row is published, so
submit on it would otherwise say "not in expected state" and give no hint
of where to edit it.

### D2 — "every draft path" covers a draft of a synced chain

A draft whose predecessor is synced is a next version of a synced policy,
the thing the spec forbids. It can exist only if it was started before its
chain was synced (`TestSync_BumpSkippedWhileInToolSuccessorExists`, and the
coming imported-policy takeover). Edit, submit, approve and publish of such
a draft are refused. Publishing it would supersede the synced row with text
Confluence never held.

### D3 — discarding a draft of a synced chain stays allowed

A draft of a synced chain holds the successor slot the page's next version
needs (#2851 D9: the bump is skipped while it exists). If discard were also
refused, nothing in the product could remove it, and the page could never
publish another version. Discard removes in-tool content and writes nothing
to the synced row, so it cannot make the two systems diverge. The synced row
itself still refuses discard with the managed-in-Confluence 409.

### D4 — control links change in place, on synced policies only

The ticket requires control links on a synced policy to keep working, and
the spec says they "stay editable". No route could change a published
policy's links: `linked_control_ids` is written only by create and draft
edit, and a published policy changes them through a new version, which the
lock forbids here. A synced row starts with no links (#2850) and only
inherits its predecessor's (#2851 D3), so without a new route no control
could ever be linked to a synced policy. The dispatcher confirmed the shape:

- `PATCH /v1/policies/{id}/controls` with `{"linked_control_ids": [...]}`
  replaces the whole set in place, with no new version. The body is capped
  at 1 MiB, as for create and draft edit.
- It accepts only a published synced policy (`UpdateSyncedPolicyLinks`
  matches nothing else). Every other policy gets 409, and the message points
  at a draft or a new version, which is how those policies change links.
  Opening in-place link edits for every published policy would bypass the
  approval that a new version goes through. Synced policies have no such
  approval in the tool: the page's approval is the authority (Q2), and it
  says nothing about links.
- Authorisation is the generic OPA `write` on `policies`, as for draft edit.
  The action is derived from the path (`internal/authz/input.go`), so no
  policy change was needed. A viewer gets 403. The handler also checks
  `authz.IsTenantApprover`, as #2664 made the other policy write handlers
  do, so it is listed in `gateSites2447` and `routes2428`.
- Ids are deduplicated, and each must be a control in the tenant
  (`checkLinkedControls`).
- The route is exempt from the sync lock: it is the one change a synced row
  accepts.

### D5 — a foreign-tenant control is 422, not 400

The dispatcher's brief said 400 for a foreign-tenant control. Create and
draft edit already answer 422 for the same `ErrUnknownLinkedControl` (#2446,
#2459). The id is well-formed, and the request is semantically invalid in
this tenant. The new route answers 422 too, so a client handles the error the
same way on every route that takes links. A malformed UUID is still 400.

### D6 — audit row with the link sets; a no-op writes nothing

Migration `20261284000000_policy_controls_updated` adds the
`controls_updated` action and two nullable `UUID[]` columns,
`previous_linked_control_ids` and `new_linked_control_ids`. Each change
writes one row naming the actor. The 2588 audience columns are the
precedent: a before and after value on the append-only log. Replacing the
links with the same set, in any order and with duplicates, writes nothing,
so a re-save is not an audit event. The down migration refuses while any
such row exists, as the 2588 down migration does.

The 2850 migration test applies that migration's down file in a rolled-back
transaction after deleting `synced` rows. Its action CHECK predates
`controls_updated`, so the test now deletes those rows too. They are written
only on synced policies.

### D7 — the web labels the approval as recorded in Confluence

From the #2850 review (issue comment): a synced policy's approver is
source-asserted text, with `approved_by` empty. The panel says "Approved in
Confluence by <name> on <date>" and never "Approved by" alone. The name
could match a real user, and the line must not read as an approval made in
security-atlas.

### D8 — the web hides actions by provenance, not by status

`primaryAction` returns null for any synced policy, whatever its status, and
the page treats a synced policy as never a draft. Edit, discard, submit,
approve, publish and create-new-version are therefore hidden, and the API
refuses them anyway. The panel (`synced-policy-panel.tsx`) is presentational,
so the node-only vitest tier renders it to static markup, as
`detail-header.test.ts` does. The link editor reuses the policy form's
`EntityMultiPicker` and is shown only on a published synced policy. The BFF
forwards it through the existing `[action]` proxy as `controls` (PATCH
upstream).

## Out of scope

- The imported-policy takeover (spec Q34) and missing-page needs-attention
  items (Q27). Those are separate tickets.
- A Playwright spec. The spec asks for none ("There's no new e2e").
