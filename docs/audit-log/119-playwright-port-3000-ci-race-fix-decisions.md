# Slice 119 — decisions log

## Diagnosis trail (the load-bearing artifact for this slice)

### The slice's primary hypothesis was wrong

The slice doc (`docs/issues/119-playwright-port-3000-ci-race-fix.md`) listed the most-likely root cause as:

> Playwright `webServer` config has `reuseExistingServer: true` set incorrectly in `web/playwright.config.ts` — that flag tells Playwright "if a server already responds on the URL, use it" — which is the right call locally but in CI it can mask a stale process or trip on the dev server's HMR socket

That hypothesis is **false**. Reading `web/playwright.config.ts` line 53:

```ts
webServer: {
  command: "npm start",
  url: baseURL,
  reuseExistingServer: !isCI,   // <-- already !isCI, not unconditional true
  timeout: 120_000,
  ...
}
```

`!isCI` evaluates to `false` in CI (`CI=true` → `isCI=true` → `!isCI=false`). So
in CI, Playwright is told **NOT** to reuse an existing server — it must spawn
its own via `npm start`.

### The actual root cause: workflow-started server + Playwright-started server collide on :3000

`.github/workflows/ci.yml` lines 832–842 (the `frontend-playwright` job, "Start
web server" step) explicitly spawns a Next.js server before Playwright runs:

```yaml
- name: Start web server
  working-directory: web
  run: |
    (PORT=3000 npm start > /tmp/web.log 2>&1) &
    echo "WEB_PID=$!" >> "$GITHUB_ENV"
    for i in $(seq 1 30); do
      if curl -sf http://localhost:3000/; then
        echo "web ready"; break
      fi
      sleep 2
    done
- name: Run Playwright tests
  working-directory: web
  run: npx playwright test
```

Then `npx playwright test` reads the webServer config — `reuseExistingServer:
false` in CI — and tries to spawn its own `npm start` on :3000. The bind
fails because the workflow-started server is already there, and Playwright
errors with the exact message the slice cites:

```
Error: http://localhost:3000 is already used, make sure that nothing is
running on the port/url or set reuseExistingServer:true in config.webServer.
```

The error message itself names the fix: `set reuseExistingServer:true in
config.webServer`. In CI specifically, that is the correct call — the
workflow's "Start web server" step is the authoritative server bring-up
(it's the same pattern used for atlas, MinIO, NATS, postgres). Playwright
should reuse what's there, not race for the port.

### Why the comment and the code contradict each other

The config's own comment (lines 7–9, lifted from slice 069) says:

> in CI a real docker-compose self-host bundle owns bring-up, so we
> `reuseExistingServer` when CI=true

And again at lines 45–49:

> Local dev: spin up `npm start` if nothing is listening on :3000.
> CI: rely on the docker-compose self-host bundle (the
> `Frontend · Playwright e2e` job in .github/workflows/ci.yml brings up
> postgres + nats + minio + atlas + web before invoking `playwright
test`). `reuseExistingServer: !isCI` keeps either path one-command.

Both prose blocks describe the **intended** behavior — reuse the existing
server in CI. The boolean expression is **inverted**: `!isCI` means "reuse
when NOT in CI." The code does the opposite of what the comment says.

This is a classic boolean-polarity bug. The author intended "reuse the
server when in CI" and wrote "reuse the server when not in CI." Locally it
happens to also work (because there's usually no server running and
Playwright spawns its own) so the bug was invisible until the CI step
started spawning the server first — which the slice 069 workflow has done
since the job was first authored.

### Why slice 082 didn't catch it

Slice 082's `seedFromFixture()` harness was the suspected culprit
(hypothesis #3 in the slice doc). Reading `web/e2e/seed.ts`:

- The harness only spawns `psql` subprocesses (lines 71–73, 115–117).
- No `next dev`, no `npm start`, no port binding.

Slice 082's decisions log D1 correctly identified the port-3000 issue as
"pre-existing infrastructure flake, not seed-harness-caused" — but did not
diagnose the actual cause because the slice was scoped to the seed harness.

### Why CI-step duplication isn't the fix

An alternative fix would be removing the "Start web server" step from
`ci.yml` and letting Playwright's webServer spawn the server. Rejected:

1. The workflow already establishes the canonical "bring services up
   first, then run tests" pattern (postgres, nats, minio, atlas all
   started by explicit steps before `npx playwright test`). Making web
   the lone exception would be a foot-gun for future contributors.
2. The workflow's "Start web server" step has a 30×2s readiness curl loop
   (`curl -sf http://localhost:3000/`). Playwright's webServer has a
   120-second timeout but uses a different readiness check (HEAD on the
   URL until 200). Diverging two readiness paths is more code to
   maintain than a one-character config fix.
3. The "Dump server logs on failure" step at line 855 reads `/tmp/web.log`
   which is populated by the workflow step's stdout redirect. If we removed
   the workflow step, we'd lose the failure-mode log dump too — Playwright's
   webServer stdout is configured as `"ignore"` (line 56) so there's no
   equivalent log capture.

The minimal, surgical, CLAUDE.md-aligned fix is to flip the boolean.

## Decision 1 — Fix shape: flip `reuseExistingServer: !isCI` to `reuseExistingServer: isCI`

The diff is one character (`!` removed). Reasoning:

- **Local dev (`isCI=false`):** `reuseExistingServer=false`. Playwright
  refuses to attach to a stale dev server — protective against the
  contributor running `npm run dev` in one terminal and `npm run test:e2e`
  in another against a stale page. Spawns its own fresh `npm start`.
- **CI (`isCI=true`):** `reuseExistingServer=true`. Playwright attaches to
  the workflow-spawned server on :3000 and runs tests. No port race.

This matches the prose intent the config's own comment documents.

Alternatives considered:

| Option                                        | Verdict                                                                                          |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `reuseExistingServer: isCI` (flip)            | **CHOSEN** — one character, matches comment, matches workflow architecture                       |
| `reuseExistingServer: !!process.env.CI`       | Equivalent — but uses `process.env.CI` twice; the existing `isCI` const is the right abstraction |
| Remove the workflow's "Start web server" step | Rejected — diverges from postgres/nats/minio/atlas pattern; loses log dump                       |
| Remove the `webServer:` block entirely        | Rejected — breaks local dev DX (no auto-spawn)                                                   |
| Add a `--port 3001` flag to one side          | Rejected — paper-mâché fix; doesn't address the root logic bug                                   |
| Add `lsof` debug step permanently             | Rejected per slice P0-A4 — debug instrumentation is removed in the final commit                  |

## Decision 2 — No CI workflow changes

The fix lives in `web/playwright.config.ts` only. The slice doc's "Notes for
the implementing agent" predicted this: "The fix LIVES IN `web/`, NOT in
the workflow files (probably). Workflow-level fixes would be a sign of a
deeper issue." Confirmed.

`.github/workflows/ci.yml` `frontend-playwright` job is unchanged. No
diagnostic `lsof` step was added, because the static read of the config +
workflow files made the bug obvious without runtime instrumentation. Per
slice anti-criterion P0-A4 (debug steps removed in final commit), the
absence of an `lsof` step in the merged diff is intentional.

## Decision 3 — No comment changes around the fix

The config's prose comment (lines 7–9, 45–49) already describes the correct
behavior. The bug was a code-vs-comment polarity inversion; the comment
side was right. Updating the comment would be churn. The one-character
code change makes the code agree with the comment.

## Decision 4 — Canary Dependabot PR

Per AC-4, validation requires re-triggering one currently-flaking
Dependabot PR post-merge and confirming Playwright passes. PR selection
recorded here once the fix lands and a canary is picked. Candidates
(currently `mergeStateStatus: UNSTABLE` on this exact failure): #151, #152,
#153, #154, #156, #158, #159. The slice prompt notes #152 (small Go patch)
and #154 (lucide-react, already classified LOW today) as good candidates.

Canary chosen: _to be filled after PR opens_

Post-canary Playwright run URL: _to be filled_

## Decision 5 — `isCI` semantics vs `process.env.CI` direct check

The fix uses the existing module-level `const isCI = !!process.env.CI`
abstraction at line 15. Keeping the single source of truth — `isCI` is
also used at lines 23, 24, 25 for `forbidOnly`, `retries`, and `workers`.
A future "is this really CI?" refinement (e.g., distinguishing
GitHub-Actions from generic CI) would be a one-line change to that const,
not a config-wide find-and-replace.
