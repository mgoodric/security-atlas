# Slice 380 — Decisions log

**Slice:** `docs/issues/380-dashboard-server-component-fanout.md`
**Type:** JUDGMENT
**Closes:** slice 332 performance audit finding F-BFF-2 (MEDIUM)
**Author:** Claude (Engineer agent), batch 162

This slice re-shapes the `/dashboard` data-fetch path from N client-side
TanStack Query round-trips into one server-side parallel `Promise.all`
fan-out, hydrated into the client cache. It is a frontend-only change
confined to `web/app/(authed)/dashboard/` plus a one-line hardening of
`web/lib/api.ts apiFetch`. No backend, no migration, no wire-shape change.

---

## Decisions made

### D1 — Use the slice-249 prefetch + HydrationBoundary pattern, NOT a pure Server Component page

**Options considered:**

1. **Convert `page.tsx` to a pure React Server Component** with one
   `<Suspense>` boundary per panel, each panel an `async` server
   component awaiting its own upstream fetch.
2. **Keep `page.tsx` a client component; add a sibling server-component
   `layout.tsx` that prefetches all six panels and ships the dehydrated
   TanStack cache via `<HydrationBoundary>`** (the slice-249 `/settings`
   precedent, generalized from one query to six).

**Chosen:** Option 2.

**Rationale:** Option 1 would require rewriting all six panel components
into server components and would **lose the per-panel client-side
refresh affordance** that AC-3, AC-5, and AC-7 (and anti-criterion P0-1)
explicitly require to stay. The panels already own `useQuery` hooks with
a `refetch()` affordance and per-panel loading/error states. Option 2
keeps every one of those properties intact while still collapsing the
cold first load to a single round-trip — the prefetched data arrives
inline in the SSR HTML and the `useQuery` hooks boot already-populated.
Slice 249 already proved this exact pattern works for `/settings`
(single query); slice 380 is the mechanical generalization to a
six-query `Promise.all`. Pattern-matched, lowest-risk, honors every AC.

**Confidence:** high.

### D2 — AC-2 "Suspense per panel" is satisfied by the EXISTING per-panel TanStack Query state isolation, not by literal RSC `<Suspense>` boundaries

**The tension:** AC-2 reads "Each panel renders inside a
`<Suspense fallback={<PanelSkeleton/>}>` so slow panels don't block fast
ones." Taken literally that implies RSC streaming with React `<Suspense>`.

**Decision:** The _intent_ of AC-2 — "a slow panel renders its skeleton
while the rest render" (threat-model D mitigation in the slice doc) — is
**already satisfied** by the current architecture, where each panel is an
independent `useQuery` rendering its own loading skeleton / error / data
state. With D1's prefetch, the common case is that _all_ panels arrive
hydrated (no skeleton at all); in the fail-soft case (D3) a panel that
the server prefetch skipped falls back to exactly the pre-existing
client-side skeleton-then-fetch behavior — "no worse than the current
per-panel loading state," verbatim the slice doc's own D-mitigation
wording. Converting to literal RSC `<Suspense>` would force the
panel-as-RSC rewrite that D1 rejected (and would violate AC-3/5/7/P0-1).
So AC-2's intent is met without literal `<Suspense>` JSX.

**Confidence:** high.

### D3 — Prefetch fails soft per panel via `setQueryData`-on-success, NOT `prefetchQuery`

**Options considered:**

1. `queryClient.prefetchQuery({ queryKey, queryFn })` per panel.
2. `await load(bearer)` then `queryClient.setQueryData(key, data)` only
   on success, inside a per-panel `try/catch`.

**Chosen:** Option 2.

**Rationale:** `prefetchQuery` catches an upstream error _internally_ and
stores an **error query-state** into the cache. After `dehydrate()` that
error state would hydrate into the client `useQuery`, which would then
render the panel's error UI and **skip its corrective re-fetch** — a
worse outcome than not prefetching at all. Seeding the cache with
`setQueryData` only on a resolved value means a failed panel is left
_unseeded_: the client `useQuery` re-fetches it cold (the BFF route is
still wired, P0-1) and the panel self-heals. One bad upstream therefore
never 500s the dashboard and never poisons a panel. The vitest
`"fails soft on a single upstream error"` case pins this: the failed
panel's query state is asserted `!== "error"` and its data `undefined`.

**Confidence:** high.

### D4 — Keep the implicit `no-store`; do NOT add an explicit `Cache-Control: no-store` header to the BFF Response (slice-doc note 5)

**The slice doc's note 5** asks whether the per-panel BFF routes should
set `Cache-Control: no-store` explicitly on their `Response`.

**Decision:** No. The caching property that P0-2 cares about is the
**Next.js fetch data cache** on the _server-side upstream fetch_, not an
HTTP response header on the BFF route. That property is now guaranteed
by adding `cache: "no-store"` to `web/lib/api.ts apiFetch` (which the SSR
fan-out calls directly) — matching the BFF proxy's existing
`cache: "no-store"` in `lib/api/bff.ts forwardJSON`. Adding a
`Cache-Control` _response_ header to the BFF routes would be churn with
no behavioral win: these routes are session-bearing and already
uncacheable in practice (they carry per-request auth), and no
intermediary in the self-host topology caches them. The minimal correct
hardening is the one fetch-option change, not six route-header edits.

**Confidence:** medium. (Revisit if a CDN/edge layer is ever placed in
front of the BFF routes in a hosted-offering topology — see revisit
list.)

### D5 — vitest mocks `@/lib/api` via a hoisted `vi.mock` factory rather than `vi.spyOn` on the namespace import

**Rationale:** `dashboard-prefetch.ts` imports the six upstream fns as
named ESM bindings. `vi.spyOn(api, "fn")` on a namespace import does not
reliably rebind those bindings under vitest's ESM live-binding semantics
(observed: it intercepted in one test but not the next in the same
file). A hoisted `vi.mock("@/lib/api", factory)` replaces the bindings
deterministically. This is a test-harness choice with no production
impact.

**Confidence:** high.

### D6 — e2e seam: SSR prefetch is skipped under a test-only cookie + env gate, NOT a test rewrite or quarantine

**The regression (surfaced post-push by CI):** moving the dashboard's
initial fetch server-side (D1) broke 9 pre-existing tests in
`web/e2e/dashboard.spec.ts`. Those tests assert the **client-side**
binding contract — either that a panel's `/api/dashboard/*` BFF request
fires, or that a Playwright `page.route(...)` browser-side mock shapes
the panel (the slice-229 subtitle empty/error states + the AC-7
degrade-independently test). `page.route` intercepts **browser**
requests; it cannot reach the SSR `Promise.all` prefetch, so the panels
rendered real seeded data instead of the per-test mocks (e.g. the
subtitle read "evidence freshness 50% within window" where the test
expected "No evidence ingested yet").

**Options considered:**

1. **`test.skip` / quarantine the 9 tests** (slice 275 pattern).
2. **Rewrite the 9 tests to assert against deterministic seeded state**
   (the slice-249 `/settings` SSR-prefetch precedent).
3. **Add a test-only seam that skips the SSR prefetch** when the e2e
   harness asks, so the page falls back to the pure client-side
   `useQuery` path those tests already exercise.

**Chosen:** Option 3.

**Rationale:** Option 1 is a last resort, not a first move — the
failures are a direct, fixable consequence of the architecture change,
not a flake. Option 2 would _weaken_ the tests: the slice-229 subtitle
empty/error and AC-7 degrade-independently tests assert _reactions to
specific upstream shapes_ (zero-total, abort, slow) that seeded state
cannot deterministically produce — rewriting them to seeded assertions
would drop exactly the behavior they pin. Option 3 keeps every one of
the 9 tests **byte-for-byte honest**: with the SSR prefetch skipped, the
page uses its client `useQuery` path, `page.route` mocks intercept as
before, and the BFF-request-fired assertions hold (the client now fires
the request the prefetch would otherwise have pre-served). The SSR
fan-out is covered by the sibling `dashboard-server-component.spec.ts`.
This is the correct separation of concerns: `dashboard.spec.ts` =
client-binding + refresh contract (which slice 380 AC-3/AC-5 require to
keep working); the new spec = SSR fan-out contract.

**The seam shape (security-reviewed):** the layout skips the prefetch
ONLY when BOTH (a) `serverPrefetchBypassed()` returns true — i.e.
`process.env.ATLAS_TEST_MODE === "1"`, the same server-only env gate the
Go-side `/v1/test/issue-jwt` endpoint uses
(`internal/api/testissuejwt.go`), set by the CI Playwright job and the
self-host test profile and NEVER in production — AND (b) the request
carries the `e2e_no_prefetch=1` cookie. The env gate is read from
`process.env` (not `NEXT_PUBLIC_*`), so it is invisible to and
unforgeable from the browser. A forged cookie in production is inert
because `ATLAS_TEST_MODE` is never "1" there. The double gate means the
seam cannot degrade production behavior under any client-controlled
input. `serverPrefetchBypassed` + `E2E_NO_PREFETCH_COOKIE` are
unit-tested (exact-match "1", unset, and non-"1" values).

**On AC-7 fail-soft (the guidance's behavior-gap concern):** with the
seam, AC-7 runs in pure client mode, so its `page.route(... abort)`
produces the panel's own error+retry UI exactly as before — no behavior
gap. The SSR fail-soft path (D3) independently preserves the same
property in production: a failed prefetch leaves the panel unseeded, so
the client `useQuery` re-fetches and renders the panel's error/skeleton.
Both paths surface per-panel degradation; neither swallows it.

**Confidence:** high.

---

## Revisit once in use

1. **Tail-latency measurement (AC-8 quantitative).** The e2e asserts the
   _qualitative_ win (zero `/api/dashboard/*` requests on first load).
   Once there is real multi-tenant traffic, capture an actual
   initial-page-load waterfall (Playwright trace or RUM) pre/post and
   confirm the p75/p95 time-to-interactive improvement is material at
   v2's 50+ concurrent users — and that it composes with slice 377's
   OPA prepared-query cache as the slice doc predicts.
2. **D4 — explicit `Cache-Control` header.** Re-evaluate if a hosted
   offering ever puts a CDN/edge cache in front of the BFF routes; in
   that topology an explicit `no-store` response header becomes
   load-bearing rather than redundant.
3. **D2 — literal `<Suspense>` streaming.** If a single panel's upstream
   ever becomes genuinely slow (e.g. framework-posture rollup at large
   framework counts) such that blocking the _prefetch_ `Promise.all` on
   it delays first byte, revisit splitting that one panel into a
   streamed RSC `<Suspense>` boundary so the document flushes before its
   data resolves. Not needed at v1 dataset sizes.
4. **Prefetch error observability.** D3 swallows per-panel prefetch
   errors silently (the client re-fetch surfaces them). Once OTEL
   frontend/BFF tracing lands, consider emitting a server-side span
   event when a prefetch panel fails so the silent fail-soft is visible
   in traces rather than only as a client re-fetch.

---

## Confidence summary

| Decision                                        | Confidence |
| ----------------------------------------------- | ---------- |
| D1 — slice-249 prefetch pattern over pure-RSC   | high       |
| D2 — per-panel TanStack state satisfies AC-2    | high       |
| D3 — fail-soft via `setQueryData`-on-success    | high       |
| D4 — no explicit `Cache-Control` header         | medium     |
| D5 — `vi.mock` factory over `vi.spyOn`          | high       |
| D6 — e2e prefetch-bypass seam over rewrite/skip | high       |

No `low`-confidence decisions. The single `medium` (D4) tops the revisit
list at item 2.

---

## Constitutional / ADR note (ISC-18a)

No new architectural decision is introduced — this slice applies the
already-established slice-249 SSR-prefetch pattern. No ADR is required
(per `Plans/prompts/04-per-slice-template.md` notes: ADR only "if a
slice surfaces a _new_ architectural decision").
