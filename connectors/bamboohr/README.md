# BambooHR connector

The BambooHR connector (slice 491) brings the **authoritative worker roster +
joiner/mover/leaver lifecycle** into the platform's evidence pipeline — the same
control question as the Rippling connector (the employee roster + termination
events that feed the access-review (slice 374) and deprovisioning controls, SOC 2
CC6.1 / CC6.2 / CC6.3). It follows the locked connector pattern verbatim:
register-per-run, a stable `actor_id`, an hour-truncated `observed_at`, scope
minimums, and vendor-native read-only auth. It emits one evidence kind:

| Kind                       | Profiles        | Source                                                                                                   |
| -------------------------- | --------------- | -------------------------------------------------------------------------------------------------------- |
| `hris.worker_lifecycle.v1` | pull, subscribe | BambooHR API `GET /v1/reports/custom` (field-scoped poll) + the BambooHR termination webhook (slice 573) |

The evidence shape is **shared** with the Rippling connector (the lifecycle field
set is identical at this altitude); `source_hris` preserves provenance.

The connector is **API-based**, not an in-host agent. The BambooHR credential
stays source-side and never enters an evidence record or a platform push (canvas
invariant #3).

## The worker-lifecycle boundary (the load-bearing guard)

**The HRIS holds the most sensitive PII the platform will ever touch.** The
connector collects **worker-lifecycle facts ONLY**:

**Collected (in scope):**

- worker id (the stable key), employment status (active / terminated / on-leave /
  pending), hire date, termination date, job title, department;
- the manager (supervisor) **assignment id** — the opaque supervisor employee id
  — for access-review routing;
- the **work email** — the only contact field, collected solely for the
  access-review join against IdP/app accounts.

**Never collected (out of scope — P0-491-3):**

- SSN / national id, compensation / pay rate, home address, bank / payment
  details, benefits / health enrollment, performance-review fields, date of
  birth, personal phone, gender / ethnicity / protected-class data.

The exclusion is enforced **structurally** at three layers:

1. the connector uses a **custom report scoped to the lifecycle `fields` only**
   (`workers.LifecycleFields`) — NOT the `/employees/directory` endpoint (whose
   field set is fixed and cannot be narrowed) nor the full-employee endpoint;
2. the `apiEmployee` / `RawWorker` structs have **no field** for any excluded
   value — a leak would be a compile error;
3. a test (`integration_test.go:TestEmittedRecords_NoSensitivePII`) asserts no
   over-collection key/substring reaches an emitted payload (AC-10), the
   client-level test asserts the `fields` query never requests a banned field,
   and another asserts the credential is never logged (AC-11).

## Least-privilege credential (required minimum)

Set `BAMBOOHR_API_KEY` + `BAMBOOHR_COMPANY_DOMAIN` (and optionally
`BAMBOOHR_BASE_URL`). The API key must belong to a user whose role grants
**read-only access to the worker-directory / employment-status fields only**. Run
`atlas-bamboohr permissions` to print the canonical minimum.

- Use only a key for a read-only worker-directory role (roster + employment
  status).
- **NEVER** use a key for a role that can see compensation, SSN, bank, benefits,
  home address, or performance, or that can **edit** employees (write scope) —
  threat-model E / P0-491-2.

The key is sent as the HTTP Basic username, read from the environment, never a
CLI flag, and never logged or placed into an evidence record.

## Profiles — honest naming, not "continuous monitoring"

The connector supports two retrieval profiles. Both describe how the connector
retrieves data **from BambooHR**; the platform-side wire is **always push**
(invariant #3) regardless of profile.

**`pull`** — each invocation is one bounded read-and-push pass, **operator-scheduled**
(cron / scheduler), recommended cadence **every 24h**. Deliberately **not**
"continuous monitoring": the interval is named honestly.

**`subscribe`** (slice 573) — a long-lived **source-side webhook receiver** that
runs **inside this connector process** (`atlas-bamboohr subscribe`). It receives
BambooHR termination / status-change webhook deliveries, **verifies the
per-monitor HMAC-SHA256 signature** (`X-BambooHR-Signature`) **before** doing any
work, re-reads the affected worker's minimal lifecycle fields via the same
read-only API, builds the **same** `hris.worker_lifecycle.v1` record, and pushes
it. This is **event-driven**, not "continuous monitoring", and **not** a platform
inbound API — the receiver is part of the connector. The webhook is a **trigger**;
the authoritative lifecycle facts come from the bounded re-read, so the
over-collection guard is unchanged.

**Dedup against pull:** a webhook-emitted record and a pull-emitted record for the
same worker within the same UTC hour derive the **same idempotency key**, so the
append-only ledger collapses them.

**Webhook auth:** set `BAMBOOHR_WEBHOOK_SECRET` to the per-monitor private key.
Front the receiver with a reverse proxy for TLS; it binds loopback by default. An
unsigned, forged, or wrong-signature delivery is rejected with `401` and produces
no record; an oversized body is rejected with `413` before it is read.

## Usage

```sh
# Print the least-privilege scope requirement.
atlas-bamboohr permissions

# Register the connector instance (profiles_supported = [pull, subscribe]).
export SECURITY_ATLAS_ENDPOINT=atlas.example.com:443
export SECURITY_ATLAS_TOKEN=<platform bearer>
atlas-bamboohr register

# pull: read worker-lifecycle records and push evidence (operator-scheduled).
export BAMBOOHR_API_KEY=<read-only worker-directory key>
export BAMBOOHR_COMPANY_DOMAIN=<your-company-subdomain>
atlas-bamboohr run --environment prod

# subscribe: run the source-side termination-webhook receiver (event-driven).
export BAMBOOHR_WEBHOOK_SECRET=<per-monitor private key>
atlas-bamboohr subscribe --environment prod --listen 127.0.0.1:8534 --path /hooks/bamboohr
```

## Scope minimums

Every record is scoped to `service` (`bamboohr`) and the required
`--environment`. Records carry `Result = INCONCLUSIVE`: the connector reports the
descriptive lifecycle facts; the platform evaluator owns the access-review
pass/fail per `(control, scope)`.

## Default SCF anchors (maintainer recheck — OQ #9)

- `hris.worker_lifecycle.v1` → `IAC-22` (Termination of employment), `IAC-09`
  (Account management / provisioning), `HRS-04` (Personnel security) —
  consistent with the existing `okta.user_lifecycle.v1` anchors.

## Follow-ons (out of v0 scope)

- manager-hierarchy evidence for review-routing (slice 571);
- ~~event-driven profile via BambooHR webhooks~~ **delivered (slice 573)** — see
  the `subscribe` profile above;
- cursor pagination for the single-pass `pull` report (threat-model D);
- a multi-employee webhook fan-out (the current receiver acts on the first
  changed employee per delivery — a single termination is the dominant leaver
  case).
