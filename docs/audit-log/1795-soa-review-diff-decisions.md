# Applicability diff on the review screen — decisions log (#1795)

A slice of the Statement of Applicability (#1744), after the SoA tab
(#1794). The approver of a framework-scope revision in review sees what it
changes against the activated revision instead of the whole decision list.

detection_tier_actual: none
detection_tier_target: none

## Decisions

**D1: One read route on the revision.** `GET
/v1/framework-scopes/{id}/applicability/diff` returns the revision, its
`baseline` (the activated revision of the same framework version), the
predicate before and after with a `changed` flag, and `changes[]`. It is a
read under the same tenant transaction and RLS as the decision list, so any
role that can read framework scopes (approvers and auditors included) can
call it, and another tenant's id is a 404.

**D2: The baseline is the activated revision, whatever state the revision
is in.** The ticket names a revision in review, but the route answers for
any state: a draft can preview its diff the same way. When the revision is
itself the activated one, or nothing is activated, `baseline` is null and
every decision on the revision is `added`; `predicate.changed` is false
because there is nothing to compare.

**D3: Four change kinds, one row per requirement.** `added` (undecided ->
decided), `removed` (decided -> undecided), `decision_changed`,
`justification_changed`. A requirement whose decision and justification
both changed is one `decision_changed` row carrying both sides. Undecided on
both sides is not a change.

**D4: The implementation reference alone is not a change.** The ticket
lists decision and justification; the implementation reference is a
pointer to where the control lives, not part of what the approver signs. An
edit to it alone does not appear in the diff. Both sides of every listed
change still carry it.

**D5: The predicate diff is before and after, not a structural diff.** The
canonical predicates are small JSON trees; the screen shows both side by
side when the hash differs. A structural diff is a later refinement if
predicates grow.

**D6: The panel sits under the scopes table.** Each revision in review gets
a "Changes in review" card on the Scopes tab, under the table that holds the
Approve and Activate buttons, so the approver reads the diff and acts on the
same screen.

**D7: Folded-in review notes from #2206.** The SoA import BFF route refuses
a request declaring more than the platform's 4 MiB cap with a 413 before
buffering it, and the SoA filter buttons are `aria-pressed` toggles in a
`group` rather than tabs.

## Removal

No API removes a decision, so the `removed` kind is reachable only through a
direct table write today. It is covered by the integration test (which
deletes the row while the revision is a draft) rather than the Playwright
spec.
