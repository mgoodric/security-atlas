# 2170 — Switch Questionnaires off end to end: decisions log

Issue: mgoodric/security-atlas#2170 (tracer of spec #2166). An admin
switches Questionnaires off and it disappears for everyone in the tenant:
the sidebar, the page and the API, with its data kept.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced. Removing the module
  gate from the chain makes both `TestModuleGate_*` integration tests fail
  (questionnaires and vendor-claims routes answer 200 with no marker).
- **detection_tier_target:** integration.

## Decisions

### D1 — the registry extends the feature-flag store; it is not a second toggle

`featureflag.Modules` lists each optional module with its switch (a
`Seed` key), its API route prefixes and its web pages. A module switch is
an ordinary per-tenant feature flag, stored, audited and toggled through
`PATCH /v1/admin/features/{key}` as before. The registry only says which
routes and pages each switch governs. Questionnaires gets the new key
`questionnaires.enabled` (default on, category `questionnaire`, which needed
the CHECK widened in migration `20261121000000`).

### D2 — Board reporting and Vendor claims keep their flag keys

The spec renames `oscal.export` into a Third-party risk feature later. This
ticket moves the two existing gates into the registry unchanged:
`board_reporting` is switched by `board.reporting`, `vendor_claims` by
`oscal.export`. The route goldens show their middleware depth is
unchanged: the gate moved from a per-route group into the shared chain.

### D3 — route ownership is a segment-bounded prefix

A prefix owns itself and any path that continues at `/` or `:` (the
`{id}:verb` shape). `/v1/questionnaires` owns
`/v1/questionnaires/{id}/answers/{qid}/ai-suggest` but not
`/v1/questionnaires-x`. That covers every route of the three modules here.
Nested routes that belong to another module's data (action plans under a
control path) are a later ticket's problem; the registry can add a pattern
form then.

### D4 — the gate runs before OPA, after tenancy

`featureflag.ModuleGate` is mounted between `tenancymw` and the authz
middleware, so a switched-off module answers 404 with the switched-off
marker (`X-Feature-Disabled: <key>`, `{"error":"feature disabled"}`)
whatever the caller's role, as the spec orders it. The per-request flag
cache moved up with it so the gate and the handlers share one cache.
`featureflag.Gate` stays as a primitive; nothing mounts it now.

### D5 — `GET /v1/me/modules` replaces `GET /v1/features/enabled`

The new endpoint reports each optional module as `{key, name, state,
pages}`, `state` being `on` or `switched_off` (the `no_access` state comes
with module access). Admins and non-admins get the same list for now;
the spec's admin-only features come later. The old endpoint and its BFF
route had no other consumer, so they are removed rather than kept as a
second copy. `/v1/me/modules` is added to the `me` self-read paths in
`defaults.rego`.

### D6 — the web app's page gate fails closed; the nav does not

FeatureGate names its own page (`page="/questionnaires"`) and looks up the
owning module in the `/v1/me/modules` response. If the read fails, no
module is known and the page shows the off state (fail-closed, as before).

The sidebar used to hide the two gated entries when the read failed. With
the page-to-module mapping now coming from the server, a failed read leaves
the sidebar nothing to gate by, so it hides nothing. Rebuilding the old
behaviour would need a web-side list of gated pages, which is what this
ticket removes. The cost is small: a link shown during a platform outage
leads to FeatureGate's off state, never to the module.

### D7 — MCP

No MCP tool reads or writes questionnaires today (`internal/mcp/tools`:
controls, risks, evidence, audit periods). Nothing to refuse yet; a future
questionnaire tool goes through the same API routes, which the gate covers.
