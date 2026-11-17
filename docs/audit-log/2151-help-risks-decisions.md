# #2151 — "How this works" help for the Risks section — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2151 (parent #2125)
**Shape:** three module help files under `web/help/modules/` (`risks`,
`risks-hierarchy`, `action-plans`), registered in `web/lib/help/modules.ts`,
`userprefs.HintModules` and migration `20261249000000`, published on the docs
site. Five new user terms (`action-plan`, `fair`, `org-unit`, `theme`,
`decision`); `verified` rewritten and `completed` linked to the glossary.
Follows #2527 (#2150).

detection_tier_actual: manual_review
detection_tier_target: integration

(The defects found are in the product or in an existing term, not this
change: see D5 and D6.)

## D1 — module ids and routes

`risks` covers `/risks` and every page under it except `/risks/hierarchy`,
which `risks-hierarchy` claims as the longer route. Risk taxonomy
(`/risks/taxonomy`) is a Risks sidebar route without a module of its own in
the ticket's list, so it stays on the register help, which describes the
taxonomy in one line and lists `theme` among its terms. `action-plans` covers
its list, new, detail and edit pages.

The help-panel test for "a page with no help module" used `/risks`; it moves
to `/calendar`, which still has none (both the vitest and the Playwright
case).

## D2 — two diagrams on the register

The register embeds both risk lifecycles, `risk_treatment` and
`risk_status`, because both are decided on a risk's own pages and the
register help covers those pages. The hierarchy has no lifecycle of its own.
Every Mermaid block was generated from `docs/lifecycles/*.json`, not drawn
by hand, so the diagram check holds by construction.

## D3 — the action plan diagram and the system verify

`action_plan.json` declares `completed → verified` as a person's `verify`
and `verified → completed` as the system's `unverify`. The system's own
`completed → verified` move after a control evaluation (#2109) is not a row
in the table, so the diagram cannot show it; the prose explains it, from
GLOSSARY.md's "Action plan: Completed and Verified".

## D4 — "who can" read from code, not docs

- Risk create, edit, treatment, close, reopen, propose: `risks` write on
  admin and grc_engineer (`grc_engineer.rego`), as `risk_treatment.json` and
  `risk_status.json` declare.
- Approve, reject, revoke acceptance: `authz.IsTenantApprover` in
  `internal/api/risks/transitions.go`; second person unless self-approval,
  people only on approve (`requires_approver`, `second_person`,
  `people_only` in the JSON); grc_engineer revoke admit from #2105.
- Hard delete: admin only (`risk_hard_delete` in `grc_engineer.rego`,
  `IsTenantAdmin` in `DeleteRisk`).
- Taxonomy writes: `requireTaxonomyAdmin` (`IsTenantApprover`).
- Action plans: OPA admits only admin on `action-plans` writes; manual
  verify is `IsTenantAdmin`, never the owner, only plans with no linked
  controls (`VerifyActionPlan`).
- Saved views (#2133) have not landed, so the help does not mention them.
  The edit protections (#2311) landed while this was in CI: the register
  help now describes the "changed since you opened it" reload and the
  methodology-reset note, in the edit form's own words.

## D5 — finding: a viewer is admitted by OPA to the register but refused by the handler

`viewer.rego` lists `risks` among a viewer's readable resources, but the
risks and decisions handlers' `hasProgramRead` admits only admin, the
approver flag or a credential with owner roles. A plain viewer looks to pass
OPA and then get 403 on the list. The help says only that the hierarchy is
readable by "anyone who can read the register", and lists writers
precisely. Reported to the dispatcher; not changed here, because it is
authz, outside a help ticket.

## D6 — the old Verified term was wrong

`verified` said "A verified plan is final" and "to restart a verified plan,
create a new one". Since #2109 a verified plan with a linked control that
stops passing returns to completed by itself. The term now says so, and
links both Completed and Verified to the GLOSSARY.md entry.

## D7 — the theme heatmap's missing counts are stated

The hierarchy's theme heatmap renders its axes but no per-cell counts (no
aggregation endpoint on main; the panel says so). The help states this
rather than describing counts the page does not show.
