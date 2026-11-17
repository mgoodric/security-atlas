# #2154 — "How this works" help for the Library section — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2154 (parent #2125)
**Shape:** five module help files under `web/help/modules/` (`policies`,
`vendors`, `personnel-security`, `questionnaires`, `scf-catalog`), registered
in `web/lib/help/modules.ts`, `userprefs.HintModules` and migration
`20261229000000`, published on the docs site. Nine new user terms. Follows
#2445 (#2152) and #2437 (#2155).

detection_tier_actual: manual_review
detection_tier_target: integration

(The one defect found is in the product, not this change: see D4.)

## D1 — one module covers both catalog routes

The Library group lists `/catalog/scf` and `/catalog/audited-under`. The
ticket names five modules, and "Audited under" only makes sense once anchors
and the crosswalk are explained, so the `scf-catalog` module lists both
routes and gives Audited under its own section. The overlay edge it adds is
the existing `org-mapping` term, so no new term duplicates it.

## D2 — permissions are written from OPA, not from the handlers

Several handlers admit more roles than the Rego bundle does (for example the
personnel-security handler passes owner-role credentials, but OPA admits only
admin and grc_engineer first). Each "Who can do this" section states what a
request can actually do, which is the OPA answer. Questionnaires are
admin-only for writes because `questionnaires` is not in `grc_engineer.rego`'s
write list.

## D3 — the policy approval rule includes draft editors

#2448 (#2446) makes everyone who edited a draft an author for the approval
rule. The help says the approver must be someone other than the submitter or
a draft editor, unless self-approval is allowed, per the dispatcher's note
that #2448 is merging. If #2448 does not land, that sentence needs trimming
to "the person who submitted it".

## D4 — acknowledgments are described without saying how to record one

The policy acknowledgment POST (`/v1/policies/{id}/acknowledge`) is a write on
the `policies` resource, which OPA admits only for admin and grc_engineer, so
viewers, control owners and auditors appear unable to acknowledge even when a
policy requires their role. The web app also has no Acknowledge button. The
help therefore explains what an acknowledgment is and how long it lasts, and
does not tell anyone to click anything. Reported to the dispatcher as a
product gap, not fixed here (out of scope for a help ticket).

## D5 — no data mocks in the Playwright spec

Each Library page renders its header, with the subtitle's `<Term>`s and the
**?**, in its loading, error and loaded states alike, and every Library
module defaults on in the seed, so `e2e/help-library.spec.ts` needs no route
mocks. Body assertions use text on one source line of the help file, because
the shared renderer still joins soft line breaks with `<br/>` (#2455).
