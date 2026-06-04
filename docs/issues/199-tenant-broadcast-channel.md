# 199 — Cross-tab BroadcastChannel sync for tenant-switcher

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Spillover from slice 192 (multi-tenant switch + frontend tenant switcher). Slice 192 shipped the `<TenantSwitcher>` dropdown at `web/components/auth/tenant-switcher.tsx`. Under the slice 192 model each tab carries its own JWT cookie; switching tenants in tab A does NOT reflect in tab B until the operator manually refreshes tab B.

Slice 199 closes that gap by wiring the switcher to the browser's `BroadcastChannel` API so that when one tab successfully switches tenants, sibling tabs in the same origin re-fetch their own tenant state and re-render. This is the v3-shape UX polish that brings the multi-tenant experience to feature-complete.

**What this slice ships:**

1. NEW module `web/lib/auth/tenant-broadcast.ts` — a thin wrapper around `BroadcastChannel('atlas-tenant')` exposing `postTenantSwitched(targetTenantId)` and `onTenantSwitched(cb)` helpers. Graceful no-op when `BroadcastChannel` is undefined (SSR, older browsers, Safari quirks).
2. `<TenantSwitcher>` posts a message on a successful switch (after `switchTenant()` returns `ok: true` but before the local `router.refresh()`).
3. `<TenantSwitcher>` subscribes on mount. On a received message it re-fetches `/api/me/tenants` and calls `router.refresh()` — but does NOT trust the broadcast tenant_id blindly; the receiving tab re-validates its own JWT cookie state via the existing periodic-refetch path (slice 192 D1).
4. Vitest unit tests with a mocked `BroadcastChannel` covering: send, receive, no-op when undefined, no self-broadcast loops, channel-name uniqueness.
5. The channel is closed on component unmount; no leaks.

**SCOPE DISCIPLINE — what's deliberately out:**

- No new BFF routes — purely client-side.
- No `localStorage`/`storage`-event fallback for browsers without BroadcastChannel — graceful degradation means the experience reverts to slice 192's pre-this-slice shape (manual refresh) on those browsers. Documented; not engineered around.
- No cross-origin sync (BroadcastChannel is same-origin by design — that is the correct trust boundary).
- No SharedWorker or ServiceWorker pathways.
- No new server-pushed events (no SSE, no WebSocket). The mechanism is purely peer-to-peer between tabs within the same origin/process.

## Canvas references

- canvas §11 #13 (single-tenant invisibility — preserved; the broadcast helper still null-renders when `tenants.length <= 1`)
- canvas §5.4 (tenant isolation enforced at DB layer via RLS — frontend broadcast cannot grant access; the receiving tab still validates its own JWT)

## Constitutional invariants honored

- **Invariant 6** (tenant isolation at DB layer): receiving tab does NOT trust the broadcast payload; it re-fetches `/api/me/tenants` which is gated by the JWT middleware (slice 190) which is gated by RLS (slice 002). The broadcast is a "go look again" nudge, not an authority.
- **AI-assist boundary**: untouched — broadcast carries only the target tenant id (an identifier the receiving tab already had in its `available_tenants[]` claim or did not). No evidence, no policy, no AI-generated content traverses the channel.

## Threat model

**S — Spoofing.** A malicious script in another tab posts a forged tenant-switch message.

- Mitigation: BroadcastChannel is same-origin. A cross-origin attacker cannot post to `atlas-tenant`. A same-origin XSS in another tab is a different threat class (already covers a much larger attack surface than this channel). The receiving tab also does NOT trust the broadcast id — it re-fetches its own state.

**T — Tampering.** Tab modifies the broadcast payload.

- Mitigation: payload carries no authority — only a "go re-fetch" signal plus the optional target tenant id (which the receiving tab will not blindly accept).

**R — Repudiation.** Tenant switch via this path needs no new audit trail beyond slice 188's `oauth_token_exchanges` table — every actual switch already lands there because it goes through `/oauth/token`. The broadcast is post-hoc UI sync, not a switch primitive.

**I — Information disclosure.** Broadcast leaks tenant ids to other tabs.

- Mitigation: same-origin scope. The other tabs are already authenticated as the same operator; they already know the operator's `available_tenants[]` from the JWT.

**D — Denial of service.** Receiving tab loops by re-broadcasting on receive.

- Mitigation: P0 anti-criterion — the listener never broadcasts. The `onTenantSwitched` callback handles the message but the broadcast-helper does NOT have a "re-broadcast on receive" code path. Test verifies.

**E — Elevation of privilege.** Receiving tab gains a tenant it shouldn't have.

- Mitigation: receiving tab re-validates via `/api/me/tenants` which reads only the tenant ids in the JWT's `available_tenants[]`. Cannot escalate.

**Verdict:** `has-mitigations`. Same-origin scope + don't-trust-broadcast + no-rebroadcast loops are the three load-bearing controls.

## Acceptance criteria

### Broadcast helper

- **AC-1.** NEW module `web/lib/auth/tenant-broadcast.ts` exports two functions: `postTenantSwitched(targetTenantId: string): void` and `onTenantSwitched(cb: (msg: TenantSwitchedMessage) => void): () => void` (the second returns an unsubscribe).
- **AC-2.** Channel name is the literal string `"atlas-tenant"`. Unique to this app.
- **AC-3.** Both functions are graceful no-ops when `typeof BroadcastChannel === "undefined"` (SSR, older browsers). `postTenantSwitched` returns without error; `onTenantSwitched` returns a noop unsubscribe.
- **AC-4.** Message payload shape is `{ type: "tenant-switched", targetTenantId: string, ts: number }`. The `ts` is `Date.now()` at post time. Unknown message shapes are dropped by the listener.

### Component wiring

- **AC-5.** `<TenantSwitcher>` (`web/components/auth/tenant-switcher.tsx`) imports `postTenantSwitched` and calls it inside `onPick` immediately after `switchTenant()` returns `ok: true` and before the local `router.refresh()`.
- **AC-6.** `<TenantSwitcher>` subscribes via `onTenantSwitched` inside a `useEffect`. On a received message the handler triggers an immediate `/api/me/tenants` re-fetch (the existing periodic-refetch path) and then `router.refresh()`. Subscription is torn down on unmount.
- **AC-7.** Listener does NOT re-broadcast on receive (no infinite loop). The `postTenantSwitched` call only happens in `onPick` — never inside the receive handler.

### Tests

- **AC-8.** NEW test file `web/lib/auth/tenant-broadcast.test.ts` covers: (a) `postTenantSwitched` sends a message with the correct shape, (b) `onTenantSwitched` receives the message, (c) no-op when `BroadcastChannel` is undefined, (d) channel name is `"atlas-tenant"`, (e) unsubscribe stops receiving messages.
- **AC-9.** Tests use a mocked `BroadcastChannel` injected via `globalThis.BroadcastChannel`. No browser env needed — tests run under the existing node-env vitest config.
- **AC-10.** All four CI surfaces (Go unit, Go integration, Frontend vitest, Frontend Playwright) remain green. No new flake.

## Anti-criteria (P0 — block merge)

- **P0-199-1.** MUST gracefully degrade when `BroadcastChannel` is unavailable. No thrown ReferenceError on SSR. No thrown ReferenceError on older browsers.
- **P0-199-2.** Does NOT trust the cross-tab message blindly. The receiving tab MUST re-fetch its OWN JWT/tenant state from the BFF before re-rendering. The broadcast is a nudge, not an authority.
- **P0-199-3.** Channel name MUST be unique to this app (`"atlas-tenant"`). Do not pick a generic name that could collide with another product on the same origin.
- **P0-199-4.** Does NOT cause infinite loops. The listener MUST NOT re-broadcast on receive. The `onTenantSwitched` handler MUST NOT call `postTenantSwitched` directly or transitively.
- **P0-199-5.** MUST close the channel on component unmount. No leaks.

## Files changed (expected)

- NEW `web/lib/auth/tenant-broadcast.ts`
- NEW `web/lib/auth/tenant-broadcast.test.ts`
- MODIFIED `web/components/auth/tenant-switcher.tsx`

## Dependencies

- Slice 192 merged (provides `<TenantSwitcher>` and `switchTenant` — both shipped at `b0b5280`).

## Notes

- Tests run under vitest node env. We mock `BroadcastChannel` via `globalThis.BroadcastChannel = MockBC` and restore afterEach.
- The `useEffect` subscription is gated on `typeof BroadcastChannel !== "undefined"` so SSR + jsdom-less test renderings of any consumer that imports the switcher do not blow up.
