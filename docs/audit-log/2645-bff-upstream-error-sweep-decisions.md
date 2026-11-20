# 2645 — the remaining BFF error pass-throughs move onto `upstreamErrorMessage`: decisions log

Issue: mgoodric/security-atlas#2645, the follow-up #2433's decisions log
(D2) listed.

## Detection-tier classification

- **detection_tier_actual:** unit. The new sentinel found one site D2 did
  not list: `app/(authed)/controls/checklist/page.tsx` read `detail.error`
  from an untyped body, so the `{ error?: string }` search that built D2
  missed it.
- **detection_tier_target:** unit (a source-scan sentinel in vitest).

## Decisions

### D1 — every site was checked; all of them passed a 5xx body through

The ticket asks for each site to be checked first, since some might read
the body only on a 4xx. None did: every listed site reached the body's
`error` on any non-2xx. So all of them moved. Four listed files had no
pass-through and are unchanged:

| File                                    | Why it is not a site                                          |
| --------------------------------------- | ------------------------------------------------------------- |
| `app/(authed)/policies/policy-form.tsx` | `error?: string` is a field component's prop                  |
| `app/api/admin/me/route.ts`             | its own response type; it writes `upstream N`, not the body   |
| `app/login/page.tsx`                    | `?error=` is set only by `login/actions.ts`, to fixed strings |
| `lib/api/audit-log.ts`                  | no body read on `main` any more                               |

### D2 — the common shape was rewritten by script, the rest by hand

65 sites had the same five lines (`let msg = status line; try { j =
await res.json(); if (j.error) msg = j.error } catch {}`). A script
replaced each with `const msg = await upstreamErrorMessage(res[, fallback])`,
keeping a site's own fallback text as `generic`. The other shapes:

- **parse first, check after** (admin tenants, users, super-admins,
  module access, vendor review, control import upload): the `!res.ok` check
  now comes before the body parse, so the success body is read only on a
  2xx. The `error?: string` field came off those body types.
- **settings tenant tab**: the four `JSON.parse(await res.text())` reads
  became the helper, keeping each `rename tenant: N`-style fallback.
- **demo seed / teardown**: the message comes from `res.clone()`, so the
  dev-only `console.warn` still logs the raw body (in development only;
  it is never shown).
- **audit periods**: `code` is still read from the body; the message is
  from `res.clone()` through the helper.
- **activity**: `upstreamErrorMessage(res, "")`, since its error class adds
  the status itself.

### D3 — `upstreamErrorText` for a body already parsed

The three AI-assist view-model parsers (`parseGenerate`, `parseAISuggest`,
`parseMappingSuggest`) take `(ok, status, raw)`, not a `Response`.
`upstreamErrorText(status, body, generic)` is the same rule on a parsed
body, and `upstreamErrorMessage` now calls it, so the 4xx/5xx rule is
written once.

### D4 — one table test and one sentinel

- `lib/api/upstream-error-sites.test.ts` drives one fetcher from every
  changed `lib/api` module (and each admin, SSO and `/v1/me` writer) with a
  500 carrying a `pq:` string and with a 400 carrying a refusal. The 500's
  text must not appear; the 400's must. The three parsers get the same pair.
  Page-local helpers (`app/**`, `components/**`) are covered by the sentinel,
  not the table: they are inline in client components that the node-only
  vitest tier does not render (AGENTS.md Q-3).
- `lib/api/upstream-error-sentinel.test.ts` scans every non-test `.ts` /
  `.tsx` under `app/`, `components/`, `lib/` (not `generated/`) for the
  hand-rolled shapes: a cast to `{ error?: … }`, an `& { error?: … }`
  intersection, and `.error ?? `…status…``. The helper's own file is the
  only exemption. Each shape has a positive case so the scan cannot pass by
  matching nothing.

### D5 — four existing tests asserted the old behaviour

`controls-list.test.ts` (two) and `risks.test.ts` expected a 502's
`{error}` in the thrown message; they now use a 400 for the pass-through,
and `risks.test.ts` adds the 5xx case. `app/api/admin/demo/status/route.test.ts`
expected a 500's body text in the route's `{error}`; the route reads
through `getAdminDemoStatus`, so it now forwards `500 Internal Server Error`.

### D6 — a 503 worded for the user also shows the status line

Two Playwright specs asserted the old pass-through:

- `e2e/admin-demo.spec.ts` AC-3 mocks the platform's real 503 "demo seed
  not enabled on this deployment" (`internal/api/admindemo/handler.go:239`).
- `e2e/risks-edit-stale-2544.spec.ts` mocks a 500 "test outage".

Both now assert the status line, and that the body's text is absent.

The strict rule holds for 503 as well (decided through the dispatcher). A
503 is a server status, and exempting it would reopen the leak for any 503
handler. The cost is that four responses worded for the user lose their
wording: the two `admindemo` ones and `boardnarrative/handlers.go:83` and
`:132`. The demo page already hides the seed button when the status route
says demo is off, so the effect today is small. #2730 moves those
responses onto a 4xx with a stable code, where the wording passes through
again.

### Out of scope

The BFF route handlers under `app/api/**` still forward the platform's
status and body verbatim (`lib/api/bff.ts`). That is the proxy contract;
the text a person sees is decided where the browser reads it, which is
what this sweep covers.
