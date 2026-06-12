# 695 — Share the prebuilt atlas binary across jobs (build once)

**Cluster:** CI / Infra
**Estimate:** S–M
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P2
**Spillover from:** slice 693 (pipeline-efficiency audit, Tier 2).

## Narrative

The `atlas` binary is compiled up to **5×** per code PR: `build-go` (`go build ./...`),
`frontend-playwright`, `frontend-playwright-prod-build`, and `frontend-ui-honesty` each run
`go build -o /usr/local/bin/atlas ./cmd/atlas`, and `trivy-image` compiles it again inside
the Docker build. The three playwright-family host compilations are redundant — they build
the identical `./cmd/atlas` target on the identical runner OS (`ubuntu-latest`).

Build `./cmd/atlas` once in `build-go`, `actions/upload-artifact` it (the job already uploads
`coverage.txt` — same pattern), and have the three playwright-family jobs
`download-artifact` instead of rebuilding. Those jobs gain `needs: build-go`. Estimated
saving ~3 runner-minutes per code PR.

Trade-off (the JUDGMENT call): adding `needs: build-go` serializes the playwright jobs behind
`build-go`'s wall-clock. Net wall-clock benefit depends on whether `build-go` is already on
the PR critical path; billing always improves. The slice should measure both before/after and
keep the change only if wall-clock does not regress materially.

## Acceptance criteria

- [ ] **AC-1.** `build-go` builds `./cmd/atlas` and uploads it as an artifact.
- [ ] **AC-2.** `frontend-playwright`, `frontend-playwright-prod-build`, and
      `frontend-ui-honesty` download the artifact instead of running `go build ./cmd/atlas`.
- [ ] **AC-3.** Those three jobs declare `needs: build-go` and still pass.
- [ ] **AC-4.** The downloaded binary is made executable and placed where the jobs expect it
      (`/usr/local/bin/atlas`); the e2e/playwright suites stay green.
- [ ] **AC-5.** PR body records the wall-clock before/after so the serialization trade-off is
      visible (revert if wall-clock regresses).

## Anti-criteria

- Does NOT change what the playwright/ui-honesty suites assert.
- Does NOT couple the artifact across differing runner OS/arch (all are `ubuntu-latest`).
- Does NOT fold `trivy-image`'s in-Docker build into this (that is slice 694's lane).

## Dependencies

- Independent of 694/696 but conceptually adjacent (all reduce redundant builds).

## Notes

Source: slice 693 audit Finding 2.1.
</content>
