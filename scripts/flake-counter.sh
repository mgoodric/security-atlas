#!/usr/bin/env bash
#
# flake-counter.sh — slice 352
#
# Walks recent GitHub Actions CI runs, identifies flake events
# (same head_sha, same job, failure on attempt N then success on
# attempt N+M with no code change in between), aggregates to a per-
# surface rate, regenerates the markdown dashboard, and optionally
# files a `flake-investigation` GitHub issue when a per-surface
# trigger threshold is crossed.
#
# "Flake" definition is intentionally narrow for v1: only re-run-
# cleared-on-same-SHA. This is the unambiguous signal that requires
# no judgment about "what counts as a fix-forward". See
# `docs/flake-budget.md` for the full rationale.
#
# Inputs (env):
#   FLAKE_WINDOW_DAYS        How far back to walk (default: 7)
#   FLAKE_WORKFLOW           Workflow filename (default: ci.yml)
#   FLAKE_REPO               owner/repo (default: derived from `gh repo view`)
#   FLAKE_DASHBOARD_PATH     Path to dashboard (default: docs/flake-budget-dashboard.md)
#   FLAKE_OPEN_ISSUES        "true" to file issues on trigger; "false" to dry-run
#                            (default: false — explicit opt-in)
#   FLAKE_FORMAT             "markdown" (default) or "json" for machine-readable
#                            output to stdout instead of dashboard write
#   FLAKE_DRY_RUN            "true" to print would-be-written content; skip writes
#                            (default: false)
#   FLAKE_VERBOSE            "true" to log progress to stderr (default: false)
#
# Outputs:
#   - Regenerates the dashboard at FLAKE_DASHBOARD_PATH (markdown mode)
#     OR emits per-surface JSON aggregate to stdout (json mode).
#   - Exits non-zero only on tool failure (gh / jq missing). A surface
#     crossing its threshold is NOT a non-zero exit — the surface is
#     reported on the dashboard and (if FLAKE_OPEN_ISSUES=true) an issue
#     is filed. The trigger is informational, not blocking.
#
# This script is intended to run from CI (.github/workflows/flake-counter.yml)
# and locally for development. It uses only `bash`, `gh`, `jq`, `date`
# and `grep` — no python, no go, no compiled binary, per slice doc
# recommendation ("shell script that greps gh run list --json output
# for test-failure patterns is fine for v1").

set -euo pipefail

# ---------- arg / env handling ----------

FLAKE_WINDOW_DAYS="${FLAKE_WINDOW_DAYS:-7}"
FLAKE_WORKFLOW="${FLAKE_WORKFLOW:-ci.yml}"
FLAKE_DASHBOARD_PATH="${FLAKE_DASHBOARD_PATH:-docs/flake-budget-dashboard.md}"
FLAKE_OPEN_ISSUES="${FLAKE_OPEN_ISSUES:-false}"
FLAKE_FORMAT="${FLAKE_FORMAT:-markdown}"
FLAKE_DRY_RUN="${FLAKE_DRY_RUN:-false}"
FLAKE_VERBOSE="${FLAKE_VERBOSE:-false}"

# Allow `--window-days N` style override on the command line for
# convenience in local invocations. Env var still wins if both set.
while [[ $# -gt 0 ]]; do
  case "$1" in
    --window-days) FLAKE_WINDOW_DAYS="$2"; shift 2 ;;
    --workflow) FLAKE_WORKFLOW="$2"; shift 2 ;;
    --dashboard) FLAKE_DASHBOARD_PATH="$2"; shift 2 ;;
    --open-issues) FLAKE_OPEN_ISSUES="true"; shift ;;
    --json) FLAKE_FORMAT="json"; shift ;;
    --dry-run) FLAKE_DRY_RUN="true"; shift ;;
    --verbose) FLAKE_VERBOSE="true"; shift ;;
    --help|-h)
      sed -n '3,40p' "$0"
      exit 0
      ;;
    *)
      echo "flake-counter: unknown arg $1" >&2
      exit 2
      ;;
  esac
done

log() {
  if [[ "$FLAKE_VERBOSE" == "true" ]]; then
    echo "flake-counter: $*" >&2
  fi
}

# Tool checks.
for tool in gh jq date; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "flake-counter: required tool '$tool' not found on PATH" >&2
    exit 2
  fi
done

# Resolve repo: explicit env override > `gh repo view` autodetect. The
# autodetect path lets the script work from any clone without needing
# the maintainer to remember the slug.
if [[ -z "${FLAKE_REPO:-}" ]]; then
  if FLAKE_REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null); then
    log "resolved repo via gh: $FLAKE_REPO"
  else
    echo "flake-counter: FLAKE_REPO not set and 'gh repo view' failed" >&2
    exit 2
  fi
fi

# ---------- date math ----------

# `gh run list --created <range>` is what we want. Format: ISO-8601.
# `date -u -v-7d` is the BSD/macOS form; `date -u -d '-7 days'` is GNU.
# We prefer the GNU form (CI runs on ubuntu); fall back to BSD for
# local macOS dev.
if NOW_ISO=$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null); then
  :
else
  echo "flake-counter: 'date' did not return a usable ISO timestamp" >&2
  exit 2
fi

# Day floor for the window.
if SINCE_ISO=$(date -u -d "-${FLAKE_WINDOW_DAYS} days" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null); then
  log "GNU date path: since=$SINCE_ISO"
elif SINCE_ISO=$(date -u -v-"${FLAKE_WINDOW_DAYS}"d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null); then
  log "BSD date path: since=$SINCE_ISO"
else
  echo "flake-counter: could not compute SINCE date" >&2
  exit 2
fi

log "window: $SINCE_ISO .. $NOW_ISO ($FLAKE_WINDOW_DAYS days)"
log "workflow: $FLAKE_WORKFLOW  repo: $FLAKE_REPO"

# ---------- surface table ----------

# Surface -> CI job-name mapping. Edits to this list require a slice
# (the budget shape is versioned per `docs/flake-budget.md`).
#
# Bash 3 / 4 compatible (no associative arrays — macOS still ships bash 3.2).
SURFACES=(
  "go-unit|Go · build + test|0.0|1|"
  "go-integration|Go · integration (Postgres RLS)|0.5|2|7"
  "vitest|Frontend · vitest|0.0|1|"
  "playwright|Frontend · Playwright e2e|1.0|2|7"
)
# Format: surface-slug | job-name | target-pct | trigger-count | trigger-days
# A blank trigger-days means "any single flake triggers" (Go unit, vitest).

surface_for_job() {
  local job="$1"
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r slug name _ _ _ <<<"$s"
    if [[ "$job" == "$name" ]]; then
      echo "$slug"
      return 0
    fi
  done
  echo ""
}

surface_target_pct() {
  local slug="$1"
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r ss _ target _ _ <<<"$s"
    if [[ "$ss" == "$slug" ]]; then
      echo "$target"
      return 0
    fi
  done
  echo "0.0"
}

surface_trigger_count() {
  local slug="$1"
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r ss _ _ trig _ <<<"$s"
    if [[ "$ss" == "$slug" ]]; then
      echo "$trig"
      return 0
    fi
  done
  echo "1"
}

surface_trigger_days() {
  local slug="$1"
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r ss _ _ _ days <<<"$s"
    if [[ "$ss" == "$slug" ]]; then
      echo "$days"
      return 0
    fi
  done
  echo ""
}

surface_job_name() {
  local slug="$1"
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r ss name _ _ _ <<<"$s"
    if [[ "$ss" == "$slug" ]]; then
      echo "$name"
      return 0
    fi
  done
  echo ""
}

# ---------- integration-surface broadening (slice 420) ----------
#
# The exact required CI check name for the integration surface. The
# A->A+1 broadening below keys on this name ONLY (P0-4): a lint / sqlc /
# govulncheck failure must NEVER be mis-attributed to the integration
# surface. This is the same string slice 352 put in the SURFACES table
# for the `go-integration` slug; it is repeated here as a named constant
# so the broadening is unambiguously integration-scoped and greppable.
INTEGRATION_JOB_NAME="Go · integration (Postgres RLS)"

# The integration test surface, as a regex over changed-file paths. A
# commit that changes a file matching this is considered to have touched
# the code that `go test -tags=integration -p 1 ./internal/...` exercises
# (and therefore could legitimately FIX an integration failure). Kept as
# a POSIX ERE (no GNU-only \b / \s — BSD grep on macOS silently no-ops
# those; this bit slice 339). Matches Go source under internal/, plus the
# migrations / sqlc generated surface that integration tests run against.
INTEGRATION_CODE_PATH_RE='^(internal/.*\.go|migrations/|internal/db/|cmd/.*\.go)'

# is_integration_surface JOB_NAME -> exit 0 iff the job name is EXACTLY
# the integration surface check. Used to gate the A->A+1 broadening so it
# cannot fire on any other surface (P0-4, P0-6: detection-only, scoped).
is_integration_surface() {
  [[ "$1" == "$INTEGRATION_JOB_NAME" ]]
}

# classify_integration_transition FAIL_CONCLUSION SUCC_CONCLUSION CHANGED_FILES
#
# THE LOAD-BEARING JUDGMENT (P0-3 / AC-4 / AC-5). Decides whether an
# integration-surface failure on commit A that is GREEN on the immediate
# child commit A+1 is a FLAKE (the test is non-deterministic) or a
# FIX-FORWARD (A+1's code actually fixed the failing test).
#
#   FAIL_CONCLUSION  the integration job's conclusion on commit A
#                    (one of failure / timed_out / cancelled to qualify)
#   SUCC_CONCLUSION  the integration job's conclusion on commit A+1
#                    (must be "success" to qualify)
#   CHANGED_FILES    newline-separated list of paths A+1 changed relative
#                    to A (the GitHub compare API `files[].filename`)
#
# Rule (mechanically defensible, documented in
# docs/audit-log/420-flake-definition-decisions.md):
#   - If A did not fail (or A+1 did not succeed): "not-applicable".
#   - Else if A+1's changed-files set intersects the integration test
#     surface (INTEGRATION_CODE_PATH_RE): "fix-forward" — A+1 plausibly
#     FIXED the failing integration test, so it is NOT a flake (AC-4).
#   - Else (A+1 changed nothing on the integration surface — e.g. a docs
#     only push, a CHANGELOG bump, an unrelated connector): "flake" —
#     the integration job went red then green with no relevant code
#     change, which is the scheduler-flake shape (AC-5).
#
# Emits exactly one of: not-applicable | fix-forward | flake
classify_integration_transition() {
  local fail_concl="$1" succ_concl="$2" changed_files="$3"

  case "$fail_concl" in
    failure | timed_out | cancelled) : ;;
    *)
      echo "not-applicable"
      return 0
      ;;
  esac
  if [[ "$succ_concl" != "success" ]]; then
    echo "not-applicable"
    return 0
  fi

  # Did A+1 touch the integration test surface? grep -E (POSIX ERE,
  # portable on BSD + GNU). Empty changed-files -> no match -> flake.
  if printf '%s\n' "$changed_files" | grep -Eq "$INTEGRATION_CODE_PATH_RE"; then
    echo "fix-forward"
  else
    echo "flake"
  fi
}

# ---------- fetch runs ----------

# Use the REST API directly via `gh api` with paging so we can pull
# all attempts (run_attempt). `gh run list` does NOT expose attempts
# in its JSON columns; the API does.
#
# Endpoint: GET /repos/{owner}/{repo}/actions/workflows/{workflow}/runs
# Filter:   created>=SINCE_ISO
# Paging:   --paginate accumulates pages.
#
# We need ALL attempts, including non-first. `?per_page=100` + paginate
# is sufficient. For each run-id we then fetch /attempts/{n}/jobs to
# get per-job conclusions.

log "fetching workflow runs since $SINCE_ISO ..."

RUNS_JSON=$(mktemp)
trap 'rm -f "$RUNS_JSON"' EXIT

# The GitHub Actions `workflow_runs` endpoint hard-caps a single
# `created` filter's paginated response at 1000 entries — exceeded
# results are silently truncated, not signalled. For a high-velocity
# repo (security-atlas runs ~1000 ci.yml workflows per 7 days) we
# must slice the window into smaller chunks that each fit under the
# cap, then deduplicate by run id.
#
# Slice size: 7 days per chunk. Pragmatic floor — we know the 7-day
# count is in the ~1000 range; halving it to 7-day chunks keeps each
# chunk safely under the cap while keeping the chunk count low.
date_offset_iso() {
  # date_offset_iso DAYS — emits an ISO-Z timestamp DAYS before NOW.
  local d="$1"
  if date -u -d "-${d} days" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null; then
    :
  else
    date -u -v-"${d}"d +%Y-%m-%dT%H:%M:%SZ
  fi
}

CHUNK_DAYS=7
remaining=$FLAKE_WINDOW_DAYS
chunk_lo_days=0
: > "$RUNS_JSON.raw"
while [[ "$remaining" -gt 0 ]]; do
  if [[ "$remaining" -ge "$CHUNK_DAYS" ]]; then
    chunk_hi_days=$((chunk_lo_days + CHUNK_DAYS))
  else
    chunk_hi_days=$((chunk_lo_days + remaining))
  fi
  chunk_hi=$(date_offset_iso "$chunk_lo_days")
  chunk_lo=$(date_offset_iso "$chunk_hi_days")
  log "fetching chunk $chunk_lo .. $chunk_hi"
  # `created` accepts a range with `..` separator per GitHub search
  # syntax: created=2026-05-21T..2026-05-28T. URL-encode the `..`.
  if ! gh api --paginate \
    -H "Accept: application/vnd.github+json" \
    "repos/$FLAKE_REPO/actions/workflows/$FLAKE_WORKFLOW/runs?created=${chunk_lo}..${chunk_hi}&per_page=100" \
    --jq '.workflow_runs[] | {id, head_sha, head_branch, conclusion, run_attempt, run_started_at, html_url, event}' \
    >> "$RUNS_JSON.raw" 2>/dev/null; then
      echo "flake-counter: gh api failed for chunk $chunk_lo..$chunk_hi" >&2
      exit 2
  fi
  chunk_lo_days=$chunk_hi_days
  remaining=$((FLAKE_WINDOW_DAYS - chunk_lo_days))
done

# Dedupe by run id (overlap at chunk boundaries can repeat a run).
jq -sc 'unique_by(.id) | .[]' "$RUNS_JSON.raw" > "$RUNS_JSON" 2>/dev/null || {
  echo "flake-counter: jq dedupe failed" >&2
  exit 2
}
rm -f "$RUNS_JSON.raw"

run_count=$(wc -l < "$RUNS_JSON" | tr -d ' ')
log "fetched $run_count workflow runs (deduped)"

if [[ "$run_count" -eq 0 ]]; then
  log "no runs in window — emitting empty dashboard"
fi

# ---------- group by head_sha; find flakes ----------

# Strategy:
# 1. Build a list of (head_sha, run_id, attempt, conclusion) tuples.
# 2. For each head_sha with >=2 distinct run_attempts:
#    a. Fetch jobs for each run_id.
#    b. For each job_name that has both a FAILED attempt and a SUCCESS
#       attempt at a HIGHER run_attempt, count 1 flake event for that
#       (head_sha, job_name).
# 3. Aggregate flake events per surface.
# 4. Surface attempt total = number of distinct (head_sha, attempt)
#    pairs where the job ran on a known surface (success+failure both
#    count; skipped does not).
# 5. Flake rate = flakes / attempts * 100.

ATTEMPTS_BY_SHA=$(mktemp)
JOBS_TMP=$(mktemp)
FLAKE_EVENTS=$(mktemp)
SURFACE_ATTEMPTS=$(mktemp)
# slice 420 — integration-surface timeline for the A->A+1 broadening:
# one row per (sha, branch) with the integration job's FINAL conclusion
# + run start time + a failure-run URL. Pass C walks this ordered by
# branch + time to find red->green-on-next-push transitions.
INTEG_TIMELINE=$(mktemp)
trap 'rm -f "$RUNS_JSON" "$ATTEMPTS_BY_SHA" "$JOBS_TMP" "$FLAKE_EVENTS" "$SURFACE_ATTEMPTS" "$INTEG_TIMELINE"' EXIT

# head_sha -> list of "attempt:run_id" sorted ascending
if ! jq -sc 'group_by(.head_sha) | .[] | {head_sha: .[0].head_sha, head_branch: (.[0].head_branch // ""), attempts: [.[] | {attempt: .run_attempt, run_id: .id, conclusion: .conclusion, started: .run_started_at, html_url: .html_url}] | sort_by(.attempt)} | select(.attempts | length >= 1)' \
  "$RUNS_JSON" > "$ATTEMPTS_BY_SHA" 2>&1; then
    echo "flake-counter: jq grouping failed" >&2
    head -5 "$ATTEMPTS_BY_SHA" >&2 || true
    exit 2
  fi

# Initialise per-surface counters (in plain files to avoid associative-
# array dependency).
for s in "${SURFACES[@]}"; do
  IFS='|' read -r slug _ _ _ _ <<<"$s"
  echo "0" > "/tmp/flake_count_$slug.$$"
  echo "0" > "/tmp/flake_attempts_$slug.$$"
  : > "/tmp/flake_tests_$slug.$$"
  : > "/tmp/flake_events_$slug.$$"
done

cleanup_per_surface() {
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r slug _ _ _ _ <<<"$s"
    rm -f "/tmp/flake_count_$slug.$$" \
          "/tmp/flake_attempts_$slug.$$" \
          "/tmp/flake_tests_$slug.$$" \
          "/tmp/flake_events_$slug.$$"
  done
}
trap 'cleanup_per_surface; rm -f "$RUNS_JSON" "$ATTEMPTS_BY_SHA" "$JOBS_TMP" "$FLAKE_EVENTS" "$SURFACE_ATTEMPTS" "$INTEG_TIMELINE"' EXIT

# Iterate per-sha groups.
#
# Two-pass approach for performance:
#   Pass A (cheap, no API calls): for every sha, count 1 attempt for
#     every surface IF the run's overall conclusion is not skipped /
#     cancelled / null. This approximates "the surface ran" using the
#     run-level signal. The approximation overcounts when path-filters
#     skipped specific surface jobs while the workflow as a whole
#     concluded success, but the path-filter cases are rare (docs-only
#     PRs); the bias is small and uniform across surfaces so rates
#     remain comparable. Documented in the dashboard's methodology
#     section.
#   Pass B (per-attempt API calls): for shas with multiple attempts AND
#     mixed conclusions (at least one failure + at least one success),
#     fetch per-attempt job data and detect re-run-cleared flakes per
#     surface.
sha_processed=0
sha_with_multi=0
while IFS= read -r group; do
  sha=$(echo "$group" | jq -r '.head_sha')
  attempts=$(echo "$group" | jq -c '.attempts')
  branch=$(echo "$group" | jq -r '.head_branch // ""')
  num_attempts=$(echo "$attempts" | jq 'length')
  sha_processed=$((sha_processed + 1))

  # Pass A — cheap denominator from run-level conclusions. Count the
  # LAST attempt's conclusion (the final outcome the maintainer sees);
  # if it's a usable conclusion, count 1 attempt per surface.
  last_conclusion=$(echo "$attempts" | jq -r ".[$((num_attempts - 1))].conclusion // \"\"")
  if [[ "$last_conclusion" == "success" || "$last_conclusion" == "failure" || "$last_conclusion" == "timed_out" ]]; then
    for s in "${SURFACES[@]}"; do
      IFS='|' read -r slug _ _ _ _ <<<"$s"
      cur=$(cat "/tmp/flake_attempts_$slug.$$")
      echo $((cur + 1)) > "/tmp/flake_attempts_$slug.$$"
    done
  fi

  # slice 420 — record this SHA in the integration A->A+1 timeline using
  # the run-level FINAL conclusion as a cheap candidate proxy. This is a
  # CANDIDATE filter only: Pass C re-confirms a red candidate via a
  # targeted integration-JOB fetch (P0-4: never trust the run-level
  # conclusion alone to attribute a failure to the integration surface —
  # a lint failure also reds the run). Columns:
  #   run_started \t branch \t sha \t run_conclusion \t failure_run_url \t last_run_id \t last_attempt_no
  first_started=$(echo "$attempts" | jq -r '.[0].started // ""')
  last_run_id=$(echo "$attempts" | jq -r ".[$((num_attempts - 1))].run_id")
  last_attempt_no=$(echo "$attempts" | jq -r ".[$((num_attempts - 1))].attempt")
  fail_url=$(echo "$attempts" | jq -r '[.[] | select(.conclusion=="failure" or .conclusion=="timed_out" or .conclusion=="cancelled") | .html_url] | (.[0] // "")')
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$first_started" "$branch" "$sha" "$last_conclusion" "$fail_url" "$last_run_id" "$last_attempt_no" \
    >> "$INTEG_TIMELINE"

  # Pass B — only multi-attempt with mixed conclusions get the
  # expensive per-attempt-job API treatment.
  if [[ "$num_attempts" -lt 2 ]]; then
    continue
  fi
  has_failure=$(echo "$attempts" | jq -r 'map(select(.conclusion == "failure" or .conclusion == "timed_out" or .conclusion == "cancelled")) | length')
  has_success=$(echo "$attempts" | jq -r 'map(select(.conclusion == "success")) | length')
  if [[ "$has_failure" -eq 0 || "$has_success" -eq 0 ]]; then
    continue
  fi
  sha_with_multi=$((sha_with_multi + 1))
  log "candidate flake sha ${sha:0:10} branch=$branch attempts=$num_attempts"

  : > "$JOBS_TMP.all"
  for ((i = 0; i < num_attempts; i++)); do
    run_id=$(echo "$attempts" | jq -r ".[$i].run_id")
    attempt_no=$(echo "$attempts" | jq -r ".[$i].attempt")

    if ! gh api -H "Accept: application/vnd.github+json" \
      "repos/$FLAKE_REPO/actions/runs/$run_id/attempts/$attempt_no/jobs" \
      --jq '.jobs[] | {name, conclusion, html_url}' \
      > "$JOBS_TMP" 2>/dev/null; then
      log "warn: failed to fetch jobs for run $run_id attempt $attempt_no — skipping"
      continue
    fi

    while IFS= read -r job_line; do
      [[ -z "$job_line" ]] && continue
      job_name=$(echo "$job_line" | jq -r '.name')
      job_conclusion=$(echo "$job_line" | jq -r '.conclusion')
      job_url=$(echo "$job_line" | jq -r '.html_url')

      surface=$(surface_for_job "$job_name")
      if [[ -z "$surface" ]]; then
        continue
      fi
      printf '%s\t%s\t%s\t%s\t%s\n' "$sha" "$attempt_no" "$surface" "$job_conclusion" "$job_url" \
        >> "$JOBS_TMP.all"
    done < "$JOBS_TMP"
  done

  # Flake detection: for each (sha, surface) with multiple attempts,
  # did the surface fail on attempt N and succeed on attempt M>N?
  if [[ -s "$JOBS_TMP.all" ]]; then
    while IFS=$'\t' read -r _ s_surface; do
      [[ -z "$s_surface" ]] && continue
      attempts_for_sj=$(awk -F'\t' -v sf="$s_surface" '$3 == sf {print $0}' "$JOBS_TMP.all" \
        | sort -t$'\t' -k2 -n)
      [[ -z "$attempts_for_sj" ]] && continue
      had_failure=false
      had_success_after_failure=false
      failure_url=""
      while IFS=$'\t' read -r _ _ _ s_concl s_url; do
        if [[ "$s_concl" == "failure" || "$s_concl" == "timed_out" || "$s_concl" == "cancelled" ]]; then
          had_failure=true
          if [[ -z "$failure_url" ]]; then
            failure_url="$s_url"
          fi
        elif [[ "$s_concl" == "success" && "$had_failure" == "true" ]]; then
          had_success_after_failure=true
        fi
      done <<<"$attempts_for_sj"

      if [[ "$had_success_after_failure" == "true" ]]; then
        cur=$(cat "/tmp/flake_count_$s_surface.$$")
        echo $((cur + 1)) > "/tmp/flake_count_$s_surface.$$"
        printf '%s\t%s\t%s\n' "$sha" "$branch" "$failure_url" \
          >> "/tmp/flake_events_$s_surface.$$"
      fi
    done < <(awk -F'\t' '{print $1"\t"$3}' "$JOBS_TMP.all" | sort -u)
  fi

  rm -f "$JOBS_TMP.all"
done < "$ATTEMPTS_BY_SHA"

log "processed $sha_processed shas; $sha_with_multi had multi-attempt mixed conclusions"

# ---------- Pass C: integration A->A+1 broadening (slice 420) ----------
#
# Catches the scheduler-flake shape the same-SHA Pass B misses: the
# integration job goes RED on one push and GREEN on the very next push to
# the same branch, with NO change to the integration test surface in
# between. That red->green-on-next-push with no relevant code change is a
# flake (AC-5); the same transition WITH an integration-code change is a
# fix-forward and is NOT counted (AC-4 / P0-3).
#
# Scope (P0-4): keyed on the EXACT integration job name only. A red run
# is only treated as an integration failure after a targeted job-level
# fetch confirms `Go · integration (Postgres RLS)` itself failed — a
# lint / sqlc red run is ignored.
#
# Cost: bounded. The per-branch walk is pure-bash over the already-
# fetched timeline; API calls (integration-job confirm + compare) fire
# ONLY on a red->green adjacency, which is rare.
#
# fetch_integration_conclusion RUN_ID ATTEMPT -> echoes the integration
# job's conclusion for that run/attempt ("" if absent / fetch failed).
fetch_integration_conclusion() {
  local run_id="$1" attempt_no="$2"
  gh api -H "Accept: application/vnd.github+json" \
    "repos/$FLAKE_REPO/actions/runs/$run_id/attempts/$attempt_no/jobs" \
    --jq ".jobs[] | select(.name == \"$INTEGRATION_JOB_NAME\") | .conclusion" \
    2>/dev/null | head -n 1 || true
}

# fetch_changed_files BASE_SHA HEAD_SHA -> newline-separated changed
# paths from the GitHub compare API ("" on failure / empty diff).
fetch_changed_files() {
  local base="$1" head="$2"
  gh api -H "Accept: application/vnd.github+json" \
    "repos/$FLAKE_REPO/compare/$base...$head" \
    --jq '.files[].filename' \
    2>/dev/null || true
}

integ_aafwd_flakes=0
if [[ -s "$INTEG_TIMELINE" ]]; then
  # Sort by branch then by run-start time so adjacent rows within a
  # branch are chronological pushes.
  sorted_timeline=$(sort -t$'\t' -k2,2 -k1,1 "$INTEG_TIMELINE")

  prev_branch=""
  prev_concl=""
  prev_sha=""
  prev_fail_url=""
  prev_run_id=""
  prev_attempt=""
  while IFS=$'\t' read -r _c_started c_branch c_sha c_concl c_fail_url c_run_id c_attempt; do
    [[ -z "$c_sha" ]] && continue

    # Only a transition WITHIN the same branch is an A->A+1 push pair.
    if [[ "$c_branch" == "$prev_branch" && -n "$prev_branch" ]]; then
      # Candidate: previous push red-ish, this push green.
      if [[ "$prev_concl" == "failure" || "$prev_concl" == "timed_out" || "$prev_concl" == "cancelled" ]] \
         && [[ "$c_concl" == "success" ]]; then
        # P0-4: confirm the RED run's failure was the integration job
        # itself (not lint/sqlc reddening the run-level conclusion).
        red_integ=$(fetch_integration_conclusion "$prev_run_id" "$prev_attempt")
        if [[ "$red_integ" == "failure" || "$red_integ" == "timed_out" || "$red_integ" == "cancelled" ]]; then
          # Confirm the GREEN run's integration job actually succeeded
          # (run-level success could mask a path-filtered integration job;
          # we only count a real red->green of the integration surface).
          green_integ=$(fetch_integration_conclusion "$c_run_id" "$c_attempt")
          if [[ "$green_integ" == "success" ]]; then
            changed=$(fetch_changed_files "$prev_sha" "$c_sha")
            verdict=$(classify_integration_transition "$red_integ" "$green_integ" "$changed")
            log "A->A+1 integ candidate ${prev_sha:0:10}->${c_sha:0:10} branch=$c_branch verdict=$verdict"
            if [[ "$verdict" == "flake" ]]; then
              cur=$(cat "/tmp/flake_count_go-integration.$$")
              echo $((cur + 1)) > "/tmp/flake_count_go-integration.$$"
              integ_aafwd_flakes=$((integ_aafwd_flakes + 1))
              ev_url="$prev_fail_url"
              [[ -z "$ev_url" ]] && ev_url="https://github.com/$FLAKE_REPO/commit/$prev_sha"
              printf '%s\t%s\t%s\n' "$prev_sha" "$c_branch" "$ev_url" \
                >> "/tmp/flake_events_go-integration.$$"
            fi
          fi
        fi
      fi
    fi

    prev_branch="$c_branch"
    prev_concl="$c_concl"
    prev_sha="$c_sha"
    prev_fail_url="$c_fail_url"
    prev_run_id="$c_run_id"
    prev_attempt="$c_attempt"
  done <<<"$sorted_timeline"
fi
log "Pass C: counted $integ_aafwd_flakes A->A+1 integration flake(s)"

# ---------- compute rates ----------

# Per-surface rate = flakes / attempts * 100. We use awk for the
# division since bash can't do floating point.
compute_rate() {
  local flakes="$1" attempts="$2"
  if [[ "$attempts" -eq 0 ]]; then
    echo "n/a"
  else
    awk -v f="$flakes" -v a="$attempts" 'BEGIN { printf "%.2f", (f / a) * 100 }'
  fi
}

# Status for a surface: green / yellow / red.
#   green  = rate <= target
#   yellow = trigger threshold not crossed, but rate > target
#   red    = trigger threshold crossed (flakes >= trigger_count)
status_for() {
  local flakes="$1" attempts="$2" target_pct="$3" trigger_count="$4"
  local rate
  if [[ "$attempts" -eq 0 ]]; then
    echo "no-data"
    return 0
  fi
  rate=$(awk -v f="$flakes" -v a="$attempts" 'BEGIN { printf "%.4f", (f / a) * 100 }')
  if awk -v r="$rate" -v t="$target_pct" 'BEGIN { exit (r <= t) ? 0 : 1 }'; then
    echo "green"
  elif [[ "$flakes" -lt "$trigger_count" ]]; then
    echo "yellow"
  else
    echo "red"
  fi
}

# ---------- write outputs ----------

if [[ "$FLAKE_FORMAT" == "json" ]]; then
  # JSON output to stdout (for testing / external consumers).
  printf '{"window_days": %s, "window_start": "%s", "window_end": "%s", "surfaces": [\n' \
    "$FLAKE_WINDOW_DAYS" "$SINCE_ISO" "$NOW_ISO"
  first=true
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r slug name target trig _ <<<"$s"
    flakes=$(cat "/tmp/flake_count_$slug.$$")
    attempts=$(cat "/tmp/flake_attempts_$slug.$$")
    rate=$(compute_rate "$flakes" "$attempts")
    status=$(status_for "$flakes" "$attempts" "$target" "$trig")
    [[ "$first" == "true" ]] && first=false || printf ','
    printf '\n  {"surface":"%s","job":"%s","flakes":%s,"attempts":%s,"rate_pct":"%s","target_pct":%s,"trigger_count":%s,"status":"%s"}' \
      "$slug" "$name" "$flakes" "$attempts" "$rate" "$target" "$trig" "$status"
  done
  printf '\n]}\n'
  exit 0
fi

# Markdown dashboard.
DASHBOARD_TMP=$(mktemp)

{
  cat <<EOF
# Flake budget dashboard

Generated by [\`scripts/flake-counter.sh\`](../scripts/flake-counter.sh)
via [\`.github/workflows/flake-counter.yml\`](../.github/workflows/flake-counter.yml).
Budget definition lives in [\`docs/flake-budget.md\`](flake-budget.md).

- **Last updated:** \`$NOW_ISO\`
- **Window:** last $FLAKE_WINDOW_DAYS days (\`$SINCE_ISO\` → \`$NOW_ISO\`)
- **Repo:** \`$FLAKE_REPO\`
- **Workflow:** \`$FLAKE_WORKFLOW\`
- **Runs in window:** $run_count

## Current rates

| Surface | Window | Flakes | Attempts | Flake rate | Target | Status | Top flaking tests |
| --- | --- | --- | --- | --- | --- | --- | --- |
EOF

  for s in "${SURFACES[@]}"; do
    IFS='|' read -r slug name target trig _ <<<"$s"
    flakes=$(cat "/tmp/flake_count_$slug.$$")
    attempts=$(cat "/tmp/flake_attempts_$slug.$$")
    rate=$(compute_rate "$flakes" "$attempts")
    status=$(status_for "$flakes" "$attempts" "$target" "$trig")
    # Top-3 tests is a v2 enhancement (requires log parsing). For v1,
    # we surface the offending failure-run URLs as evidence.
    evidence_summary=""
    if [[ -s "/tmp/flake_events_$slug.$$" ]]; then
      n_events=$(wc -l < "/tmp/flake_events_$slug.$$" | tr -d ' ')
      evidence_summary="$n_events event(s) — see appendix"
    else
      evidence_summary="—"
    fi
    rate_display="$rate%"
    [[ "$rate" == "n/a" ]] && rate_display="n/a"
    printf '| %s | %s | %s | %s | %s | %s%% | %s | %s |\n' \
      "$name" "${FLAKE_WINDOW_DAYS}d" "$flakes" "$attempts" \
      "$rate_display" "$target" "$status" "$evidence_summary"
  done

  cat <<EOF

## Legend

- **green** — flake rate is at or below target.
- **yellow** — rate exceeds target but trigger-threshold not yet crossed.
- **red** — trigger threshold crossed. A \`flake-investigation\` issue
  was filed (or appended to) at the time of this dashboard write, if
  the counter was invoked with \`FLAKE_OPEN_ISSUES=true\`.
- **no-data** — surface had no attempts in the window (e.g. all PRs in
  the window were path-filtered out of the surface's job).

## Appendix — flake events in window

EOF

  any_events=false
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r slug name _ _ _ <<<"$s"
    if [[ -s "/tmp/flake_events_$slug.$$" ]]; then
      any_events=true
      echo "### $name"
      echo
      echo "| head_sha | branch | failure run |"
      echo "| --- | --- | --- |"
      while IFS=$'\t' read -r ev_sha ev_branch ev_url; do
        sha_short="${ev_sha:0:10}"
        printf '| %s | %s | [link](%s) |\n' "$sha_short" "$ev_branch" "$ev_url"
      done < "/tmp/flake_events_$slug.$$"
      echo
    fi
  done

  if [[ "$any_events" == "false" ]]; then
    echo "_No flake events detected in window._"
    echo
  fi

  cat <<EOF
## Methodology

- Walks every workflow run of \`$FLAKE_WORKFLOW\` in the last
  $FLAKE_WINDOW_DAYS days via the GitHub Actions REST API.
- For each \`head_sha\`, fetches per-attempt job data via
  \`/actions/runs/{id}/attempts/{n}/jobs\`.
- Counts one **flake event** per \`(head_sha, surface)\` pair where
  the surface job failed on an earlier attempt and succeeded on a
  later attempt (same SHA, no code change in between).
- **Integration surface only (slice 420):** ALSO counts an A->A+1
  flake — the \`$INTEGRATION_JOB_NAME\` job red on one push and green on
  the very next push to the same branch — but only when the A+1 diff did
  NOT touch the integration test surface (a diff that did is a
  fix-forward, not a flake). This catches rerun-cleared timing flakes
  (e.g. the scheduler goroutine race) that the same-SHA rule missed. The
  broadening is integration-scoped; the unit / vitest / Playwright
  surfaces keep the same-SHA-only v1 rule.
- Aggregates flake events and total attempts per surface; the rate is
  \`flakes / attempts * 100\`.
- "Skipped" job conclusions (path-filter stubs) do not count toward
  attempts.

See [\`docs/flake-budget.md\`](flake-budget.md) for the budget
definition this dashboard reports against.
EOF
} > "$DASHBOARD_TMP"

if [[ "$FLAKE_DRY_RUN" == "true" ]]; then
  cat "$DASHBOARD_TMP"
  log "dry-run: dashboard NOT written to $FLAKE_DASHBOARD_PATH"
else
  mkdir -p "$(dirname "$FLAKE_DASHBOARD_PATH")"
  mv "$DASHBOARD_TMP" "$FLAKE_DASHBOARD_PATH"
  log "wrote $FLAKE_DASHBOARD_PATH"
fi

# ---------- trigger automation: file/append issue on red ----------

if [[ "$FLAKE_OPEN_ISSUES" == "true" ]]; then
  for s in "${SURFACES[@]}"; do
    IFS='|' read -r slug name _ trig _ <<<"$s"
    flakes=$(cat "/tmp/flake_count_$slug.$$")
    if [[ "$flakes" -ge "$trig" && "$flakes" -gt 0 ]]; then
      log "trigger fired for surface $slug ($flakes >= $trig)"

      # Idempotency: search for an open `flake-investigation` issue
      # whose title contains this surface slug. Append a comment if
      # found, else file a new one.
      existing=$(gh issue list \
        --repo "$FLAKE_REPO" \
        --label flake-investigation \
        --state open \
        --json number,title \
        --jq ".[] | select(.title | contains(\"$slug\")) | .number" \
        | head -n 1 \
        || true)

      body_file=$(mktemp)
      {
        echo "**Surface:** \`$name\`"
        echo "**Flake count in window ($FLAKE_WINDOW_DAYS d):** $flakes"
        echo "**Trigger threshold:** $trig"
        echo "**Dashboard:** [\`docs/flake-budget-dashboard.md\`](../blob/main/$FLAKE_DASHBOARD_PATH)"
        echo
        echo "## Flake events"
        echo
        if [[ -s "/tmp/flake_events_$slug.$$" ]]; then
          while IFS=$'\t' read -r ev_sha ev_branch ev_url; do
            sha_short="${ev_sha:0:10}"
            echo "- \`$sha_short\` (branch \`$ev_branch\`) — $ev_url"
          done < "/tmp/flake_events_$slug.$$"
        fi
        echo
        echo "## Suggested next step"
        echo
        echo "- File a flake-investigation slice following the slice 340 pattern:"
        echo "  diagnose root cause, record in \`docs/audit-log/<slice>-...-decisions.md\`."
        echo "- Close this issue when the underlying flake is fixed or quarantined with rationale."
        echo
        echo "Filed automatically by [\`scripts/flake-counter.sh\`](../blob/main/scripts/flake-counter.sh)."
      } > "$body_file"

      if [[ -n "$existing" ]]; then
        log "appending comment to existing issue #$existing"
        gh issue comment "$existing" --repo "$FLAKE_REPO" --body-file "$body_file" >/dev/null
      else
        # Ensure label exists. `gh label create` is idempotent via 422
        # but we ignore non-zero to keep going.
        gh label create flake-investigation \
          --repo "$FLAKE_REPO" \
          --color FBCA04 \
          --description "Filed by scripts/flake-counter.sh when a surface crosses its trigger threshold (slice 352)" \
          2>/dev/null || true
        log "filing new issue for surface $slug"
        gh issue create \
          --repo "$FLAKE_REPO" \
          --title "flake-investigation: $slug — $flakes flakes in ${FLAKE_WINDOW_DAYS}d window" \
          --label flake-investigation \
          --body-file "$body_file" \
          >/dev/null
      fi
      rm -f "$body_file"
    fi
  done
fi

log "done"
