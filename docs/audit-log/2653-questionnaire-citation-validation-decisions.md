# #2653 — questionnaire answer citations checked server-side: decisions log

**Issue:** #2653, from the review of #2651 (#2432). `PATCH
/v1/questionnaires/{id}/answers/{qid}` stored `citations[]` as sent: any id,
any title.

- detection_tier_actual: `manual_review`
- detection_tier_target: `integration`

> The defect (dangling, foreign or made-up citation ids, client-chosen titles)
> was found in code review of #2651. The round-trip integration test of #2432
> wrote random UUIDs and got a 200; an integration test citing an id the
> tenant does not hold would have caught it.

## D1 — where each check runs

| Check                                | Where                                         | Why                                                                                 |
| ------------------------------------ | --------------------------------------------- | ----------------------------------------------------------------------------------- |
| Shape (object, UUID id, known type)  | `questionnaire.ParseCitations`, pure Go       | No DB needed; unit-testable.                                                        |
| Caller may read the record           | `Handler.citationsReadable`, embedded OPA     | The same engine the middleware uses, asked as `GET /v1/<type>/<id>` would be.       |
| Record exists in the caller's tenant | `resolveCitations` inside `UpsertAnswer`'s tx | Under the RLS GUC, so a foreign tenant's id reads exactly as absent (invariant #6). |
| Title                                | `resolveCitations`                            | Overwritten from the record; the client's title is dropped in `ParseCitations`.     |

All refusals are one 400 with one message (`invalidCitationMsg`), so the PATCH
does not tell an id that exists in another tenant from one that exists nowhere.

## D2 — the readability check, and why the viewer test is a handler test

OPA admits questionnaire writes only to `admin` (no role file lists
`questionnaires`), and admin reads everything. So today no caller that reaches
the handler is refused by the read check: a viewer's PATCH is already a 403 at
the middleware. The check is kept because the ticket asks for it and it holds
if the write admit later widens (for example to `grc_engineer` or
`control_owner`). The viewer case is proven by calling the handler directly
with the real embedded engine (`handlers_citations_test.go`), not through the
router, because the router refuses the viewer earlier.

The middleware's resource-attribute hydration (the auditor's assigned-period
evidence, #2157) does not run on this in-handler decision, so a rule that needs
those attributes denies. That fails closed.

With no engine attached (unit-test servers), the read check passes, as those
servers skip the authz middleware too. A served binary refuses to start
without an engine (#2555).

## D3 — the evidence title

An evidence record has no title column. The server writes `<evidence_kind> ·
<observed_at UTC day>` (`Evidence` when the kind is empty), the label the web
picker shows for a listed record (`web/lib/entity-picker.ts`
`evidenceOptions`). A record picked from the server search showed `<kind> ·
<control_ref>` in the picker; after the save it carries the server's title.

## D4 — limits

At most 100 citations per answer (`MaxCitations`), so one PATCH cannot ask for
an unbounded number of OPA decisions. Existence is two `= ANY($1)` queries, one
per type.

## Not changed

- The AI draft/approve path (`qaisuggest`) writes its own citation shape and
  resolves its citations itself; it does not go through this PATCH.
- An answer saved before this change may cite an id that no longer resolves.
  Re-saving it from the editor now returns the 400 until that citation is
  removed.
