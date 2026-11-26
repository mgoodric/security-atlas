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
  break: not on an ATX heading, not before a blank line or at the end of
  the body, and not before a line that starts a new block (a heading,
  blockquote, fence, thematic break, setext underline, or a list item
  that may interrupt a paragraph; #3014 below narrows this). A converter that
  pads every line, such as the #2910 integration fixture's `reflow`,
  therefore still reads as unchanged. A run that ends in a tab or NBSP is
  not a hard break and is stripped as before.

Accepted limitations: the parse is line-local, not a CommonMark parser.
A fence nested in a list item or a blockquote is not recognised as a
fence, and trailing spaces on a table row or before an HTML block are
kept as a hard break. Both err toward reporting drift rather than hiding
it.

- **detection_tier_actual:** integration. The first head treated every
  trailing two-space run before a non-blank line as a break, and
  `TestSync2910_SameVersionWhitespaceIsUnchanged` (shard B2) caught the
  padding before list items reported as drift.
- **detection_tier_target:** unit. `TestBodyDriftedConverterReflow` now
  pins that input without Postgres.

#### D6 amendment — the block-start rule follows CommonMark (#3014)

Follow-up from PR #3010. The first block-start rule was broader than
CommonMark, so in four cases a real `<br>` change was normalised away.
The rule now carries a small line state (is the last line paragraph
text, and the content indent of the list item it sits in, if known) and
applies CommonMark's interruption rules:

- An ordered item interrupts a paragraph only when it starts at 1
  (`1.`, `01)`). `text  \n2. foo` is a lazy continuation and keeps the
  break. After a list item's own line, a less-indented ordered item at
  any number starts a sibling item, so padded numbered lists still read
  as unchanged.
- An empty item (`*`, `+`, `1.`) never interrupts a paragraph; it ends
  one only as a sibling item outside the paragraph's list item. A lone
  `-` stays a setext underline.
- A `=` line ends a paragraph only when the whole line is `=` and the
  paragraph's containers still match; `text  \n=foo` keeps the break,
  and so does `- item  \n===`, where the `===` is lazy text. `--` after a
  list item is lazy text too, while `---` is a thematic break anywhere.
  `___` is now recognised as a thematic break.
- Trailing spaces on a setext underline or thematic break are no longer
  kept, since only paragraph text carries a break.

Where the state is unknown (a dedent out of a nested item, a paragraph
after a blank line inside an item), every choice resolves toward keeping
the break, so the comparison errs toward reporting drift.

Accepted limitations, left out of #3014:

- **Indented code blocks are not compared verbatim.** Trailing
  whitespace and blank-line runs inside a four-space indented code block
  are still normalised, so a change to only those is not reported.
  Telling indented code from an indented list continuation needs the
  full container parse; the converters emit fenced code, so this is
  recorded rather than built.
- **A `>` line always ends the paragraph before it.** Inside a
  blockquote, `> a  \n> b` renders a break that is normalised away. The
  rule does not strip quote markers to compare the quoted paragraph.

- **detection_tier_actual:** manual_review. The reviewer of PR #3010
  found the four cases by reading the rule against the spec.
- **detection_tier_target:** unit. `TestNormalizeDriftBody` and
  `TestBodyDriftedBlockStart` pin each case; every new case failed
  against the previous rule.
