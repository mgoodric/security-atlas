# 673 — Board Packs list fails to load in seeded tenant — `/api/board-packs` 500

**Cluster:** Board packs
**Estimate:** S-M (0.5-1.5d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 demo-tenant UI audit (ATLAS-025).

## Narrative

The Board Packs page shows "Could not load board packs / internal error"; `GET
/api/board-packs` returns **HTTP 500** (reproduced twice) in the seeded demo tenant. The same
page **loads fine in the default tenant**, so it is **data/seed-dependent** — the demo seed
writes `board_packs` / `board_briefs` rows that the list handler chokes on. Re-verified on
`main` build `2a3805b`. Likely related to slice 662 (ATLAS-005): the demo-seeded board pack
carries the `vendor_burndown` section data that the missing/unrenderable section path
mishandles.

## Threat model

Read-only, RLS-tenant-scoped list. The fix must keep tenant scoping and must not leak
internal error detail to the user (clean error/empty state per slice 367).

## Acceptance criteria

- [ ] **AC-1.** Root-cause the 500 from the seeded board-pack data — capture the logged error
      and identify the field/section/shape the list handler fails on (likely the
      `vendor_burndown` section or a null/format the seed produces).
- [ ] **AC-2.** `GET /api/board-packs` returns 200 with the seeded packs (or a clean empty
      state) in BOTH the default and demo-seeded tenants; an integration test seeds a board
      pack with the offending shape and asserts the list succeeds.
- [ ] **AC-3.** If the seed produces malformed board-pack data, fix the seed too (coordinate
      with slice 671/680 — the demo board pack should be coherent + listable).
- [ ] **AC-4.** Cross-check with slice 662 (vendor-burndown section) — resolve any shared
      root cause once rather than twice.

## Anti-criteria

- Does NOT widen board-pack scope or change publish semantics.
- Does NOT mask the 500 by catching-and-empty-stating without root-causing (AC-1 first).

## Dependencies

- The board-packs list API + the board-pack demo seed (`internal/api` board surface, `internal/demoseed`).
- Pairs with slice 662 (ATLAS-005).

## Notes

Source: 2026-06-10 demo-tenant audit, item **ATLAS-025** (high/major). Seed-data-dependent
(works in default tenant). Re-tested open on `2a3805b`.
