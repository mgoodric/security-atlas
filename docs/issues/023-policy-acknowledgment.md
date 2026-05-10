# 023 — Policy acknowledgment workflow + role-required attestation

**Cluster:** Policies
**Estimate:** 1d
**Type:** AFK

## Narrative

Implement the policy acknowledgment workflow. Policies declare `acknowledgment_required_role[]` — roles whose members must annually attest the policy. On policy publish + on role assignment, the system queues an acknowledgment request. Users sign in, see required acknowledgments in their inbox, click to attest. Each attestation is recorded as an evidence record (`policy.acknowledgment.v1` kind) — feeding the "Policy attestation rate" KPI in slice 031. The slice delivers value because the dashboard's policy-attestation metric becomes real, not aspirational.

## Acceptance criteria

- [ ] AC-1: User signing in sees pending acknowledgments at `GET /v1/me/acknowledgments`
- [ ] AC-2: `POST /v1/policies/:id/acknowledge` records the attestation as an evidence record
- [ ] AC-3: A user without a required role doesn't see the acknowledgment task
- [ ] AC-4: Acknowledgments are bound to a `policy_version_id` — superseded policies require re-acknowledgment of the new version
- [ ] AC-5: Annual recurrence: 365 days after last ack, the task reappears
- [ ] AC-6: `GET /v1/policies/:id/acknowledgment-rate` returns the percent of required role-members who have acknowledged in the window

## Constitutional invariants honored

- **Invariant 9 (manual evidence first-class):** policy acknowledgments flow through the same evidence ledger
- **Invariant 6 (RLS):** acknowledgment rows tenant-scoped

## Canvas references

- `Plans/canvas/02-primitives.md` §2.6 (Policy.acknowledgment_required_role)
- `Plans/canvas/07-metrics.md` §7.1 (Policy attestation rate KPI)

## Dependencies

- #022, #034

## Anti-criteria (P0)

- Does NOT permit acknowledgment without authenticated user
- Does NOT count acknowledgments older than the freshness window toward the rate
- Does NOT permit ack of a superseded policy version as ack of the current

## Skill mix (3–5)

- Next.js inbox UI
- Go acknowledgment handlers
- Evidence ledger client (slice 013 push)
- Time-window aggregation queries
- Role-based UI gating
