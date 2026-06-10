# Slice 522 — Azure Event Grid event-driven (subscribe) profile — decisions log

**Type:** JUDGMENT (subscribe-profile shape + Event-Grid delivery-auth + event→reader routing + dedup-with-pull interaction)
**Slice:** 522 — Azure connector: event-driven (subscribe) profile via Event Grid / Activity Log
**Built onto:** `connectors/shared/webhookrecv` (slice 656) + the Intune validation-handshake-in-adapter precedent (slice 557).

This log records the build-time subjective calls Claude made (per the JUDGMENT
slice type). The maintainer iterates post-deployment; the merge is not blocked on
human sign-off. None of these calls touch the product runtime AI-assist boundary.

---

## Decisions

### D1 — Event-Grid delivery-auth scheme: operator-configured delivery key, constant-time header compare

Event Grid secures a webhook subscription two ways: (a) the **validation handshake**
that establishes the endpoint at subscription-create time, and (b) **per-delivery**
authentication of each real event. These are orthogonal: the handshake proves the
endpoint owner controls the URL once; the per-delivery credential authenticates
every subsequent event.

For per-delivery auth I implement an operator-configured **delivery key**
(a static shared secret Event Grid replays on every delivery) checked
**constant-time**. Event Grid supports placing a secret in the delivery URL as a
query parameter (`?key=...` / a custom query param) or — for Entra-authenticated
delivery — an `Authorization: Bearer` header. The connector accepts the credential
in a configurable location:

- a request **header** (default `Authorization`, e.g. for an Entra-issued bearer or
  a custom shared-secret header), OR
- a request **query parameter** (default `code`, mirroring the Event-Grid "delivery
  key in the URL" convention).

The verifier (`eventgrid.NewDeliveryKeyVerifier`) holds the configured secret
unexported (a stray `%v` cannot leak it) and compares constant-time via
`crypto/subtle.ConstantTimeCompare`. A missing or mismatched credential returns
`webhookrecv.ErrBadSignature` so the shared skeleton rejects **401 BEFORE** any
parse / re-read / record build (verify-FIRST). This mirrors the slice-557 Jamf
`SharedSecretVerifier` shape (Event Grid, like Jamf, does not HMAC-sign the body —
the operator configures a static credential the source replays). The secret is read
from the connector environment (`AZURE_EVENTGRID_DELIVERY_KEY`), never a CLI flag
(it would land in shell history), never logged, never placed into a record.

**Why not body-HMAC:** Event Grid does not sign event payloads with a per-event
HMAC the receiver can recompute; the shared `webhookrecv.HMACConfig` core therefore
does not apply. The one-method `webhookrecv.Verifier` interface fits the
delivery-key compare exactly (same as the Intune `ClientStateVerifier` did).

### D2 — SubscriptionValidation handshake lives in the Azure adapter (NOT the shared package)

When Event Grid creates/validates a webhook subscription it POSTs a single event of
type `Microsoft.EventGrid.SubscriptionValidationEvent` carrying
`data.validationCode`. The receiver MUST respond **200** with
`{"validationResponse":"<code>"}` (JSON) and build **NO** record. This is the
**synchronous validationCode echo** — the primary Event-Grid validation path. (Event
Grid also offers a manual handshake via a `data.validationUrl` GET the operator can
visit; that is a fallback for endpoints that cannot echo synchronously. This
connector implements the **synchronous validationCode echo** — it can always echo —
and surfaces the `validationUrl` only by ignoring it; no GET endpoint is exposed.)

Following slice 557's directive ("if the shared seam can't express the handshake,
own it in the adapter rather than bending the shared package"), the
`validationHandler` wraps the receiver and intercepts the SubscriptionValidation
event **FIRST**, before delegating real deliveries to the shared verify-first
skeleton. The handshake path:

- is recognised by the event's `eventType == "Microsoft.EventGrid.SubscriptionValidationEvent"`,
- bounds the echoed code length (a hostile caller cannot make us reflect an
  unbounded body),
- responds `200 application/json {"validationResponse": code}`,
- triggers **NO** verification, **NO** re-read, **NO** record (a test pins all three).

Note an asymmetry vs. Intune: Intune's handshake is a `validationToken` **query
param** on an otherwise-empty body; Event Grid's is an **event in the POST body**.
So the Event-Grid `validationHandler` parses the body to detect the validation
event (a cheap structural check) before the credential path runs. The validation
event is unauthenticated by design (it establishes the endpoint before the operator
has wired the delivery key into deliveries) — so it MUST be intercepted before the
verify-first skeleton, exactly as Intune's was.

### D3 — event → resource-type → reader routing table

The event is a **TRIGGER**, not the data. From a real change event the connector
takes ONLY the changed **resource ID** + **resource type** (derived from the event's
`subject` / `data.resourceUri` ARM path and/or `eventType`), then routes to the
EXISTING reader, re-reads, and emits the matching EXISTING kind. The routing table
(resource-provider path segment → reader):

| ARM resource-provider segment in the resource id            | reader   | emitted kind                    |
| ----------------------------------------------------------- | -------- | ------------------------------- |
| `Microsoft.Storage/storageAccounts`                         | storage  | azure.storage_account_config.v1 |
| `Microsoft.ContainerService/managedClusters`                | aks      | azure.aks_cluster_config.v1     |
| `Microsoft.Network/networkSecurityGroups`                   | nsg      | azure.nsg_rules.v1              |
| `Microsoft.KeyVault/vaults`                                 | keyvault | azure.keyvault_access_config.v1 |
| `Microsoft.Network/firewallPolicies`                        | firewall | azure.firewall_rules.v1         |
| `Microsoft.Authorization/roleAssignments` (Entra/directory) | entra    | azure.entra_role_assignment.v1  |

Any other resource provider (or an event with no parseable ARM resource id) is
**dropped honestly** — the connector logs at most a `%q`-escaped one-liner and acks
**200** so Event Grid does not retry an event the connector has no reader for. We do
NOT invent a reader for an unmapped type, and we do NOT build a record from an
unmapped event's payload.

### D4 — re-read semantics: re-run the existing reader, emit ONLY the changed resource

The existing readers (`storage.Inspect`, `aks.Inspect`, `entra.Pull`, …) are
subscription-wide list scans — they read config only (no over-collection; the guard
is the struct shape, UNCHANGED). On an event the adapter:

1. acquires a read-only token for the reader's scope (ARM Reader / Graph),
2. re-runs the existing reader (UNCHANGED) over the subscription/tenant,
3. **filters** the returned records to the one whose resource id matches the event's
   changed resource id (case-insensitive ARM-id compare),
4. builds the matching kind via the EXISTING record builder (UNCHANGED) and pushes.

The record's data therefore comes ENTIRELY from the re-read, never the event
payload — a forged/payload-only event whose resource id does not resolve to a real
resource yields ZERO records (the filter finds nothing). A test asserts this
(no-fabrication). The re-read reads exactly what the pull profile reads — never
beyond it.

**Why re-run the list reader rather than a new get-by-id path:** adding a
get-single-resource ARM path per reader is new surface (six new client methods) the
slice does not need; re-using the UNCHANGED list reader + a resource-id filter keeps
the over-collection guard and the record-builder byte-identical, so the
subscribe-emitted record is provably the same shape as the pull-emitted one (the
dedup in D5 then collapses them). The cost is a subscription-wide list per event;
the coalescing window (D6) bounds how often that happens.

### D5 — cross-profile dedup: the slice-486 idem key, UNCHANGED

The record builders derive `idempotency_key = sha256("<kind>|<resource_id>|<hour>")`
(hour-truncated UTC `observed_at`) via the slice-486 `internal/idem` package,
UNCHANGED. Because the subscribe path re-uses the SAME builder, a subscribe-emitted
record and a pull-emitted record for the SAME resource in the SAME UTC hour derive
the IDENTICAL key and collapse to ONE ledger row. A test pins this (subscribe event

- pull of the same resource within the hour → one key).

**Mid-hour-event vs top-of-next-hour-pull interaction (documented):** a subscribe
event at 14:45 and a pull at 15:05 fall in DIFFERENT UTC hours → two ledger rows
(14:00 and 15:00), which is correct: they are two genuine observations an hour
apart. Within one hour they collapse; across an hour boundary they do not. The pull
profile remains the reconciliation backstop — if subscribe misses an event, the next
pull catches the drift (a fresh hour, a fresh row).

### D6 — coalescing window + queue bounds (DoS, threat-model D)

An event storm (e.g. a script touching every storage account) could drive one
re-read (a subscription-wide list) per event. Mitigation:

- a **bounded queue** (`DefaultQueueDepth = 1024` events) — a full queue drops the
  newest event and logs a `%q`-escaped counter (the pull backstop catches anything
  dropped),
- a **coalescing window** (`DefaultCoalesceWindow = 5s`): events for the SAME
  resource id arriving within the window collapse into ONE re-read. A worker drains
  the queue, groups pending events by resource id, and performs one re-read per
  distinct resource id per window tick.

The receiver's `ServeHTTP` returns **200 immediately** after enqueue (Event Grid
expects a fast ack; a slow handler triggers Event-Grid retries, amplifying the
storm). The actual re-read/push happens on the background worker. Verify-first still
holds: the credential is checked in the handler BEFORE enqueue, so an unauthenticated
event never reaches the queue.

The receiver lifecycle: the `webhook`/`eventgrid` subcommand starts the worker, then
`Serve(ctx)` blocks until SIGINT/SIGTERM, then drains.

### D7 — receiver lifecycle + bind config

Mirrors the Intune `webhook` subcommand: `--listen` (default loopback
`127.0.0.1:8485`), `--path` (default `/webhooks/azure/eventgrid`), graceful
shutdown on signal. Event Grid requires an HTTPS endpoint with a valid certificate —
the operator terminates TLS at a reverse proxy in front of this loopback-bound
process (the connector itself binds plaintext loopback; the proxy owns TLS). Named
honestly as `profile=subscribe (event-driven via Event Grid)` — NOT continuous
monitoring.

---

## Revisit (post-deployment)

- **R1 — Activity-Log diagnostic-settings auto-provisioning.** This slice receives
  events; it does NOT auto-create the Event-Grid system topic / Activity-Log
  diagnostic setting that routes events to the webhook. Operators wire that in the
  Azure portal / IaC today. Auto-provisioning (the connector creating its own
  subscription) is a separate concern and would need a write-scope permission this
  slice explicitly excludes — filed as a spillover.
- **R2 — shared validation-handshake hook in `webhookrecv`.** BOTH Intune (557) and
  Event Grid (522) now own a validation-handshake-in-adapter. The 557 decisions log
  flagged "file a first-class shared hook when a 2nd validation-handshake connector
  arrives." Event Grid is that 2nd one — filed as a spillover (do NOT refactor here).
- **R3 — per-event get-by-id re-read.** D4 re-runs the list reader + filters. If
  event volume makes the per-event subscription-wide list too costly, a get-by-id
  ARM path per reader is the optimisation (a later slice).
- **R4 — coalescing-window / queue-depth tuning.** The 5s / 1024 defaults are
  judgment; operators may want them configurable. Left as constants for v0.

---

## Confidence

- **D1 (delivery-key constant-time verify):** HIGH — direct mirror of the shipped
  slice-557 `SharedSecretVerifier`; constant-time compare; verify-first enforced by
  the shared skeleton.
- **D2 (validation handshake in adapter):** HIGH — direct mirror of slice-557's
  Intune `validationHandler`; the only delta is body-event vs query-param detection.
- **D3 (routing table):** MEDIUM-HIGH — the ARM resource-provider → reader mapping is
  mechanical; the drop-unmapped-honestly path is tested.
- **D4 (re-read-list-then-filter):** MEDIUM — correct and over-collection-safe, but
  re-runs a subscription-wide list per coalesced event; R3 is the optimisation if
  volume demands it.
- **D5 (idem dedup):** HIGH — the key is the slice-486 key UNCHANGED; the builder is
  reused byte-identical; a test pins the collapse.
- **D6 (coalescing + queue):** MEDIUM — bounds the storm; the window/depth are
  judgment defaults (R4).

## Detection-tier classification

- `detection_tier_actual`: unit — the bugs surfaced during the build (resource-id
  ARM-path case sensitivity; the validation-event-before-verify ordering; the
  filter-finds-nothing no-fabrication path) were all caught by the eventgrid
  package's pure-Go unit tests and the cmd seam tests.
- `detection_tier_target`: unit — these are pure-Go parse/route/verify/coalesce
  branches and HTTP-handler ordering, all expressible without Postgres or live
  Azure; unit is the correct tier and is where they were caught.
