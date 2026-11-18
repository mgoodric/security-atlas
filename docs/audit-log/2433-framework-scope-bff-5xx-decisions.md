# 2433 — Framework-scope BFF: generic text for a 5xx; self-approval refusal e2e; axe on the Activate panel: decisions log

Issue: mgoodric/security-atlas#2433, a follow-up from the review of #2419
(#2115).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced: the 5xx pass-through is
  latent (every current 5xx goes through `httperr.WriteInternal`, whose text
  is generic), and the new e2e and axe sweep found nothing at write time.
- **detection_tier_target:** unit.

## Decisions

### D1 — one helper, `upstreamErrorMessage`, in `web/lib/api/_shared.ts`

`upstreamErrorMessage(res, generic?)` returns the text an error response
shows. A 4xx `{error}` string passes through (the approval rule, a frozen
revision, "reason is required" are written for the user). A 5xx returns
`generic`, the status line by default, whatever its body says. A non-string
`error` is ignored.

It replaces the inline parse in four places, all on the framework-scope path
or shared by it:

| Site                                                                    | Before                   | Now                                  |
| ----------------------------------------------------------------------- | ------------------------ | ------------------------------------ |
| `lib/api/framework-scopes.ts` `upstreamError` (server side)             | `{error}` for any status | helper                               |
| `app/(authed)/framework-scopes/wizard.ts` `errorMessage` (browser side) | `{error}` for any status | helper                               |
| `lib/api/_shared.ts` `bffControlFetch`                                  | `{error}` for any status | helper                               |
| `lib/api/_shared.ts` `bffPostJSON`                                      | `{error}` for any status | helper, generic `request failed (N)` |

`bffControlFetch` and `bffPostJSON` are shared by 14 domain modules and three
pages, so those get the rule without further change. The ticket names
`framework-scopes.ts`; the wizard's `errorMessage` is what the scope page and
the wizard call in the browser, so leaving it would have left the leak the
ticket describes.

The status line, not a sentence, is the generic text: it is what the ticket
asks for, and what these helpers already showed for a non-JSON body, so the
pages' error states need no new copy.

### D2 — the other hand-rolled pass-throughs are listed, not changed

The same `{error}`-for-any-status parse is hand-rolled in about 45 more
places. Moving them all is a cross-cutting refactor well outside a
framework-scope follow-up; each should move onto `upstreamErrorMessage` with
its own vitest. They are safe today for the reason in the classification
above. The sites (files with an `{ error?: string }` read, tests excluded):

- `lib/api/`: `action-plans.ts`, `activity.ts`, `admin.ts`, `approvals.ts`,
  `audit-log.ts`, `audit-periods.ts`, `audit.ts`, `audited-under.ts`,
  `board.ts`, `calendar.ts`, `control-actions.ts`, `controls-bulk.ts`,
  `controls-list.ts`, `evidence.ts`, `exceptions.ts`, `me.ts`, `metrics.ts`,
  `oscal-components.ts`, `personnel-security.ts`, `policies.ts`,
  `posture-targets.ts`, `risks.ts`, `saved-views.ts`, `scope-dimensions.ts`,
  `taxonomy.ts`
- `lib/`: `board/narrative-ai.ts`, `questionnaire/ai-suggest.ts`,
  `questionnaire/mapping-suggest.ts`
- `app/`: `(authed)/audits/new/actions.ts`,
  `(authed)/controls/imports/page.tsx`,
  `(authed)/oscal/component-definitions/[id]/page.tsx`,
  `(authed)/policies/policy-form.tsx`, `(authed)/questionnaires/[id]/page.tsx`,
  `(authed)/questionnaires/page.tsx`, `(authed)/risks/new/actions.ts`,
  `(authed)/settings/_tabs/tenant.tsx`,
  `(authed)/vendors/[id]/reviews/new/page.tsx`, `admin/demo/demo-controls.tsx`,
  `admin/sso/page.tsx`, `admin/super-admins/page.tsx`,
  `admin/tenants/page.tsx`, `admin/users/page.tsx`, `api/admin/me/route.ts`,
  `login/page.tsx`

Not every one passes a 5xx body through (some read `error` only on a 4xx),
so the follow-up starts by checking each.

### D3 — the self-approval e2e has a tenant of its own

The demo tenant leaves Allow self-approval on for the seeded scope specs, so
the refusal needs another tenant.
`fixtures/e2e/framework-scope-self-approval.sql` seeds tenant `…2433` with
the setting off (set again each run), two tenant admins and a synthetic
framework version. Every id carries `2433`: `users.id` is global, and a user
id shared with another fixture would move that user between tenants (#2617).

Both people sign in with tenant-role JWTs minted with `super_admin: false`,
so the approve route's tenant-role gate (#2428) is on the path rather than
bypassed by a super-admin bearer.

The requester creates and submits the scope through the platform API. The
wizard is `framework-scope-wizard.spec.ts`'s subject; this spec's subject is
the refusal and the second person's approval on the scope page.

### D4 — the Activate-panel axe sweep rides on the same spec

The second person opens the Activate panel, axe runs with it open
(`expectNoSeriousAxe`, the #2613 pattern), then Confirm. It runs against the
real page, so the panel's markup is the shipped one, not a mock's.
