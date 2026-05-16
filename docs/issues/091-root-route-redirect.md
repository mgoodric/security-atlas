# 091 — Root-route redirect (replace stock create-next-app template)

**Cluster:** Frontend / UX
**Estimate:** 0.5d
**Type:** AFK

## Narrative

`web/app/page.tsx` is the literal stock `create-next-app` template that ships from `npx create-next-app` — Next.js logo, "To get started, edit the page.tsx file", links to Vercel templates and the Next.js learning center. It was never replaced when the security-atlas web app was built out. Every signed-in user landing on `/` after auth ends up on the Vercel starter homepage with no path forward, even though real routes exist at `/dashboard`, `/audit`, `/vendors`, `/board-packs`, `/catalog/scf`, `/admin`, etc.

This breaks the first-time-user experience on every fresh deploy. It also breaks the "click the security-atlas link in my browser bookmarks" experience for returning users — bookmarks pointing at the bare hostname land them on Vercel-branded scaffolding.

The fix is a one-screen-of-code redirect: `/` resolves to `/dashboard` if the user has a valid session cookie, otherwise `/login` (with `?from=/` so post-login they end up back at the right place). No real content lives at `/` — it's bookmarkable as the "go to my workspace" URL but never renders UI itself.

## Acceptance criteria

- [ ] AC-1: `web/app/page.tsx` is replaced with a server component that calls `next/navigation`'s `redirect()` — no rendered UI, no `<html>`, no Vercel/Next.js branding remains anywhere in the file
- [ ] AC-2: For an unauthenticated request, `/` returns HTTP 307 → `/login?from=%2F` (the same pattern the existing auth middleware uses for protected routes)
- [ ] AC-3: For an authenticated request (valid `SESSION_COOKIE` per `web/lib/auth.ts`), `/` returns HTTP 307 → `/dashboard`
- [ ] AC-4: The bare path `https://atlas.home.gmoney.sh/` (or `http://localhost:3015/` for local dev) never renders the Vercel starter page under any auth state — verified via `curl -I` returning 307, not 200
- [ ] AC-5: All Next.js / Vercel marketing imports (`next/image`, Vercel hero copy, template Tailwind classes from create-next-app) that were ONLY used by the stock page are removed; the file's only imports are `next/navigation` + the existing session helper from `@/lib/auth`
- [ ] AC-6: Static assets shipped only by the stock template (`web/public/next.svg`, `web/public/vercel.svg`, `web/public/file.svg`, `web/public/globe.svg`, `web/public/window.svg` if present) are removed from `web/public/` — they were never referenced by any other page
- [ ] AC-7: New Playwright spec `web/e2e/root-redirect.spec.ts` asserts the unauthed-→login + authed-→dashboard behavior (uses the auth fixture from slice 069 if merged; otherwise inline-fixture)
- [ ] AC-8: Existing Playwright specs that previously navigated via `page.goto('/dashboard')` still work — adding the redirect at `/` does not change deep-link behavior to specific routes

## Constitutional invariants honored

- **CLAUDE.md "no Vercel/Next-template branding in shipped artifacts":** the slice removes the last instance of `next/image`-rendering-Vercel-logos in the app shell
- **Slice 034 (auth flow) cookie semantics:** the redirect logic uses the existing `SESSION_COOKIE` constant from `@/lib/auth` — does not invent a new auth check

## Canvas references

- `Plans/canvas/01-vision.md` §1.5 (acceptance criterion #7 — installable + first evidence in 4h; the redirect is part of what "first user lands somewhere usable" actually means)
- `web/app/page.tsx` (the file being replaced — current contents are stock create-next-app)
- `web/app/login/page.tsx` (the existing login route that `/` redirects unauthed users to)
- `web/app/(authed)/dashboard/page.tsx` (the existing dashboard route that `/` redirects authed users to)

## Dependencies

- #005 (merged) — Next.js scaffold under which `web/app/page.tsx` was created
- #034 (merged) — auth + session cookie this slice's redirect reads
- #040 (merged) — `/dashboard` route this slice redirects to

## Anti-criteria (P0 — block merge)

- **P0-A1:** Does NOT add any rendered content to `/`. The route is a pure redirect. Future "marketing homepage" or "tenant picker" UI is a separate slice if ever wanted — keep this one's diff at exactly one redirect call.
- **P0-A2:** Does NOT introduce a third destination (e.g. "redirect to /onboarding if first-time-user"). Two cases only — authed → `/dashboard`, unauthed → `/login`. Onboarding flows belong in their own slice.
- **P0-A3:** Does NOT change the SESSION_COOKIE name, expiry, or middleware order. The redirect READS the existing session; it does not modify auth.
- **P0-A4:** Does NOT add client-side JavaScript to perform the redirect. Server-side `redirect()` only — client-side adds a brief flash of unstyled-template content before the redirect fires.
- **P0-A5:** Does NOT leave the `web/public/next.svg`/`vercel.svg`/etc. assets in the tree "in case someone needs them later". They're dead weight in every container image; remove them.
- **P0-A6:** Does NOT touch any other `page.tsx` in the tree. Scope is exactly `web/app/page.tsx` + the corresponding asset cleanup + the new e2e spec.
- **P0-A7:** Does NOT use vendor-prefixed tokens in the new e2e spec — neutral `test-*` fixture only (same convention as the rest of the codebase per slice 05's hard rules).

## Skill mix (3–5)

- Next.js App Router server-component redirects (`next/navigation`)
- Session-cookie reads in server components (no `headers()`/`cookies()` traps)
- Playwright spec authoring (one round-trip assertion per AC)

## Notes for the implementing agent

- The reference implementation is roughly six lines:

  ```tsx
  import { redirect } from "next/navigation";
  import { cookies } from "next/headers";
  import { SESSION_COOKIE } from "@/lib/auth";

  export default async function Home() {
    const session = (await cookies()).get(SESSION_COOKIE)?.value;
    redirect(session ? "/dashboard" : "/login?from=/");
  }
  ```

  Verify the exact `SESSION_COOKIE` import path against current `web/lib/auth.ts` before committing.

- This is the smallest slice in the queue (0.5d). It should sail through grill-with-docs in one pass — there is nothing to surface beyond "yes, replace it; no, don't put marketing content here." If grill-with-docs returns design questions, the agent is over-thinking it. Ship the redirect.
- Surfaced during the 2026-05-15 deploy-walkthrough session at `~/.claude/MEMORY/WORK/20260514-064726_security-atlas-unraid-deploy/` — the user landed on the stock template after pasting a bearer token at login. Recommended capture for the continuous-batch loop's spillover-as-slice convention (`Plans/prompts/07-continuous-batch-loop.md` Amendment 2).
