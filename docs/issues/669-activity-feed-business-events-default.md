# 669 — Activity ledger is dominated by internal read-telemetry (low signal-to-noise)

**Cluster:** Activity
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (default filter / which kinds are "business" events)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-018).

## Narrative

The tenant Activity feed (`/activity`, 201 rows observed) is **dominated by internal
"decision … read" telemetry** (the app auditing its own reads). Human-meaningful events
(e.g. `demo_seed`, `users` write) are buried. Re-verified on `main` build `2a3805b`. Filed
as an enhancement (UX/filtering).

## Threat model

Read-only view; tenant-scoped via RLS. The default filter must not HIDE security-relevant
mutations (auth, role, tenant, exception) — only de-prioritize high-volume read-telemetry.
No audit data is deleted; this is presentation/filtering only (the ledger stays complete).

## Acceptance criteria

- [ ] **AC-1.** The Activity view **defaults to mutating/business events** (writes:
      auth/role/tenant/risk/control/evidence/exception/demo_seed, etc.), with read-telemetry
      ("decision … read") **excluded by default**.
- [ ] **AC-2.** Read-telemetry is reachable via an **opt-in filter / kind toggle** (not
      removed) so the full ledger is still inspectable.
- [ ] **AC-3.** JUDGMENT (decisions log): define the "business event" allow-list (or the
      "read-telemetry" deny-list) used for the default; record the classification.
- [ ] **AC-4.** The underlying audit ledger is unchanged (filtering is a view concern, not a
      retention change); a test pins the default-filtered set vs the show-all set.

## Anti-criteria

- Does NOT delete or stop recording read-telemetry (retention unchanged; view-only filter).
- Does NOT hide security-relevant mutations by default.

## Dependencies

- The Activity feed + its source (`internal/api` activity/audit endpoint; `web/app/(authed)/activity`).
- Related to slice 667 (dashboard activity chips) — share the kind-filter model if both touch it.

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-018** (priority low /
severity minor; type enhancement). Re-tested open on build `2a3805b`.
