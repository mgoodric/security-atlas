# Issue tracker: GitHub

Issues and specs for this repo live as GitHub issues on `mgoodric/security-atlas`. Use the `gh` CLI for all operations.

Issues track the work, and `isa/` still says what is true when it is done
(see `AGENTS.md`, "How work lands"). A ticket never adds to a claim, weakens one
or rewords one. If a spec implies a new or changed claim, that claim lands in
`isa/` on `main`; the ticket links to it and doesn't restate it.

## Conventions

- **Create an issue**: `gh issue create --title "..." --body "..."`. Use a heredoc for multi-line bodies.
- **Read an issue**: `gh issue view <number> --comments`, filtering comments by `jq` and also fetching labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`
- **Apply / remove labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh issue close <number> --comment "..."`

Infer the repo from `git remote -v`; `gh` does this automatically when run inside a clone.

## Pull requests as a triage surface

**PRs as a request surface: no.** _(Set to `yes` if this repo treats external PRs as feature requests; `/triage` reads this flag.)_

When set to `yes`, PRs run through the same labels and states as issues, using the `gh pr` equivalents:

- **Read a PR**: `gh pr view <number> --comments` and `gh pr diff <number>` for the diff.
- **List external PRs for triage**: `gh pr list --state open --json number,title,body,labels,author,authorAssociation,comments` then keep only `authorAssociation` of `CONTRIBUTOR`, `FIRST_TIME_CONTRIBUTOR`, or `NONE` (drop `OWNER`/`MEMBER`/`COLLABORATOR`).
- **Comment / label / close**: `gh pr comment`, `gh pr edit --add-label`/`--remove-label`, `gh pr close`.

GitHub shares one number space across issues and PRs, so a bare `#42` may be either: resolve with `gh pr view 42` and fall back to `gh issue view 42`.

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is a single issue with **child** issues as tickets.

- **Map**: a single issue labelled `wayfinder:map`, holding the Notes / Decisions-so-far / Fog body. `gh issue create --label wayfinder:map`.
- **Child ticket**: an issue linked to the map as a GitHub sub-issue (`gh api` on the sub-issues endpoint). Where sub-issues aren't enabled, add the child to a task list in the map body and put `Part of #<map>` at the top of the child body. Labels: `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`). Once claimed, the ticket is assigned to the driving dev.
- **Blocking**: GitHub's **native issue dependencies**, per [ADR 0008](https://github.com/mgoodric/development/blob/main/docs/decisions/0008-cross-repo-dependency-edges-are-authored-in-github.md). Read edges only from the list endpoint, `gh api repos/<owner>/<repo>/issues/<n>/dependencies/blocked_by`; it returns `repository.full_name`, so cross-repo blockers resolve. Never use `issue_dependencies_summary`: it lags the list endpoint and reads zero for seconds after a write, so it cannot verify one. Before adding an edge, read the list; a duplicate is a hard error (`Target issue has already been taken`). Add an edge with `gh api --method POST repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the blocker's numeric **database id** (`gh api repos/<owner>/<repo>/issues/<n> --jq .id`, _not_ the `#number` or `node_id`). If a native edge can't be created, stop and report it. Don't write a `Blocked by:` line instead; a text edge means ADR 0008 needs revisiting. A ticket is unblocked when every blocker is closed.
- **Frontier query**: list the map's children from the sub-issues endpoint, `gh api --paginate repos/<owner>/<repo>/issues/<map>/sub_issues` (`gh issue list` can't filter to a map's sub-issues). Keep children whose `state` is `"open"` and whose `assignees` is empty. For each, read its `blocked_by` list from the list endpoint, as in **Blocking**, and drop it if any entry has `state` `"open"`. The first remaining child, in the order the endpoint returns them, wins. For a map without sub-issues, read the children from the map body's task list instead, apply the same filters, and take the first in task-list order.
- **Claim**: `gh issue edit <n> --add-assignee @me`, the session's first write.
- **Resolve**: `gh issue comment <n> --body "<answer>"`, then `gh issue close <n>`, then append a context pointer (gist + link) to the map's Decisions-so-far.
