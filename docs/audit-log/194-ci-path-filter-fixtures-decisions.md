# Slice 194 — CI path filter: add `fixtures/**` to Playwright gate (decisions log)

**Slice:** 194 · **Type:** AFK · **Cluster:** Infra/CI
**Branch:** `infra/194-ci-fixtures-pathfilter`
**Spillover from:** slice 193 (dashboard.spec AC-5 upcoming-row fixture fix)

---

## D-0: The surfaced gap

`.github/workflows/ci.yml` defines a `changes` job that runs
`dorny/paths-filter@v4` to produce a `code` boolean output. Expensive
downstream jobs — including `Frontend · Playwright e2e` — gate on
`needs.changes.outputs.code == 'true'`; otherwise they short-circuit to
a docs-only-stub sibling job that just echoes a message.

The pre-slice-194 `code:` glob list enumerated:

- `**/*.go`, `**/*.ts`, `**/*.tsx`, `**/*.js`, `**/*.py`
- `go.{mod,sum,work,work.sum}`, `package*.json`, `pnpm-lock.yaml`
- `migrations/**`, `sql/**`, `proto/**`, `policies/**`, `schemas/**`
- `connectors/**`, `web/**`, `internal/**`, `cmd/**`, `pkg/**`,
  `oscal-bridge/**`, `gen/**`
- Build/deploy/tool files

**It did NOT list `fixtures/**`.** A PR that touched ONLY a
Playwright e2e seed file (under `fixtures/e2e/`) was classified
`code: false` and Playwright was bypassed — the very surface the
fixture was supposed to exercise.

---

## D-1: How slice 193 exposed it

Slice 193 fixed the `dashboard.spec.ts` AC-5 upcoming-row failure by
flipping the seeded exception from `status='approved'` to `'active'`
in `fixtures/e2e/dashboard.sql`. The expected outcome was that on the
slice 193 PR, the `Frontend · Playwright e2e` job would run the real
spec and turn green.

Instead, the engineer observed that the e2e job short-circuited to its
stub because the PR diff touched only `fixtures/**` + `docs/**` —
neither matched the `code:` filter. The slice-193 engineer worked
around this by adding a brief slice-193 preamble comment to
`web/e2e/dashboard.spec.ts` — the spec file itself — which DID match
`web/**` and forced the gate to fire.

That workaround proved the fixture was correct (the real Playwright
job went green), but the underlying CI gate was incorrect: a future
fixture-only PR would silently miss its gate without the workaround.

Slice 194's job is to remove the need for that workaround.

---

## D-2: The fix — single-line insertion

```text
              - 'connectors/**'
              - 'web/**'
              - 'fixtures/**'    # ← added (single quote, matches existing style)
              - 'internal/**'
```

Placement adjacent to `web/**` is deliberate: both globs are
e2e-test-relevant (Playwright specs at `web/e2e/**` + seed fixtures at
`fixtures/e2e/**`). Logical grouping for future readers; no functional
effect.

---

## D-3: Why NOT also add to `schemas:` filter

The `changes` job exposes a second filter output, `schemas:`, that
gates `internal/api/schemaregistry/schemas/**`. That filter is
intentionally narrow (slice 179 P0-179-4 — the schema-removal-age
check is informational, NOT branch-protection-required, and runs only
on diffs that touch the canonical schema directory).

Adding `fixtures/**` to `schemas:` would either:

1. Fire the schema-removal-age check on PRs that don't touch schemas
   (false-positive surface), or
2. Compose meaninglessly with the existing narrow gate.

Neither is desirable. The `fixtures/**` addition is to `code:` only —
the broad gate that controls real CI jobs including Playwright.

This satisfies **P0-194-1** (no fixtures globs added to other
unrelated filters).

---

## D-4: Why NOT broaden existing globs

We could have written the patch as "add `**/*.sql` to the `code:`
list" — that would catch fixture-SQL files but also catch any future
`*.sql` anywhere in the tree (migrations, queries, ad-hoc scripts).

Today the pre-slice-194 filter already lists `migrations/**` and
`sql/**` for that purpose; adding `**/*.sql` would be redundant noise
that broadens the gate's surface unnecessarily. The narrower
`fixtures/**` is precise — it triggers on exactly the file class that
needs the gate.

This satisfies **P0-194-2** (no broadening of existing filters).

---

## D-5: No change to stub-sibling pattern

The 15+ stub-sibling jobs in `ci.yml` (each running
`echo "Docs-only change — skipped per dorny/paths-filter@v4 (slice 061)."`)
are untouched. Their `if:` conditions still gate on
`needs.changes.outputs.code != 'true'`. The contract — `code: true` →
real job, `code: false` → stub — is unchanged. Only the input to
that boolean changed.

This satisfies **P0-194-3** (stub-sibling pattern intact).

---

## D-6: Verification approach

End-to-end verification of a CI path-filter inside a PR has an
inherent limitation: you can only fully verify by triggering a
fixture-only PR through CI, which requires merging this slice first.
So the on-PR verification is necessarily partial:

1. **Static — `actionlint .github/workflows/ci.yml`** — confirms the
   modified workflow YAML parses and validates. Result: **no NEW
   diagnostics from this slice's diff** (15 pre-existing shellcheck
   warnings on unrelated shell-script blocks in the file exist on
   `main` and on this branch identically; remediation of those is
   explicitly out of scope per P0-194-3 — they belong to a future
   focused-cleanup slice).
2. **Diff — surface review** — the patch is a single-line addition
   in the right block at the right indent level.
3. **CI on this PR** — this PR's diff touches `.github/workflows/**`
   which IS in the `code:` filter, so the e2e job will run on the
   slice's own PR. (That tests the OLD filter; the NEW behavior
   activates only after merge.)
4. **First post-merge fixture-only PR** — the first PR after this
   merges that touches ONLY `fixtures/**` will trigger the real
   Playwright job. That's the empirical confirmation. If a future
   PR is filed with the explicit goal of exercising this gate,
   document it in the audit-log here as the closing verification.

---

## Anti-criteria check

- **P0-194-1.** Did NOT add `fixtures/**` to the `schemas:` filter
  (or any other filter). Sole addition is to `code:`. **PASS** (see D-3).
- **P0-194-2.** Did NOT relax/broaden any existing filter. The new
  glob is narrower than the alternative (`**/*.sql`). **PASS** (see D-4).
- **P0-194-3.** Did NOT change the stub-sibling pattern. Stub jobs
  and their conditions are untouched. **PASS** (see D-5).

---

## Files changed

1. `.github/workflows/ci.yml` — one line inserted in the `code:`
   filter list (between `'web/**'` and `'internal/**'`).
2. `docs/audit-log/194-ci-path-filter-fixtures-decisions.md` — this
   file.
3. `docs/issues/_STATUS.md` — row 194 flipped from `ready` to
   `in-review` in the status-flip commit after the PR opens.

---

**Time spent (engineer wall-clock):** ~12 min from spec-read to
decisions-log committed.
