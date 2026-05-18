# 127 — Branch-protection drift fix — decisions log

Slice 127 is `Type: JUDGMENT`. This log records the build-time judgment
calls made while implementing the file ↔ live reconcile, the drift-
detect CI job + script, the apply script, and the CONTRIBUTING.md
documentation.

Format: Decision · Diagnosis · Alternatives weighed · Trade-off · Revisit-trigger.

---

## D0 — Drift snapshot (pre-reconcile)

**Captured 2026-05-18, immediately before this slice's reconcile commit.**

| Side                                       | Contexts (sorted)                                                                                                                                                                                                                                                                                                | Count  |
| ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------ |
| File (`.github/branch-protection.json`)    | `Analyze (go)`, `Analyze (javascript-typescript)`, `Frontend · Playwright e2e`, `Frontend · install + build`, `Frontend · vitest`, `GitGuardian Security Checks`, `Go · build + test`, `Go · integration (Postgres RLS)`, `Go · lint`, `Proto · lint + generate diff`, `Python · ruff`, `pre-commit · all hooks` | **12** |
| Live (`gh api .../required_status_checks`) | `Analyze (go)`, `Analyze (javascript-typescript)`, `Frontend · install + build`, `GitGuardian Security Checks`, `Go · build + test`, `Go · integration (Postgres RLS)`, `Go · lint`, `Proto · lint + generate diff`, `Python · ruff`, `pre-commit · all hooks`                                                   | **10** |

**Delta:** the file lists `Frontend · vitest` and `Frontend · Playwright e2e` as required-checks; live does NOT enforce either.

**Origin of the drift (verified via `git log --all --follow .github/branch-protection.json`):** slice 069 (commit `63455f9`, merged via `9824bc5`) ADDED `Frontend · vitest` and `Frontend · Playwright e2e` to the file's `required_status_checks.contexts` array as part of the verification-suite ratchet. The corresponding `gh api -X PUT …` to push the file change to live was NEVER run. Result: the file moved forward in intent; the live config stayed at the pre-069 enforcement set.

**Critically: this is NOT a deliberate human removal from live** (P0-A6 guard). Reading the git history of the file shows additive forward motion only — no subsequent commit shrinks the contexts list. The drift is the unapplied-file-change failure mode, not the file-not-tracking-deliberate-relaxation failure mode. Both options (a) and (b) are therefore safe from P0-A6's "respect deliberate human intent" angle. The choice between them is driven by AC-1 narrative pragmatics, not by P0-A6.

---

## D1 — Reconcile direction (AC-1)

**Decision:** Option **(a)** — edit `.github/branch-protection.json` to match live (remove `Frontend · vitest` and `Frontend · Playwright e2e` from the required-checks contexts list, in-line with what live actually enforces today). A `$deviations_from_slice_069` annotation block in the file documents WHY the file's original intent is being weakened, WHY option (b) is the right end-state, and WHO is responsible for completing the restoration (the slice that follows up after slice 123 lands).

**Diagnosis:** AC-1 enumerates two directions. The slice narrative's surfacing-time conversation surfaced option (b) as the maintainer's lean, with the explicit caveat (slice doc line 109): "Option (b) implementation should wait until 123 lands; the slice itself can still merge with option (a) (file-to-live) immediately to stop the bleeding."

**Alternatives weighed:**

| Option                                                                                                        | Pros                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Cons                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | Verdict                          |
| ------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------- |
| **(a)** Edit `.github/branch-protection.json` to match live (file follows live, file is weakened temporarily) | Stops the bleeding NOW — the drift IS closed at merge-time of this PR; the drift-detect CI job has a clean baseline to enforce going forward; this PR can actually merge today (option (b) would block this PR on itself, because restoring `Frontend · Playwright e2e` as required would gate every PR on 4 still-broken specs that slice 123 has not yet fixed). Documented `$deviations_from_slice_069` annotation surfaces the intent gap so it cannot be forgotten — the restoration is a one-command operation the day slice 123 lands. | Temporarily weakens the file's intent claim — for the soak window between slice 127 merging and slice 123 landing, the file says "10 contexts are required" instead of "12 contexts are required". The two checks (vitest + Playwright) keep running in CI; they just are not required-checks. The slice doc P0-A5 mandates explicit documented rationale for picking (a) — satisfied by the new `$deviations_from_slice_069` annotation block + this decisions log entry.                               | **Chosen**                       |
| **(b)** Apply `.github/branch-protection.json` to live (live follows file, enforcement is restored)           | Honors the maintainer's lean from the surfacing conversation; closes the drift in the structurally-stronger direction (file = intent IS the contract; live converges to it); restores the slice 069 verification-suite ratchet to its intended strength.                                                                                                                                                                                                                                                                                      | Slice 123 is `ready` but NOT merged. The 4 e2e specs unmasked by slice 119 (auth-open-redirect, first-time-login, logo-render, security-headers) are still broken. Applying option (b) makes `Frontend · Playwright e2e` required-checks for every PR on the project — INCLUDING this PR's own CI run, which would gate the merge of slice 127 itself on a check that fails for orthogonal reasons. The slice doc explicitly flags this dependency on line 109; the cure becomes worse than the disease. | Deferred until slice 123 merges. |

**Trade-off:** Option (a) trades a short-term weakening of the file's intent claim (2 contexts dropped) for the ability to actually merge this PR + ship the drift-detect job that prevents the next bleeding event. Option (b) is the ideal end-state but its dependency on slice 123 makes it un-applyable today. The `$deviations_from_slice_069` annotation block + this entry + a clear follow-up note in `_STATUS.md` make the restoration cheap and obvious for the next session after 123 lands: "edit `.github/branch-protection.json` to re-add the two contexts, run `bash scripts/apply-branch-protection.sh`, commit + push."

**Revisit-trigger:** When slice 123 merges. The follow-up is mechanical — edit two lines, run one script, commit. Drift-detect will confirm convergence on the next PR.

---

## D2 — Stub-twin omission (AC-5)

**Decision:** Do NOT add a stub-twin sibling job for `branch-protection-drift` (deviating from the slice 061 stub-twin convention shared by every build/test/lint job).

**Diagnosis:** AC-5 explicitly leaves this to engineer judgment ("if added, document why"). The stub-twin pattern exists to give branch-protection a named-check signal under the same job name on docs-only PRs (so the required-check never sits "Expected — waiting for status to be reported"). Two facts of this slice break that requirement:

1. The job is NOT in `required_status_checks.contexts` (P0-A1, P0-A3 — informational only). Branch protection does not gate on this name, so the "Expected — waiting" failure mode that stub-twins exist to prevent is irrelevant.
2. The job is fast (~5 seconds end-to-end: one `gh api` call + a small jq diff). Running it unconditionally on every PR (including docs-only) costs less wall-clock time than the YAML overhead of a stub-twin pair would impose.

**Alternatives weighed:**

| Option                                                        | Pros                                                                                                                                                                                                                                                                                              | Cons                                                                                                                                                                                          | Verdict    |
| ------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| **(a)** Add stub-twin (mirror the build/test/lint convention) | Consistency with the rest of the workflow file (every other expensive job has a stub-twin).                                                                                                                                                                                                       | Wrong tool for the problem the stub-twin pattern solves. This job is neither required-checks nor expensive — both conditions that motivate the stub-twin pattern are absent.                  | Rejected   |
| **(b)** No stub-twin; run the job unconditionally on every PR | Matches the actual cost shape (single `gh api` call + jq diff = ~5 seconds, dominated by checkout + harden-runner setup). Also fires on PRs that touch ONLY `.github/branch-protection.json` (which the `code` path filter does NOT match — that file is governance/markdown-adjacent, not code). | One extra job-run on docs-only PRs. The cost is ~5 seconds of runner time per docs PR. Trade is favorable: we get drift coverage on PRs that touch the file itself even when no code changes. | **Chosen** |

**Trade-off:** Option (b) trades the consistency of "every job has a stub-twin" for the simpler shape of "this job is cheap and unconditional." The slice 061 convention's load-bearing intent (named-check resolution on docs-only PRs) is satisfied trivially because this job is not a named required check.

**Revisit-trigger:** If the job ever becomes required (which would require a separate slice and explicit override of P0-A1), it MUST gain a stub-twin in the same PR that promotes it.

---

## D3 — AC-2 diff command correction

**Decision:** The drift-detect script + apply-script convergence check use `jq -cS '.required_status_checks.contexts | sort'` on the file side and `gh api … --jq '.contexts | sort'` on the live side, then `[[ "$file_ctx" == "$live_ctx" ]]`. This deviates from the literal AC-2 command in the slice doc (which uses `jq -S` without `-c` on the file side and no `| sort` on the file side).

**Diagnosis:** Running the AC-2 command literally:

```sh
diff <(jq -S .required_status_checks.contexts .github/branch-protection.json) \
     <(gh api repos/mgoodric/security-atlas/branches/main/protection/required_status_checks --jq '.contexts | sort')
```

…exits non-zero EVEN WHEN the two sides carry the same set of strings, because:

1. `jq -S` produces pretty-printed indented JSON (`[\n  "a",\n  "b"\n]`).
2. `gh api --jq '.contexts | sort'` produces compact JSON (`["a","b"]`).
3. The file side is also not `| sort`ed at the array level — `jq -S` sorts OBJECT keys, not array elements. So the file side preserves declaration order while the live side is sorted.

The literal command would always show non-empty diff. The slice doc's intent is unambiguous (verify the two SETS of contexts are equal); the literal command is a typo. We honor the intent via `jq -cS '.required_status_checks.contexts | sort'` on the file side and `--jq '.contexts | sort'` on the live side, both compact, both sorted. A direct string compare then becomes the correct test.

**Verification of AC-2 (corrected form):**

```sh
diff <(jq -cS '.required_status_checks.contexts | sort' .github/branch-protection.json) \
     <(gh api repos/mgoodric/security-atlas/branches/main/protection/required_status_checks --jq '.contexts | sort')
echo "exit=$?"
```

Returns `exit=0` after this slice's reconcile commit (verified locally 2026-05-18 against live mgoodric/security-atlas main).

**Alternatives weighed:** flagging the typo as a spillover and using the buggy command verbatim was rejected — it would have produced false-positive drift findings on every PR, defeating the purpose of the slice.

**Revisit-trigger:** A future slice that revises the slice 127 doc could correct the AC-2 command in the doc itself. Not blocking.

---

## D4 — Apply script vs inline workflow `gh api` call

**Decision:** Ship a standalone `scripts/apply-branch-protection.sh` rather than burying the apply command in the workflow file or in CONTRIBUTING.md prose only.

**Diagnosis:** AC-4 says "apply script that reads `.github/branch-protection.json` and PUTs to GitHub. Idempotent." The natural alternative is to document the bare `gh api -X PUT …` invocation in CONTRIBUTING.md and leave the script-creation step out. Three reasons the script wins:

1. **Annotation-key stripping is non-trivial.** The file carries `$comment`, `$deviations_from_slice_050_AC11`, `$deviations_from_slice_069`, `$rationale_required_signatures_off`, `$verification` keys that GitHub's PUT API rejects. A maintainer running the bare `gh api -X PUT … --input .github/branch-protection.json` would get a 422 with a not-immediately-obvious error message. The script handles this via `jq 'with_entries(select(.key | startswith("$") | not))'`.
2. **Convergence verification.** GitHub silently drops context names from the live config when those names have never reported a status on `main` (e.g. a check for a job that has never run). The script re-reads live after the PUT and surfaces the convergence-failure case as exit 3 with a clear hint. The bare command leaves this debugging to the maintainer.
3. **Idempotency check.** The script's exit semantics make "I ran this twice; nothing happened the second time" easy to verify (P0-A2). The bare command's `gh api PUT` is structurally idempotent but its output is identical on every invocation, which hides the "no-op" case.

**Trade-off:** One extra file in `scripts/` (44 sloc + ~70 sloc of header comments). The file shape matches the `scripts/audit-deps.sh`, `scripts/check-secret.sh` (slice 089), `scripts/audit-rls.sh` precedents in the repo.

**Revisit-trigger:** If a future slice promotes branch-protection management to a higher-leverage shape (e.g. Terraform, a release-tooling integration), this script may become redundant. Not in scope for v1.

---

## D5 — Where the apply ritual is documented (AC-10)

**Decision:** `CONTRIBUTING.md` gets the canonical apply-ritual subsection (new "Branch protection" subsection replacing the stale block at lines 163–173 of the pre-slice file). The decisions log (this file) records the JUDGMENT rationale. No separate `docs/branch-protection.md` is created.

**Diagnosis:** AC-3 says "add a section to `CONTRIBUTING.md` (or new `docs/branch-protection.md`) documenting the apply ritual." Two candidate homes:

| Option                                | Pros                                                                                                                                                                                                                  | Cons                                                                                                                                                                                              | Verdict    |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| **(a)** CONTRIBUTING.md               | One existing doc readers already look at; replaces a stale "≥1 approving review required" block that was inaccurate post-slice-050 drift; co-located with the rest of the contributor-facing branch-protection prose. | Adds ~40 lines to a 342-line file.                                                                                                                                                                | **Chosen** |
| **(b)** New docs/branch-protection.md | Separation of concerns; CONTRIBUTING.md stays terse.                                                                                                                                                                  | Yet another doc to discover. The audience for this content (maintainers + contributors editing the file) already reads CONTRIBUTING.md; isolating to a new doc raises the lookup cost for no win. | Rejected   |

**Trade-off:** Option (a) costs ~40 lines in CONTRIBUTING.md in exchange for replacing a stale section AND giving the apply ritual its natural home alongside the rest of the branch-protection prose.

**Revisit-trigger:** If the branch-protection content grows past ~100 lines (e.g. the project moves to an org account and the `restrictions` field comes back; multiple branches are protected with different rules), promote to `docs/branch-protection.md` and leave a pointer in CONTRIBUTING.md.
