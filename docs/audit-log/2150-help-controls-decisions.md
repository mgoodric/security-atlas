# #2150 — "How this works" help for the Controls section — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2150 (parent #2125)
**Shape:** three new module help files under `web/help/modules/` (`evidence`,
`exceptions`, `controls-checklist`) and a rewrite of `controls`, registered in
`web/lib/help/modules.ts`, `userprefs.HintModules` and migration
`20261244000000`, published on the docs site. Four new user terms
(`evaluation`, `evidence-ledger`, `exception`, `implementation-checklist`);
`attestation` reused and extended. Follows #2462 (#2154).

detection_tier_actual: manual_review
detection_tier_target: integration

(The one defect found is in the product, not this change: see D5.)

## D1 — the exception diagram moves from Controls to Exceptions

The pipeline ticket (#2143/#2148) put the exception lifecycle in the Controls
help because Exceptions had no module. Exceptions now has one, and the Controls
help embeds the control state diagram (#2147) instead. One lifecycle per page
keeps each panel about its own page. The `controls-exception-*.svg` files are
removed by the renderer, and `help-panel.spec.ts`'s diagram test follows the
diagram to `/exceptions`.

## D2 — the control state diagram is drawn exactly as exported

`docs/lifecycles/control_state.json` is a complete graph: every result can
follow every other, because each evaluation is computed afresh. The Mermaid
block was generated from the JSON, not hand-drawn, so the diagram check holds by
construction. The rendered picture is dense (five states, twenty edges). The
prose lists each result and the input that produces it, and the panel's text
alternative lists every transition, so the diagram is a supporting picture,
not the only explanation.

## D3 — module ids and routes

`controls-checklist` (not `checklist`) because the route is
`/controls/checklist`; `helpModuleForPath` takes the longest route, so it wins
over `controls` there while `/controls/{id}`, `/controls/imports` and the
attest page stay on `controls`. `evidence` and `exceptions` cover their detail,
new and action pages.

## D4 — "who can" read from code, not docs

Each "who can" line was taken from the handler gates and rego:

- Attest: `cred.HasOwnerRole` (owner role or admin), `attest.go`; pass/fail,
  latest wins, future-dated refusal from #2492/#2512.
- Evaluate now: assigned owner or admin, rate-limited per person
  (`controlstate/evaluate.go`, #2491).
- Bundle download: any reader of controls; upload: admin (or a machine actor),
  `controls/handlers.go`.
- Exceptions: request needs write on `exceptions` (admin, grc_engineer);
  approve/deny/activate/revoke need `IsTenantApprover`; second person unless
  self-approval is on; deny never self-done; machines never approve.
- Checklist: generate/approve need `IsTenantApprover`; read/export also any
  credential with owner roles.

Re-evaluation on an exception change (#2498) is not on `main`, so the
Exceptions help says the control's state follows "at its evaluation", which is
true both before and after it lands.

## D5 — finding: a control_owner may be refused by OPA when attesting

The handler admits anyone holding the control's owner role, and GLOSSARY.md
says the same. But `POST /v1/controls/{id}/attestations` resolves to resource
`controls`, action `write`, and `policies/authz/control_owner.rego` grants a
control_owner `controls` write only on `/evaluate`. So a user whose only role
is `control_owner` looks to be refused before the handler runs. The help text
states the intended rule (owner role or admin). Reported to the dispatcher as a
finding; not changed here, because it is authz, outside a help ticket.
