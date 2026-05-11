# 02 — GitHub Issues Sync (optional)

Push the 49 markdown issues to GitHub Issues on `mgoodric/security-atlas` for visible status, labels, milestones, and comments. **Skip until you have collaborators or want a public-tracker substrate** — for solo work, markdown alone is enough.

## Prompt

```
Sync docs/issues/*.md to GitHub Issues on mgoodric/security-atlas (private) using the GitHub MCP.

For each docs/issues/NNN-<slug>.md file:
- Create one GitHub Issue
- Title: H1 of the file, stripped of leading "NNN — "
- Body: narrative + acceptance criteria + anti-criteria + canvas references + dependencies (link to other issues by number once those issues exist)
- Labels: cluster (spine|catalog|audit|board|control-as-code|evidence-pipeline|scope|risk|policies|vendor|auth|infra|frontend|connectors) + mode (hitl|afk) + estimate (s ≤ 1d | m 1–2d | l 2–3d)
- Milestone: "v1" (create if not present)
- Footer: "Canonical spec: docs/issues/NNN-<slug>.md"

After creation, update _INDEX.md's Status column to "Open · gh#NNN" with link.

Source-of-truth contract:
- docs/issues/*.md remains canonical for acceptance criteria + architectural decisions
- GitHub Issues is the tracker for status, assignees, comments, milestones
- AC changes: edit the markdown, then re-sync the issue body

Report-back gate (BEFORE creating any issues):
1. Total issues to create (expect 49)
2. Label set inventory (cluster × mode × estimate)
3. Markdown files that don't parse cleanly into title/body/AC — surface and fix before syncing
4. Confirm "v1" milestone exists or will be created

Use Algorithm mode. Initialize a PRD (id: v1-github-sync).
```

## What to expect back

- 49 issues created on github.com/mgoodric/security-atlas
- A "v1" milestone scoped to all of them
- Labels applied per cluster × mode × estimate
- `docs/issues/_INDEX.md` Status column updated with GitHub issue URLs

## Source-of-truth split

| Concern                                         | Where it lives                 |
| ----------------------------------------------- | ------------------------------ |
| Acceptance criteria, anti-criteria, canvas refs | `docs/issues/*.md` (canonical) |
| Status, assignees, comments, milestone          | GitHub Issues (tracker)        |

If AC changes: edit the markdown, then re-run a sync step to update the issue body. Never edit AC text on github.com — it'll silently drift from the canonical spec.

## Notes

- The report-back gate is important. If a markdown file doesn't parse cleanly, fix it before creating GH issues — you don't want to clean up 49 mis-shaped issues on github.com.
- Labels are flat (no namespacing). If you prefer scoped labels (`cluster:spine`, `mode:hitl`), update the prompt's label list before running.
- This sync is one-way (markdown → GH). Reverse sync (GH comment → markdown) is out of scope for this prompt.
