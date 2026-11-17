# 2110 — Evaluate now, and the control page's actions: decisions log

Issue: mgoodric/security-atlas#2110 (parent #2071, finish the record
lifecycles). Adds `POST /v1/controls/{id}/evaluate` and
`GET /v1/controls/{id}/bundle`, and the Evaluate now / Download bundle /
Upload new version actions in the control-detail header.

## Detection-tier classification

- **detection_tier_actual:** unit. The #2447 gate-site scan
  (`TestGateSites2447_TableMatchesSource`) failed on the new
  `IsTenantAdmin` call until the route was added to `routes2428`, with its
  handler-narrower pairs.
- **detection_tier_target:** unit. That is where it should be caught.

## Decisions

### D1 — who may evaluate: the control's owner or an admin (Matt's call)

The ticket says "control owner or admin". OPA disagreed: `evaluate` is not a
transition verb, so the route resolves to `write` on `controls`, which
admits admin and grc_engineer and refuses control_owner. Raised to Matt
through the dispatcher on 2026-11-16. **Matt picked owner or admin.**

- **OPA:** a narrow rule in `control_owner.rego` (in
  `internal/authz/rego_bundle/` and mirrored to `policies/authz/`) admits
  control_owner for `POST /v1/controls/{uuid}/evaluate` only. It is method-
  and path-predicated like the calendar subscription rule, so it grants no
  other controls write (`TestDecide2110_ControlOwnerRuleIsNarrow`).
- **Handler:** admits `authz.IsTenantAdmin(cred)` (the admin flag or a live
  tenant admin role), or the person in `control_owner_assignments` for the
  control. The comparison is `credstore.MinterUserID(cred)`, the bare
  users(id) uuid, so a JWT `user:` session and a user-bound API key both
  match the owner, and an unbound key (no user) never does. A grc_engineer
  or control_owner who is not this control's owner is refused, so both are
  listed as handler-narrower than OPA in `handlerNarrower2428`.
- The owner is read in a tenant-GUC transaction, so RLS keeps another
  tenant's assignment out of reach; a control with no owner admits only an
  admin.

### D2 — the same evaluation path as the scheduler

The handler calls `eval.Engine.EvaluateControl(ctx, id, "manual",
FarFuture)`, the call the ingest subscriber makes with `ingest`. Tenant
transaction, the action-plan reconcile (#2109, inside the eval
transaction), and the `control_evaluations` append are identical; a
reconcile error fails the evaluation as it does elsewhere (#2451 D4). The
response is the `GET /v1/controls/{id}/state` shape read right after.

### D3 — abuse: single-flight per control, 30 s timeout

Concurrent Evaluate now calls for one (tenant, control) share one
evaluation (`golang.org/x/sync/singleflight`, already a direct dependency).
The shared run uses a context detached from any one caller's cancellation
(`context.WithoutCancel`, keeping the tenant and credential values) with a
30 s deadline, so a caller that hangs up does not fail the others; a run
past the deadline is 504. A per-caller rate limit was not added: the
single-flight already bounds the work per control to one evaluation at a
time, and the authz gate bounds who can start one.

### D4 — Download bundle is the stored control.yaml, re-uploadable

`GET /v1/controls/{id}/bundle` returns a gzip-tar with `control.yaml` at its
root, the shape `control.ParseTarball` reads, holding the manifest the store
keeps for that control byte for byte. So a download re-uploads as a no-op,
and an edited download uploads as a new version. It is a read on
`controls` (every role that reads the control page). No secrets: a bundle
declares control text, applicability and evidence queries; credentials live
in connectors, never in a manifest. `description.md` is not stored, so it is
not in the archive. A control seeded straight into the table with no stored
manifest is 404 ("control has no stored bundle"), since an empty
control.yaml would not upload back.

### D5 — Upload new version is the existing upload, no new path

The form posts through a BFF (`/api/controls/upload-bundle`) to the
existing `POST /v1/controls:upload-bundle`, with its tenant-admin gate,
parser, test gate and supersession unchanged. When the upload supersedes
the control, the response's `control_id` is the new version and the page
moves to it.

### D6 — the buttons render for everyone; the platform refuses

Evaluate now and Upload new version are not hidden by role: the control
page does not know the caller's ownership, and the platform is the gate. A
refusal shows inline under the actions. Run query and Edit YAML stay
removed (#2082); no disabled placeholder is rendered.
