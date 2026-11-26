# #2592 — "How this works" help for the auditor workspace and requirement detail: decisions log

detection_tier_actual: none
detection_tier_target: none

Ticket: https://github.com/mgoodric/security-atlas/issues/2592 (parent
#2125). Follows the Program help (#2149, #2582) for front matter, `<Term>`
placement, hint migration and Playwright shape, and empties the
`HELP_PENDING` list #2156 left for this ticket.

## D1 — Two modules

`audit-workspace` claims `/audit`; a control's page (`/audit/{controlId}`)
falls under it through the route prefix, as a metric's page falls under
`metrics`. The lookup matches whole segments, so `/audit` covers neither
`/audits` (the `audits` module) nor `/audit-log` (exempt, a redirect);
`modules.test.ts` pins both. `requirements` claims `/requirements`, which
covers `/requirements/{id}`, the only page under it.

The audit workspace help is for the auditor and the team they work with,
and the audit-period help (`audits.md`) is for the admin who sets the
period up. They overlap on freezing, populations and samples; each says it
from its reader's side rather than one linking to the other for the
concept.

## D2 — The workspace carries its own **?**

The **?** and the first-visit hint live in `PageShell`. The workspace
renders `PageShell` only in its "No audit period assigned" and error
states; with a period it renders `AuditShell`, a full-bleed two-pane
layout with no page header. So `AuditShell` opens its own `HelpScope`, the
period bar (the workspace's header) renders `<HelpButton />` at its right
end, and `<OnboardingHint />` sits under the bar in a wrapper that
collapses when the hint renders nothing. No new component: the same
scope, button and hint `PageShell` uses.

## D3 — No lifecycle diagrams

Neither page owns a lifecycle in `docs/lifecycles/`. The audit period's
lifecycle is already drawn on the `audits` help. Both panels are asserted
to show none.

## D4 — Who can do what, and where each claim comes from

| Claim in the help                                                                      | Code path                                                                                                                                                                                                                          |
| -------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| The workspace opens on the most recently started assigned period; others see no period | `internal/api/me/audit_period.go` `AuditPeriod` (rows ordered `period_start DESC`, 404 when none); `web/app/audit/page.tsx` `NoPeriodState`                                                                                        |
| An assignment is made by an admin and requires the auditor role                        | existing `assigned-auditor` term and `audits.md` (#2118, #2239)                                                                                                                                                                    |
| Assigned auditors draw samples and record results in their own period                  | `policies/authz/auditor.rego` (`samples` write with `auditor_period_matches`); `policies/authz/audit_periods.rego` (`samples` / `populations` read)                                                                                |
| Admins and GRC engineers draw samples, record results and build populations            | `policies/authz/grc_engineer.rego` `grc_writable_resources` (`samples`, `populations`); admin's wildcard in `admin.rego`                                                                                                           |
| Auditors cannot build a population                                                     | `policies/authz/auditor.rego` and `audit_periods.rego`: no `populations` write rule for the auditor role, so default deny                                                                                                          |
| Walkthroughs: admins and GRC engineers record; auditors read                           | `internal/api/walkthroughs/handlers.go` `canWrite` = `authz.IsTenantApprover`; `policies/authz/auditor.rego` (`walkthroughs` read only). `control_owner.rego` admits a write, but the handler refuses it, so the help omits owners |
| Auditors write private or shared notes in their own period                             | `policies/authz/auditor.rego` (`audit-notes` write with `auditor_period_matches`); `internal/api/auditnotes/handlers.go` (`visibility` `auditor_only` / `shared`)                                                                  |
| GRC engineers reply on shared notes and never see private ones                         | `policies/authz/grc_engineer.rego` (`audit-notes` comment); the thread query filters `visibility = 'shared' OR author_user_id = caller` (`auditnotes/handlers.go` thread doc)                                                      |
| A half-written result survives switching tabs and controls                             | `web/components/audit/audit-shell.tsx` (`AnnotationDraftProvider`), `control-workspace.tsx` (CSS-toggled tabs)                                                                                                                     |
| Everyone reads a requirement's page                                                    | `policies/authz/defaults.rego` `catalog_resources` (`requirements` read for every role)                                                                                                                                            |
| Coverage formula and band cut points                                                   | `internal/api/ucfcoverage/rollup.go` (`rollupCoverageStrength`, `bandWeakCeil` 0.5, `bandPartialCeil` 0.8, `BandExcluded`)                                                                                                         |
| Org mappings count toward your coverage only, and an org-only control is marked        | `web/app/(authed)/requirements/[id]/page.tsx` (`isOrgMapping`, `orgOnly`)                                                                                                                                                          |
| Admins and GRC engineers add or retire an org mapping, from the Audited under report   | `internal/api/mappingoverlays/handlers.go` `requireOverlayWrite` = `authz.IsTenantApprover`; `web/app/(authed)/catalog/audited-under/page.tsx`                                                                                     |
| No one changes a catalog mapping from this page                                        | `policies/authz/grc_engineer.rego` (#1797 comment: "the catalog edges stay read-only"); the page has no write affordance                                                                                                           |

## D5 — Terms

Five new user terms: `evidence-horizon`, `walkthrough`, `requirement`,
`coverage-strength`, `confidence-band`. The modules' other terms already
existed (`assigned-auditor`, `population`, `sample`, `anchor`, `strm`,
`org-mapping`).

The requirement page's subtitle gains a line naming the requirement, SCF
anchors and coverage strength, rendered in every fetch state so the
Playwright check does not depend on data; the Coverage card's description
names the confidence band. On the workspace, the period bar names the
evidence horizon in both its open and frozen forms, and the "No audit
period assigned" text names the assigned auditor.

## D6 — The "no help module" checks move to an unmatched URL

Every authenticated route now has help, so no real page is left without a
**?**. `help-panel.spec.ts` uses `/no-such-page`, whose 404 renders inside
`PageShell` (`not-found-panel.tsx`) and so exercises the same "no module,
no button" branch; `modules.test.ts` checks the same path.

## D7 — Hints

The two ids join `userprefs.HintModules`, and migration
`20261266000000_onboarding_hint_audit_workspace_requirements_modules.sql`
widens the dismissal CHECK, so the three lists stay in step
(`modules.test.ts` pins this).
