# 2954 — atlas-confluence: exit non-zero on skipped pages and platform warnings: decisions log

Issue: mgoodric/security-atlas#2954, a follow-up from #2856 (PR #2947), with
the `warnings` / 422 scope added from #2902 (PR #2976).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — Exit 3 for "pushed, needs attention", exit 1 unchanged

Exit 1 keeps its meaning: nothing was pushed, or the platform refused the
push. A run whose push was accepted but should still page someone exits
`3` (`ExitNeedsAttention`). A separate code lets a scheduler tell "the
platform has this run's state" from "the platform has nothing new", while
any non-zero still alerts. The full report is printed before the error, so
the operator sees which pages caused it.

Two conditions trigger it:

- more pages `skipped by connector` than `--max-skipped` (default 0);
- any entry in the platform's `warnings` array (#2902). No flag relaxes
  this: the platform already applies its own threshold (more than half the
  source missing), and a second threshold in the connector would just
  disagree with it.

Default 0, because a converter refusal is a page whose policy the
platform will mark `missing`, and that needs attention every time. The
flag exists for an operator who has accepted that one page can't be
converted.

### D2 — Skipped pages stay out of the push (ticket item 2: considered, not done)

The ticket asked whether to send a converter-refused page with its
previous body, so the platform keeps it present rather than `missing`.
Not done, for three reasons:

- **The connector holds no previous body.** It keeps no state between
  runs by design: the platform is the only store. Sending "the previous
  body" would need either a new connector-side cache or a new platform
  field meaning "keep what you have".
- **Every other carrier misreports the page.** Sending the page with an
  empty `body_md` would keep it present, but the platform would report it
  `skipped reason=empty body`, which is false: the page has a body that
  the connector couldn't convert.
- **`missing` is the honest-enough outcome now that the run alerts.** The
  policy stays published and in force, gets a needs-attention item, and the
  run exits 3 with the page named `skipped by connector`. The operator's
  attention lands on the right page either way.

If keeping the page present matters later, the owning change is on the
platform: a per-page `unreadable: true` (or similar) that counts the page
as present and reports it as such. That would be a separate ticket in this
repo. None was filed here.

### D3 — 422 message surfaced verbatim from the platform's `error`

`policysync.Push` now returns a `*PushError` and, for any non-200 with a
JSON `{"error": ...}` body, quotes the platform's message instead of the
raw body. A 422 reads `platform refused an empty push (HTTP 422): <message>`.
The connector refuses an empty labelled set before pushing, so reaching
the 422 means the two guards disagree. It exits 1 like any refusal.

## Live smoke run of `content/search` (ticket item 3: not done)

Not run. This session had no Confluence Cloud site or credentials, and
fetching secrets was out of scope for the worker. The connector's
response handling is still verified only against replayed responses
shaped after Atlassian's documented `GET /wiki/rest/api/content/search`
(`results[].body.storage.value`, `results[]._links.webui`, `_links.base`,
`_links.next`). The exact command and what to look for are in
`connectors/confluence/README.md` under "Live smoke run". Record the
result here when it is done: date, site (redacted), page count, exit
status, and any shape difference found.
