# Slice 397 — decisions log

**Slice:** 397 — rename `SESSION_COOKIE` → `ATLAS_JWT_COOKIE` (closes slice 328 M-3)
**Type:** JUDGMENT
**Date:** 2026-05-30

This is a JUDGMENT slice: Claude makes the subjective build-time calls (rename
scope, whether the wire value changes, the golden-tier-test touch) and records
them here rather than blocking the merge on a human sign-off. The product
runtime AI-assist boundary is unrelated and untouched.

## Context

Slice 328's code-review audit raised finding **M-3**: the BFF cookie constant
exported from `web/lib/auth.ts` is named `SESSION_COOKIE` but, since slice 206,
resolves to the value `"atlas_jwt"` — the symbol name is misleading (it reads
like a generic session cookie; it is in fact the JWT bearer cookie). The audit
explicitly noted "the underlying behavior is correct, only the symbol name is
misleading." The rename was punted twice (328 §D4 → bundle into 370; 370 D2 →
defer to 395) and finally split into this dedicated slice because an _atomic_
rename must touch two golden-tier contract tests, which slice 395's brief
forbade disturbing.

## Decisions

### D1 — Target name: `ATLAS_JWT_COOKIE` (as the slice doc / source comment specify)

The slice doc title, AC-1, and the pre-existing self-acknowledging comment at
`web/lib/auth.ts:10-14` ("A follow-on cleanup slice may rename the symbol to
`ATLAS_JWT_COOKIE`") all name the target `ATLAS_JWT_COOKIE`. No alternative
considered — adopted verbatim.

### D2 — Pure symbol rename; the cookie VALUE and all wire behavior are UNCHANGED (P0-397-1)

The exported value stays `export const ATLAS_JWT_COOKIE = "atlas_jwt";`. The
slice doc is unambiguous that this is naming-consistency only, and slice 328 M-3
itself says the behavior is correct. I did **not** change the wire cookie name
or value. Verified the three load-bearing auth files
(`web/lib/api/bff.ts`, `web/proxy.ts`, `web/app/login/actions.ts`) have
identifier-only diffs — every `cookies().get/set/delete(...)` call and the
`Authorization: Bearer` forward operate on the same `"atlas_jwt"` value as
before. No auth/cookie bug surfaced; nothing to spill over.

### D3 — Word-boundary-aware codemod; `OIDC_SESSION_COOKIE` is NOT touched (P0-397-2)

`OIDC_SESSION_COOKIE` is a _distinct_ cookie (value `"atlas_session"`, the
slice 034 OIDC session id, forwarded only on `/api/me/sessions*`). It contains
`SESSION_COOKIE` as a substring, so the rename had to be word-boundary-aware.

I proved before applying that `\bSESSION_COOKIE\b` does **not** match inside
`OIDC_SESSION_COOKIE` — the character preceding `SESSION` there is `_`, a word
character, so there is no `\b` boundary at that position (test fixture:
`OIDC_SESSION_COOKIE` untouched, standalone `SESSION_COOKIE` and
`foo SESSION_COOKIE bar` matched, `XSESSION_COOKIE` not matched). The codemod
was `perl -i -pe 's/\bSESSION_COOKIE\b/ATLAS_JWT_COOKIE/g'` applied per-file to
the 127 files containing a bare match. Post-rename verification:
`OIDC_SESSION_COOKIE` remains at all **38** occurrences; **zero** bare
`SESSION_COOKIE` remain (grep-clean excluding OIDC).

### D4 — Golden-tier contract-test touch is acceptable (JUDGMENT call; P0-397-3 honored)

The slice doc's JUDGMENT note: the golden-tier no-touch convention protects the
recorded contract _shapes_ (`*.golden.json`) and assertion semantics, not the
local identifier a test uses to set a cookie in the harness. I confirmed this
empirically: the only two `web/lib/contracts/` files modified are
`me.contract.test.ts` and `demo-status.contract.test.ts`, each a single
`cookieStore.set(SESSION_COOKIE, …)` → `cookieStore.set(ATLAS_JWT_COOKIE, …)`
identifier change. **No `*.golden.json` was touched** (verified via
`git status web/lib/contracts/` — the four `.golden.json` files are unmodified)
and both contract tests pass green in vitest. This is the mechanical-local-edit
kind of touch the slice doc sanctions, not a contract-semantics change. Per
P0-397-3 the PR does **not** auto-merge — the golden-tier touch is flagged for
review in the PR body.

### D5 — Renamed the standalone local const in `capture-readme-screenshots.ts` too

`web/scripts/capture-readme-screenshots.ts` declares its _own_ local
`const SESSION_COOKIE = "atlas_jwt";` (not imported from `@/lib/auth`, because
esbuild bundles the script in isolation) with a comment "Must match
`SESSION_COOKIE` in web/lib/auth.ts." AC-2 requires zero bare `SESSION_COOKIE`
references anywhere under `web/`, and the symbol's whole purpose is to mirror
the lib constant, so I renamed it (const + usage + comment) for consistency.

### D6 — Rewrote the now-stale rename-intent comment in `web/lib/auth.ts`

The comment block at lines 10-14 described the _old_ deliberate decision to keep
the name `SESSION_COOKIE` and forward-referenced "a follow-on cleanup slice may
rename the symbol to `ATLAS_JWT_COOKIE`". Post-rename the mechanical codemod
left that block self-contradictory ("The constant NAME stays
`ATLAS_JWT_COOKIE`… may rename the symbol to `ATLAS_JWT_COOKIE`"). I rewrote it
to state the rename is complete (slice 397, closes slice 328 M-3, pure symbol
rename, value unchanged). This is the one human-judgment edit beyond the
mechanical codemod.

### D7 — Playwright e2e left to CI (not run locally)

The Playwright suite mints a JWT against a running atlas Go server via the
env-gated `POST /v1/test/issue-jwt` endpoint and exercises the full stack
(atlas + Postgres + NATS/MinIO). Standing up that complete bring-up locally
would not faithfully mirror the purpose-built CI job. Because the change is
compile-time-only with a byte-identical resolved cookie value, the Playwright
behavior cannot differ from a pure identifier rename. Local verification that
covers the e2e surface: `tsc --noEmit` typechecks the e2e specs (they import
`ATLAS_JWT_COOKIE`), and the full `next build` compiles every BFF route +
middleware + module the specs import. The authoritative Playwright run executes
in CI on this PR.

## Verification summary

| Check                                  | Result                                    |
| -------------------------------------- | ----------------------------------------- |
| Zero bare `SESSION_COOKIE` (excl OIDC) | PASS (grep-clean)                         |
| `OIDC_SESSION_COOKIE` intact           | PASS (38 occurrences)                     |
| Cookie value unchanged                 | PASS (`"atlas_jwt"` / `"atlas_session"`)  |
| `tsc --noEmit`                         | PASS (clean)                              |
| `npm run lint`                         | PASS (0 errors; 2 pre-existing warnings)  |
| `npm run test` (vitest)                | PASS (1204/1204, 122 files)               |
| `next build`                           | PASS                                      |
| Playwright e2e                         | deferred to CI (compile-time-only change) |

## Spillover

None. The rename was fully mechanical and surfaced no genuine auth/cookie bug.
