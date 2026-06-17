# 658 — Azure Event-Grid provision / deprovision subcommand — decisions log

JUDGMENT slice. The write-scope-boundary decision was made by the maintainer
(2026-06-15); Claude made the implementation-mechanism calls below (credential
separation mechanism, ARM client shape, diagnostic-setting in-or-out,
idempotency / teardown semantics, RBAC action list) and recorded them here. The
slice ships when CI is green (no human sign-off gate). This is a connector-side
privilege-boundary call, not a cross-cutting architecture decision, so it gets a
decisions log, not an ADR.

- detection_tier_actual: none (no bug surfaced during the slice)
- detection_tier_target: unit

No defect surfaced during the build. The provisioning logic + the ARM write
client are exercised entirely at the Go unit tier (the `provision` package's
fake-API ordering/idempotency tests + the httptest ARM-client body/idempotency
tests + the cmd seam tests). This tier is the right target: the connector cannot
reach live Azure in CI, so the injectable seam + httptest is the authoritative
oracle, exactly as the six existing read kinds are tested.

## The boundary (maintainer decision, implemented exactly)

**D0 — Separate, opt-in, one-shot `provision` / `deprovision` subcommand run with
the operator's OWN elevated credential.** The steady-state `eventgrid` receiver
stays READ-ONLY and never holds a write scope (P0-658-1). The `provision`
subcommand is the ONLY write code path in the connector. Provisioning talks to
Azure's ARM management API only; it does not touch the security-atlas platform
and does not widen the platform push wire (invariant #3 / P0-658-2). No new
evidence kind.

## Implementation-mechanism decisions (Claude)

**D1 — Credential separation is real, not cosmetic.** The provision subcommands
resolve their elevated credential from DEDICATED env vars
(`AZURE_PROVISION_TENANT_ID` / `AZURE_PROVISION_CLIENT_ID` /
`AZURE_PROVISION_CLIENT_SECRET`), resolved EXPLICITLY in `resolveProvisionCredential`
rather than via the shared `azureauth.Resolve` env fallback — so a receiver
credential sitting in `AZURE_*` can NEVER be silently picked up by `provision`.
A unit test (`TestDoProvision_RequiresElevatedCredential`) sets ONLY the
receiver's `AZURE_*` and asserts `provision` refuses with no ARM call issued.
A second test (`TestReceiverNeverConstructsWriteAPI`) fails loudly if the
receiver path ever constructs the write client or acquires an elevated token.

**D2 — Raw-HTTP ARM client behind an injectable interface, NOT the
armeventgrid/armmonitor management SDK.** The slice brief suggested the Azure
ARM management SDK. I chose to mirror the connector's six EXISTING read kinds
(`internal/storage`, `internal/aks`, `internal/nsg`, `internal/keyvault`,
`internal/firewall`, `internal/entra`), every one of which is a thin raw-HTTP
ARM client behind an injectable `API` interface — the connector currently has
ZERO `azure-sdk-for-go` dependency. Reasons: (a) consistency with the
established connector pattern reviewers already know; (b) Article VIII
(anti-abstraction / trust-the-framework, but more precisely: don't add a heavy
dependency the rest of the package doesn't use) — pulling in two new SDK module
trees + their transitive deps to issue four PUTs and three DELETEs is not worth
it; (c) it keeps the one PRIVILEGED write surface small and auditable
line-by-line (the entire ARM write surface is ~120 lines in
`internal/provision/client.go`), which matters more for a write path than for a
read path; (d) no go.mod / go.sum churn and no phantom-dep audit risk. Trade-off
accepted: I hand-pin the ARM API versions (`2022-06-15` for EventGrid,
`2021-05-01-preview` for Insights diagnostic settings) and hand-shape the
request bodies, which the SDK would otherwise type for me. Revisit if a future
provisioning surface needs polling long-running-operation handles or rich typed
responses, at which point the SDK earns its weight.

**D3 — Diagnostic-setting provisioning is IN this slice (opt-in within the
opt-in).** The AC names the Activity-Log diagnostic setting, and routing
Activity-Log events is the whole point of the subscription-level system topic, so
omitting it would leave the feature half-useful. It is gated behind
`--with-diagnostic` so the operator who only wants resource-change events (and
who may not want to grant `Microsoft.Insights/diagnosticSettings/write`) is not
forced into the broader grant.

**D4 — Idempotency via ARM PUT-is-upsert; teardown via DELETE-404-is-no-op.**
`provision` issues PUTs (ARM PUT is create-or-update), so re-running an
already-provisioned plan succeeds without a pre-existence GET. `deprovision`
issues DELETEs and treats HTTP 404 as success, so a partial teardown re-runs
safely. This keeps both verbs idempotent without a read-modify-write round-trip
(and the receiver, the read side, is the only thing that ever reads).

**D5 — Order: provision topic→subscription→diagnostic; deprovision reverse.** The
subscription and the diagnostic setting both target the system topic, so the
topic is created first and torn down last. `Plan.Validate()` runs before any ARM
call so a typo never leaves a half-provisioned state.

**D6 — Delivery key carried as a secret static delivery-attribute.** The event
subscription writes the receiver's delivery key into the webhook destination's
`deliveryAttributeMappings` as a `Static` attribute with `isSecret: true`, named
per the receiver's `--credential-in` (header name or query-param name). The key
is read from `AZURE_EVENTGRID_DELIVERY_KEY` (same var the receiver verifies),
never a flag, and never logged — the ARM client's error path echoes only the
response status + a bounded RESPONSE body, never the request body
(`TestClient_PutError_DoesNotLeakRequestBody`).

## Exact Azure RBAC actions the operator must grant the elevated credential

These are OPERATOR-SUPPLIED for the one-shot run, never held by the receiver.
`atlas-azure provision --print-rbac` prints this list; it is also in the README.

| Action                                                       | Why                                                                       |
| ------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `Microsoft.EventGrid/systemTopics/write`                     | create / upsert the system topic                                          |
| `Microsoft.EventGrid/systemTopics/delete`                    | tear down the system topic                                                |
| `Microsoft.EventGrid/systemTopics/eventSubscriptions/write`  | create / upsert the event subscription                                    |
| `Microsoft.EventGrid/systemTopics/eventSubscriptions/delete` | tear down the event subscription                                          |
| `Microsoft.Insights/diagnosticSettings/write`                | create / upsert the Activity-Log diagnostic setting (`--with-diagnostic`) |
| `Microsoft.Insights/diagnosticSettings/delete`               | tear down the Activity-Log diagnostic setting                             |

## Scope discipline

Connector-only. No `internal/` (platform) change, no migration, no RLS, no
openapi/routes, no `web/`. The only non-connector file touched is
`cmd/scripts/coverage-thresholds.json` (a new floor row for the new
`internal/provision` package — added to the floors map, not excludes, so no
exclude-justification entry is required).

## Revisit once in use

- If multi-subscription / multi-region provisioning is needed, the single-plan
  shape generalizes to a loop — filed as a spillover if demand surfaces.
- If a provisioning-status / list surface is wanted (e.g. "is the subscription
  already wired?"), it would be a read against ARM — separate from this write
  path. Filed as a spillover.
- If a future surface needs ARM long-running-operation polling or rich typed
  responses, reconsider D2 (the management SDK).
