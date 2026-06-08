# Rippling connector

The Rippling connector (slice 491) brings the **authoritative worker roster +
joiner/mover/leaver lifecycle** into the platform's evidence pipeline. For the
solo security leader, the HRIS is the source of truth for who is employed, when
they joined, and — critically — when they left; that record is the spine of the
access-review (slice 374) and deprovisioning controls (SOC 2 CC6.1 / CC6.2 /
CC6.3, "access is granted on a need basis and revoked on termination"). Today
"prove every terminated employee's access was revoked" means reconciling the HRIS
against IdP/app rosters by hand; this connector brings the roster + termination
events into the ledger so reconciliation becomes a control evaluation.

It follows the locked connector pattern verbatim: register-per-run, a stable
`actor_id`, an hour-truncated `observed_at`, scope minimums, and vendor-native
read-only auth. It emits one evidence kind:

| Kind                       | Profile | Source                                                    |
| -------------------------- | ------- | --------------------------------------------------------- |
| `hris.worker_lifecycle.v1` | pull    | Rippling API `GET /platform/api/employees` (field-scoped) |

The evidence shape is **shared** with the BambooHR connector (the lifecycle field
set is identical at this altitude); `source_hris` preserves provenance.

The connector is **API-based**, not an in-host agent — consistent with the "no
closed proprietary collector agents" anti-pattern. The Rippling credential stays
source-side and never enters an evidence record or a platform push (canvas
invariant #3).

## The worker-lifecycle boundary (the load-bearing guard)

**The HRIS holds the most sensitive PII the platform will ever touch.** The
connector collects **worker-lifecycle facts ONLY**:

**Collected (in scope):**

- worker id (the stable key), employment status (active / terminated / on-leave /
  pending), start (hire) date, end (termination) date, title, department;
- the manager **assignment id** — the opaque manager worker id — for
  access-review routing;
- the **work email** — the only contact field, collected solely because the
  access-review join keys the roster against IdP/app accounts by work email.

**Never collected (out of scope — P0-491-3):**

- SSN / national id, compensation / salary, home address, bank / payment
  details, benefits / health enrollment, performance-review fields, date of
  birth, personal phone, gender / ethnicity / protected-class data.

The exclusion is enforced **structurally** at three layers:

1. the API request asks for ONLY the minimal `fields` set
   (`workers.LifecycleFields`) — the sensitive PII is never returned over the
   wire;
2. the `apiEmployee` / `RawWorker` structs have **no field** for any excluded
   value — a leak would be a compile error;
3. a test (`integration_test.go:TestEmittedRecords_NoSensitivePII`) asserts no
   over-collection key/substring reaches an emitted payload (AC-10), the
   client-level test asserts the `fields` query never requests a banned field,
   and another asserts the credential is never logged (AC-11).

## Least-privilege credential (required minimum)

Set `RIPPLING_API_TOKEN` (and optionally `RIPPLING_BASE_URL`) to a Rippling API
token scoped to the **read-only employee-directory / worker-lifecycle field
group only**. Run `atlas-rippling permissions` to print the canonical minimum.

- Grant only the read-only worker-lifecycle field group (roster + employment
  status).
- **NEVER** grant a full-PII read group (compensation, SSN, bank, benefits) or
  any **write** scope (threat-model E / P0-491-2).

The token is read from the environment, never a CLI flag (so it never lands in
shell history), and is never logged or placed into an evidence record.

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly. A real-time leaver signal via
Rippling termination webhooks (an event-driven profile) is a documented
follow-on (slice 573), not part of v0.

## Usage

```sh
# Print the least-privilege scope requirement.
atlas-rippling permissions

# Register the connector instance (profiles_supported = [pull]).
export SECURITY_ATLAS_ENDPOINT=atlas.example.com:443
export SECURITY_ATLAS_TOKEN=<platform bearer>
atlas-rippling register

# Read worker-lifecycle records and push evidence.
export RIPPLING_API_TOKEN=<read-only worker-lifecycle token>
atlas-rippling run --environment prod
```

## Scope minimums

Every record is scoped to `service` (`rippling`) and the required
`--environment`. Records carry `Result = INCONCLUSIVE`: the connector reports the
descriptive lifecycle facts; the platform evaluator owns the access-review
pass/fail per `(control, scope)` by reconciling this roster against the IdP/app
entitlements.

## Default SCF anchors (maintainer recheck — OQ #9)

The bundled schema carries default SCF-anchor hints, flagged for maintainer
accuracy recheck:

- `hris.worker_lifecycle.v1` → `IAC-22` (Termination of employment), `IAC-09`
  (Account management / provisioning), `HRS-04` (Personnel security) —
  consistent with the existing `okta.user_lifecycle.v1` anchors (same
  joiner/mover/leaver control question).

## Follow-ons (out of v0 scope)

- manager-hierarchy evidence for review-routing (slice 571);
- event-driven profile via Rippling termination webhooks — the highest-value
  upgrade, a real-time leaver signal for deprovisioning (slice 573).
