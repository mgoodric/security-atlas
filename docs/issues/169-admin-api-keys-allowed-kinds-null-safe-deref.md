# 169 — Apply slice 166 null-safe allowed_kinds helper to admin/api-keys page

**Cluster:** Quality
**Estimate:** 0.1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** Surfaced during slice 166 P0-A1 audit, captured as follow-up per continuous-batch policy.

Slice 166 fixed the production DoS in `/settings` where `c.allowed_kinds.length` threw `TypeError: Cannot read properties of null` when the backend returned `allowed_kinds: null` (the natural state of every newly-issued admin credential — pgx-go empty-array → Go nil → JSON null). The slice 166 fix landed a pure null-safe helper at `web/app/(authed)/settings/allowed-kinds-display.ts` (`isAnyKind` / `kindsLabel`) and swapped the crash site to use it.

During the slice 166 audit, the same identical bug pattern was found at `web/app/admin/api-keys/page.tsx:200`:

```tsx
{
  c.allowed_kinds.length === 0 ? (
    <span className="text-muted-foreground">any</span>
  ) : (
    c.allowed_kinds.join(", ")
  );
}
```

Same data source (`AdminCredential` list response from `GET /v1/admin/credentials`), same null-deref pattern, same React-unmount blast radius. Any admin user with a default-empty `allowed_kinds` credential cannot view the `/admin/api-keys` page either.

Slice 166 scoped its fix to "the `/settings` credentials table render path" per the slice doc + orchestrator P0-A1 ("Do NOT widen the fix beyond the `/settings` credentials table render path"). The admin/api-keys page is a sibling crash site that needs the same trivial swap.

**WHAT.** Apply the slice 166 helper to `web/app/admin/api-keys/page.tsx`. Net diff: 1 new import line + 2 substantive line changes at the render site (3 total). The helper is intentionally generic — it lives in the settings directory because that's where the canonical pattern is, but it has no settings-specific coupling.

**SCOPE DISCIPLINE — what's deliberately out:**

- Does NOT relocate the helper module from `web/app/(authed)/settings/allowed-kinds-display.ts` to a more "neutral" path. The helper's home is fine; cross-app imports are a non-issue in this repo. Moving it later (if a third consumer appears) is a separate cleanup slice.
- Does NOT add the backend non-nil marshal (Option B from slice 166 D2). That's still a valid future cleanup — making `allowed_kinds` consistently `[]` on the wire would eliminate the nil-slice→null-marshal gotcha for every consumer including CLIs and SDKs. File as future spillover when the maintainer chooses to invest the cycle.
- Does NOT change the wire shape or TypeScript type. The fix is purely defensive read coercion at the render boundary, identical to slice 166's approach (P0-A4 carryover: type stays `string[]`, null is a bug-shaped artifact, coerce on read).

## Threat model

**Spoofing.** N/A.

**Tampering.** N/A — defensive read coercion, no semantic change.

**Repudiation.** N/A.

**Information disclosure.** N/A.

**Denial of service.** This slice FIXES the second arm of the same DoS surface slice 166 closed for `/settings`. Closing the slice removes the crash from the `/admin/api-keys` render path.

**Elevation of privilege.** N/A.

## Acceptance criteria

- [ ] AC-1: `web/app/admin/api-keys/page.tsx` imports `isAnyKind` and `kindsLabel` from `web/app/(authed)/settings/allowed-kinds-display.ts`.
- [ ] AC-2: The render site at `web/app/admin/api-keys/page.tsx:200` uses `isAnyKind(c.allowed_kinds)` and `kindsLabel(c.allowed_kinds)` instead of `c.allowed_kinds.length` and `c.allowed_kinds.join(", ")`.
- [ ] AC-3: Diff is ≤ 5 substantive lines (1 import + 2-3 render-site).
- [ ] AC-4: Full vitest suite continues to pass (503/503 from slice 166 baseline).
- [ ] AC-5: Manual or scripted verification that `/admin/api-keys` renders with at least one row whose `allowed_kinds` arrives as `null` from the backend (Playwright spec, vitest assertion against a mocked BFF, or an explicit unit test of the page's data-transform layer — whichever is cheapest given the page's current test surface).

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT relocate the helper module — keep the import path pointing at `web/app/(authed)/settings/allowed-kinds-display.ts`.
- **P0-A2**: Does NOT change the wire shape of `GET /v1/admin/credentials` — TypeScript type at `web/lib/api.ts:596` stays `allowed_kinds: string[]`. P0-A4 carryover from slice 166.
- **P0-A3**: Does NOT widen the fix to other pages or other `string[]`-typed fields. If grep surfaces a third instance during this slice, file a sweep slice (170+) rather than expanding this one.

## Canvas references

- `web/app/admin/api-keys/page.tsx:200` — second crash site (same pattern as slice 166).
- `web/app/(authed)/settings/allowed-kinds-display.ts` — slice 166 helper module to import from.
- `web/app/(authed)/settings/allowed-kinds-display.test.ts` — slice 166 regression suite (covers null + undefined + [] + non-empty).
- Slice 166 (`166-settings-creds-allowed-kinds-null-crash.md`) — parent slice; surfaced this bug via the P0-A1 audit.
- Slice 166 decisions log (`docs/audit-log/166-settings-creds-allowed-kinds-null-crash-decisions.md`) — D3 documents this exact follow-on.

## Dependencies

- #166 — must be merged first (provides the helper module). This slice is trivial-but-blocked-on-166.

## Notes for the implementing agent

- The slice 166 helper file is intentionally framework-free (pure function, no React, no Tailwind). It can be imported cross-app without indirection. Just `import { isAnyKind, kindsLabel } from "@/app/(authed)/settings/allowed-kinds-display";` (or the relative path equivalent).
- The slice 166 vitest regression already covers the null / undefined / empty / non-empty cases at the helper layer. AC-5 here is about end-to-end coverage of the `/admin/api-keys` render — the helper itself is already locked.
- This slice should fit in a single 15-minute focused PR. If it's taking longer, something has drifted; STOP and report.
