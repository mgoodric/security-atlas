# Implementation-reference changes on the review diff — decisions log (#2214)

Follow-up to the applicability diff (#1795, #2212). It revisits that log's
D4, which left an edit to only the implementation reference off the diff.

detection_tier_actual: none
detection_tier_target: none

## Decisions

**D1: A separate list, not a fifth kind in `changes[]`.** The diff body gains
`reference_changes[]`. Its rows have the same shape as `changes[]` rows,
with `change: "reference_changed"` and both sides. `changes[]` and the four
kinds' counts are unchanged, so existing readers of `changes.length` or the
per-kind badges see what they saw before. A reference-only row is a requirement
decided the same way on both sides, with the same justification, and a
different `implementation_ref`. When the decision or justification also
changed, the row stays in `changes[]` (both sides already carry the reference)
and is not listed a second time.

**D2: Muted, informational, plain text.** The card lists each reference
change below the change table as a muted line, "reference changed: before ->
after", with "(none)" for an empty side. It has no badge and no count. The
reference is org-supplied, so it is rendered as React text and never
auto-linked. If a safe-link component lands later, it can be adopted here.

**D3: A superseded revision is diffed against the current activated revision.**
The baseline is always the revision activated now, whatever the state of the
revision asked about. Asking for the diff of a superseded revision therefore
compares it against its successor, not against the revision it replaced. This
is documented in the route summary (OpenAPI) and on the handler, and is not
returned as `baseline: null`. The comparison is still well defined and is the
one an approver of a rollback would want. An integration assertion pins it.
