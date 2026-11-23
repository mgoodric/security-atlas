# 2910 — Policy sync: whitespace-normalised drift comparison: decisions log

Issue: mgoodric/security-atlas#2910, a follow-up from #2854 (PR #2908),
whose D4 predicted that in-place takeovers would mostly report drift on
conversion whitespace.

## Detection-tier classification

- **detection_tier_actual:** integration. The first run of the new API
  tests expected the recorded hash and the stored body to be of the body
  exactly as posted. The endpoint trims the body at parse
  (`importfile.FromPage`), so the tests were corrected. The code was
  right.
- **detection_tier_target:** integration.

## Decisions

### D1 — the normalisation, and only for the comparison

Both sides are normalised as follows: CRLF and bare CR become `\n`,
trailing whitespace (`unicode.IsSpace`, which includes NBSP) is stripped
from every line, runs of blank lines collapse to one, and trailing
newlines are trimmed. Leading indentation and spacing inside a line are
kept, because in Markdown they can change meaning (code blocks, nested
lists). A paragraph re-wrapped onto two lines also counts as a change:
rewrapping is something a reader can see, so a converter that reflows
paragraphs will still report drift. `synced_body_sha256`,
`sync_drift_body_sha256` and every audit hash remain the sha256 of the
body as sent.

### D2 — compare bodies, not hashes

A normalised comparison needs the stored text, so the same-version
path now compares `cur.body_md` with the page body instead of comparing
`synced_body_sha256` with the page hash. On every synced row
`synced_body_sha256` is the hash of `body_md`: a synced insert writes
both from the same text, and an in-place takeover sets it to the imported
body's hash. The two checks therefore agree wherever the old one ran.

### D3 — the drifted page hash gets its own audit column

`policy_audit_log.body_sha256` means the hash of the policy row's own
body on every action. An in-place takeover leaves that body unchanged, so
the page's hash goes in a new nullable `sync_drift_body_sha256` column.
It is set only when the takeover drifted. A CHECK allows it only on
`sync_takeover` rows and only as a hex sha256. The down migration refuses
to run while any row holds one, as the other audit migrations do. This
was chosen over reusing `body_sha256` for the page hash, which would have
given that column a different meaning for this one action. (Dispatcher
call on #2910, option A.)

### D4 — the chained takeover's state change

On a chained takeover, the `sync_takeover` row on the imported policy
records `previous_state` = the imported row's status before (`published`)
and `new_state` = its status after `SupersedeImportedPolicy`
(`superseded`). Both values are read from the rows rather than
hard-coded. The in-place row leaves both null, because its status does
not change.

### D5 — the `FOR UPDATE … LIMIT 1` comment was already gone

The ticket's third item, the comment on `GetImportedPolicyForTakeover`,
no longer applies on `main`. That query was replaced by
`ListImportedPoliciesForTakeover`, which has no `LIMIT`, and its comment
already describes what a concurrent run sees. Nothing was changed.

### D6 — code fences and hard breaks are content (#2984)

Follow-up from PR #2979. Two cases D1 normalised away change what a
reader sees, so the comparison now keeps them:

- **Fenced code blocks.** Between an opening fence (up to three spaces of
  indent, then three or more backticks or tildes) and its closing fence
  (the same character, at least as many, nothing after but whitespace),
  lines are compared verbatim: trailing whitespace stays and blank-line
  runs are not collapsed. A fence left unclosed runs to the end of the
  body, as in CommonMark. Line endings are still normalised inside a
  fence, and trailing newlines at the very end of the body are still
  trimmed.
- **Trailing two-space hard breaks.** A line ending in two or more spaces
  (spaces only) is kept with exactly two, since any run of two or more
  renders the same `<br>`. It is kept only where CommonMark renders a
  break: not on an ATX heading, and not before a blank line or at the end
  of the body. A run that ends in a tab or NBSP is not a hard break and
  is stripped as before.

Accepted limitations: the parse is line-local, not a CommonMark parser.
A fence nested in a list item or a blockquote is not recognised as a
fence, and trailing spaces on a setext underline, a table
row or a thematic break are kept as a hard break. Both err toward
reporting drift rather than hiding it.

- **detection_tier_actual:** none.
- **detection_tier_target:** unit.
