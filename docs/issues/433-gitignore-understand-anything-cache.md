# 433 — gitignore the `.understand-anything/` local-analysis cache

**Cluster:** Infra
**Estimate:** S
**Type:** AFK
**Status:** `ready` (no dependencies)

## Narrative

The `understand-anything` code-analysis tool writes a per-machine cache to
`.understand-anything/` at the repo root — currently `~9.8M` across four
files (`knowledge-graph.json`, `fingerprints.json`, `meta.json`, and a
`.understandignore`). `meta.json` pins a local `gitCommitHash` and a list of
~1699 analyzed files: it is a snapshot of one developer's working tree at one
instant, not a shared artifact. The directory is **not** in `.gitignore`
(`grep understand .gitignore` returns nothing) and shows as untracked in
`git status` — one stray `git add .` away from polluting history with a 9.8M
machine-local blob that would then churn on every analysis re-run.

This is the smallest possible cleanup: add one line to the root `.gitignore`
and confirm the directory drops out of `git status`. The deliverable is the
ignore rule plus the verification, nothing more.

**Scope discipline.** This slice does NOT delete the existing local
`.understand-anything/` directory (it is the current developer's working
cache — deleting it is their call, not the repo's), does NOT add the tool to
any CI workflow, and does NOT touch any other ignore rule.

## Threat model

STRIDE pass for a `.gitignore`-only change. The relevant categories are
Information disclosure and Tampering; the rest are not-applicable for a file
that adds an exclusion rule and touches no runtime code, no auth boundary,
and no tenant-scoped data path.

**S — Spoofing.** N/A. No endpoints, no identities, no auth surface touched.

**T — Tampering.** The only integrity concern is the inverse of the usual
one: a too-broad ignore glob (e.g. `understand*` or a bare `*.json`) could
silently mask a legitimately-tracked file from `git status`, causing a real
change to go uncommitted. Mitigation: the rule is the exact-path,
directory-anchored form `/.understand-anything/` (leading slash = root-only,
trailing slash = directory-only) — it cannot match anything outside that one
cache directory. AC-4 proves no currently-tracked file is newly ignored.

**R — Repudiation.** N/A. No audit-logged operation.

**I — Information disclosure.** This is the threat the slice _closes_, not
one it opens. `meta.json` embeds a local machine's analyzed-file inventory
and a working-tree commit hash; left untracked-but-not-ignored it is one
`git add` from leaking into the public-bound repo history. Adding the ignore
rule removes that footgun. The change itself discloses nothing.

**D — Denial of service.** N/A.

**E — Elevation of privilege.** N/A. No role check, no `atlas_app` /
`atlas_migrate` / `atlas_service_account` boundary, no privileged path.

**Verdict:** CLEAN — the slice closes a latent disclosure footgun and the
only introduced risk (over-broad glob masking a tracked file) is gated by
AC-2 (exact anchored path) and AC-4 (no-tracked-file-newly-ignored proof).

## Acceptance criteria

- [ ] **AC-1.** Root `.gitignore` contains a `.understand-anything/` entry,
      grouped under a clearly-labelled comment block (e.g.
      `# Local code-analysis tool cache (understand-anything) — per-machine, never commit`).
- [ ] **AC-2.** The ignore rule is the directory-anchored exact-path form
      (`/.understand-anything/` or `.understand-anything/`) — NOT a wildcard
      that could match files outside that directory.
- [ ] **AC-3.** `git status --porcelain` no longer lists any
      `.understand-anything/` path as untracked.
- [ ] **AC-4.** `git ls-files --error-unmatch` against every currently-tracked
      file still succeeds, and `git status` shows no previously-tracked file
      newly disappearing — i.e. the new rule ignores nothing that was already
      tracked. (Concretely: `git ls-files -i -c --exclude-standard` returns
      empty.)
- [ ] **AC-5.** No file other than `.gitignore` is modified by this slice.

## Constitutional invariants honored

- No architecture invariant is touched — this is an ignore-rule change with
  no runtime, schema, auth, or tenancy surface.
- Style: no emojis; the `.gitignore` comment is plain ASCII.

## Canvas references

- None directly. This is repo-hygiene infrastructure below the canvas's
  design layer. (The mockups/plans-vs-code separation in CLAUDE.md "Working
  norms" is the nearest analogue — keep machine-local artifacts out of the
  tracked tree.)

## Dependencies

- None.

## Anti-criteria (P0 — block merge)

- Does NOT delete or modify the existing local `.understand-anything/`
  directory contents.
- Does NOT use a wildcard glob that could mask a tracked file outside the
  cache directory (P0 — the inverse-tampering guard from the threat model).
- Does NOT add the tool to any CI workflow, `justfile` target, or
  pre-commit config.
- Does NOT touch any `.gitignore` rule other than the new cache entry.

## Skill mix (3-5)

- `git-worktree-manager` (or plain git) — verify status/tracked-file state.
- `simplify` — pre-PR sanity (one-line change; trivial pass).
- `ship-gate` — confirm no unintended file touched.

## Notes for the implementing agent

This is genuinely a one-line change plus two verification commands; do not
over-engineer it. The single design call is the anchor form of the glob —
prefer `.understand-anything/` (matches the directory anywhere, but with the
trailing slash restricting to directories) or the root-anchored
`/.understand-anything/`. Either satisfies AC-2; pick the form that matches
the surrounding `.gitignore` house style. Do NOT `rm -rf` the local cache as
a "cleanup bonus" — that is the developer's working artifact and out of
scope (P0).
