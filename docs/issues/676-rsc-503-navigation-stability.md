# 676 — Pervasive 503s on Next.js RSC prefetch/navigation requests

**Cluster:** Platform / stability
**Estimate:** M (1-2d, investigation-led)
**Type:** JUDGMENT (root-cause: overload vs cascading backend error vs handler bug)
**Status:** `ready` — surfaced by the 2026-06-10 demo-tenant UI audit (ATLAS-026).

## Narrative

A large volume of RSC requests (URLs with `?_rsc=…`) across `/dashboard`,
`/dashboards/metrics`, `/board-packs`, and metric detail pages return **HTTP 503** — dozens
of `?_rsc=` GETs returned 503 in a single session; only a few returned 200. Re-verified on
`main` build `2a3805b`. Likely causes flaky navigation, slow/failed prefetch, and
intermittent loading skeletons.

This may be **partly a symptom**: the 503s concentrate on pages whose data calls are
currently erroring (board-packs 500 → slice 673; OSCAL 500 → slice 659; metrics 0 → 677).
Some 503s may resolve as those are fixed; the rest point at server overload or an RSC handler
returning 503 under load (the edge box is a single VM sharing an Unraid host).

## Threat model

Availability/stability, not data. No scope/wire change. Investigation must distinguish a real
capacity/handler problem from downstream-500 cascades so the fix targets the true cause.

## Acceptance criteria

- [ ] **AC-1.** Capture which routes' `?_rsc=` requests 503 and correlate with the underlying
      data-call status (are the 503s concentrated on pages with a failing API — 659/673/677?).
- [ ] **AC-2.** Determine the 503 origin: the web (Next.js) server returning 503 under
      load/timeout, the BFF translating a backend timeout, or resource exhaustion on the edge
      VM. Record findings (decisions log).
- [ ] **AC-3.** If cascade: confirm the rate drops materially once 659/673/677 land (re-test);
      file any residual as the genuine stability issue.
- [ ] **AC-4.** If genuine overload/handler bug: fix it (timeout/concurrency/caching on the
      RSC path, or resource limits) so navigation/prefetch is reliable; add a smoke check.

## Anti-criteria

- Does NOT paper over downstream 500s by suppressing the 503 (must root-cause first).
- Does NOT require infra changes to the user's Unraid host as the primary fix (app-level first).

## Dependencies

- The web server / BFF (`web/`) + the edge deployment characteristics (see `OPERATIONS.local.md`).
- Correlates with slices 659 (OSCAL 500), 673 (board-packs 500), 677 (metrics).

## Notes

Source: 2026-06-10 demo-tenant audit, item **ATLAS-026** (medium/major). Re-tested open on
`2a3805b`. Investigation-led — outcome may be "largely cascade of 659/673/677" + a small
residual.
