# 655 — BambooHR webhook multi-employee fan-out

**Cluster:** Connectors
**Estimate:** S (0.5d)
**Type:** code
**Status:** `ready`

## Narrative

Surfaced during slice 573 (HRIS event-driven termination-webhook profile). The
BambooHR `subscribe` receiver acts on the **first** changed employee in a webhook
delivery's `employees[]` array. BambooHR can deliver multiple changed employees in
one webhook (e.g. a bulk status change). A single termination is the dominant
leaver case, so slice 573 deliberately scoped to the first employee and recorded
the fan-out as a follow-on (decisions log Revisit #3).

This slice extends the BambooHR `bambooParser` + the receiver pipeline to re-read
and push a record for **each** changed employee in a single delivery, reusing the
same trigger + re-read + dedup machinery slice 573 established. The Rippling
envelope is single-worker, so this is BambooHR-only.

## Dependencies

- **#573** (HRIS event-driven termination-webhook profile) — must merge first;
  the shared `connectors/hris/webhook` receiver, the `PayloadParser` interface,
  and the `FetchOne` re-read are reused.

## Anti-criteria (P0)

- Does NOT widen the platform-side wire — push only (invariant #3); the receiver
  stays source-side in the connector.
- Does NOT collect anything beyond the slice-491 worker-lifecycle field set; each
  fan-out re-read uses the same minimal `fields` guard.
- Does NOT process an unsigned / invalidly-signed delivery; signature
  verification stays the first step, once per delivery, before any fan-out.

Parent: #573.
