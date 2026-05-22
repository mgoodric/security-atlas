#!/usr/bin/env bash
#
# edge-image-cleanup.sh — prune accumulated `:main-<sha7>` tags from
# the GHCR packages built by `.github/workflows/container-publish.yml`
# on every push to `main`.
#
# Slice 207 — the edge channel publishes a new image per commit on
# `main`. Without pruning, the GHCR repository accumulates indefinitely.
# This script implements the slice-207 D2 hybrid retention policy:
#
#   KEEP a `:main-<sha7>` tag if EITHER condition is true:
#     - its created_at is younger than EDGE_RETENTION_DAYS days, OR
#     - it is among the most recent EDGE_RETENTION_KEEP_LAST tags.
#
#   DELETE all other `:main-<sha7>` tags. The `:edge` floating tag is
#   never deleted (Watchtower polls it; deleting it briefly breaks the
#   edge channel until the next main push restores it). The release
#   tags (`vX.Y.Z`, `X.Y`, `X`, `latest`) are never deleted (they're
#   the stable channel).
#
# WHY HYBRID (D2):
#   - "always keep < N days" alone can blow the registry budget when
#     `main` is hot (slice 196's morning shipped 12 commits in 4h — the
#     2-week retention floor accumulates 168 tags before the first
#     deletion fires).
#   - "always keep last K tags" alone can prune a tag the operator is
#     STILL rolling back from when `main` has been quiet for a month
#     and then ships 50 commits in a day.
#   - Hybrid: bounded growth (the "last K" floor) AND grace period for
#     active rollbacks (the "< N days" floor).
#
# USAGE:
#   GH_TOKEN=<token-with-package:write> ./scripts/edge-image-cleanup.sh
#   ./scripts/edge-image-cleanup.sh --dry-run     # print what would be deleted
#   ./scripts/edge-image-cleanup.sh --verbose     # extra tracing
#
#   The scheduled `.github/workflows/edge-image-prune.yml` workflow
#   invokes this script weekly with the workflow's GITHUB_TOKEN, which
#   has packages:write on the org via the `permissions:` block in the
#   workflow.
#
# ENV:
#   GH_TOKEN                   GitHub PAT or workflow token with
#                              `read:packages` + `delete:packages`
#                              scope. Required.
#   EDGE_RETENTION_DAYS        Keep all tags created within this many
#                              days (default: 30).
#   EDGE_RETENTION_KEEP_LAST   Keep this many most-recent tags
#                              regardless of age (default: 50).
#   EDGE_REGISTRY_OWNER        GHCR owner / org (default: mgoodric).
#   EDGE_PACKAGES              Comma-separated package names within
#                              the owner namespace (default:
#                              "security-atlas,security-atlas-cli,
#                              security-atlas-web,
#                              security-atlas-bootstrap" — the four
#                              images container-publish.yml builds).
#   EDGE_DRY_RUN               If "true" (or the --dry-run flag is
#                              passed), prints what would be deleted
#                              and exits without calling DELETE.
#
# REQUIRES:
#   - `gh` CLI on PATH (used for the GHCR REST API calls so this
#     script doesn't reimplement bearer auth).
#   - `jq` on PATH (every CI runner has it; macOS via brew).
#   - bash 3.2+ (macOS default).
#
# EXIT:
#   0 — pruning completed (or dry-run completed without errors).
#   1 — pruning failed (auth, API rate limit, transport error).
#   2 — environment misconfigured (missing dependencies, missing
#       GH_TOKEN for non-dry-run).

set -Eeuo pipefail

# ------------------------------------------------------------------
# Argument parsing.
# ------------------------------------------------------------------
DRY_RUN="${EDGE_DRY_RUN:-false}"
VERBOSE=false
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    --verbose) VERBOSE=true ;;
    -h|--help)
      sed -n '1,/^set -Eeuo pipefail/p' "$0" | sed -e '$d' | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "edge-image-cleanup: unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

# ------------------------------------------------------------------
# Dependency checks.
# ------------------------------------------------------------------
for tool in gh jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "edge-image-cleanup: required tool '$tool' not found on PATH" >&2
    exit 2
  fi
done

# ------------------------------------------------------------------
# Defaults + env-overrides.
# ------------------------------------------------------------------
RETENTION_DAYS="${EDGE_RETENTION_DAYS:-30}"
KEEP_LAST="${EDGE_RETENTION_KEEP_LAST:-50}"
OWNER="${EDGE_REGISTRY_OWNER:-mgoodric}"
PACKAGES="${EDGE_PACKAGES:-security-atlas,security-atlas-cli,security-atlas-web,security-atlas-bootstrap}"

# Validate numeric inputs early so a typo doesn't silently delete
# everything (a passed `EDGE_RETENTION_DAYS=` would arithmetic-expand
# to 0 and prune the entire history).
if ! [[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]] || [[ "$RETENTION_DAYS" -lt 1 ]]; then
  echo "edge-image-cleanup: EDGE_RETENTION_DAYS must be a positive integer (got: $RETENTION_DAYS)" >&2
  exit 2
fi
if ! [[ "$KEEP_LAST" =~ ^[0-9]+$ ]] || [[ "$KEEP_LAST" -lt 1 ]]; then
  echo "edge-image-cleanup: EDGE_RETENTION_KEEP_LAST must be a positive integer (got: $KEEP_LAST)" >&2
  exit 2
fi

if [[ "$DRY_RUN" != "true" && -z "${GH_TOKEN:-}" ]]; then
  echo "edge-image-cleanup: GH_TOKEN is required for non-dry-run mode (set EDGE_DRY_RUN=true to skip)" >&2
  exit 2
fi

# Compute the cutoff timestamp in epoch seconds. `date -d` is GNU; macOS
# uses `date -j -f`. We try GNU first and fall back to BSD.
if cutoff_epoch="$(date -u -d "${RETENTION_DAYS} days ago" +%s 2>/dev/null)"; then
  :
else
  # BSD date (macOS): subtract days from now.
  now_epoch="$(date -u +%s)"
  cutoff_epoch=$(( now_epoch - RETENTION_DAYS * 86400 ))
fi

if [[ "$VERBOSE" == "true" ]]; then
  echo "edge-image-cleanup: owner=$OWNER"
  echo "edge-image-cleanup: packages=$PACKAGES"
  echo "edge-image-cleanup: retention_days=$RETENTION_DAYS (cutoff epoch: $cutoff_epoch)"
  echo "edge-image-cleanup: keep_last=$KEEP_LAST"
  echo "edge-image-cleanup: dry_run=$DRY_RUN"
fi

# ------------------------------------------------------------------
# Per-package pruning loop.
# ------------------------------------------------------------------
total_deleted=0
total_kept=0
total_skipped_non_main_sha=0

# IFS-split the comma-separated package list portably.
IFS=',' read -r -a pkg_array <<<"$PACKAGES"

for pkg in "${pkg_array[@]}"; do
  pkg="${pkg// /}" # trim any whitespace
  [[ -z "$pkg" ]] && continue

  echo "=== package: ${OWNER}/${pkg} ==="

  # List all versions of the container package. The user-vs-org
  # endpoint differs — we use the user endpoint because the owner is
  # a user account (mgoodric). If the owner is an org, swap `/users/`
  # for `/orgs/`.
  #
  # The API returns at most 100 per page; we paginate with `--paginate`.
  #
  # Capture stdout + stderr separately so a non-zero exit surfaces the
  # gh-cli error text on stderr WITHOUT contaminating stdout's JSON
  # (which `jq` parses below). The `set -e` in effect at this scope
  # bypasses the implicit `pipefail` for command-substitution
  # assignment, so we explicitly check $? after the call.
  versions_json=""
  if ! versions_json="$(
        gh api \
          --paginate \
          -H "Accept: application/vnd.github+json" \
          "/users/${OWNER}/packages/container/${pkg}/versions" \
          2>/tmp/edge-image-cleanup-gh-err.$$
      )"; then
    echo "edge-image-cleanup: gh api failed for ${OWNER}/${pkg}" >&2
    cat /tmp/edge-image-cleanup-gh-err.$$ >&2 || true
    rm -f /tmp/edge-image-cleanup-gh-err.$$
    exit 1
  fi
  rm -f /tmp/edge-image-cleanup-gh-err.$$

  # Filter versions that contain at least one `:main-<sha7>` tag. A
  # version may have MULTIPLE tags (e.g. `:edge` AND `:main-abc1234`)
  # — we never delete the version if it also carries `:edge` or a
  # versioned release tag (`v*.*.*` / `latest`). Use jq to select
  # ONLY versions whose `metadata.container.tags` array is composed
  # ENTIRELY of `main-<sha7>` tags (plus optional empty), so the
  # `:edge` floating tag is always preserved and so is anything that
  # also carries a release tag.
  candidates="$(
    jq -c '
      [.[] | select(
        (.metadata.container.tags // []) | length > 0 and
        all(. as $t | $t | test("^main-[0-9a-f]{7,40}$"))
      )]
    ' <<<"$versions_json"
  )"

  # Total tags inspected (informational only).
  total_versions=$(jq 'length' <<<"$versions_json")
  candidate_count=$(jq 'length' <<<"$candidates")

  if [[ "$VERBOSE" == "true" ]]; then
    echo "  inspected ${total_versions} version(s) total"
    echo "  identified ${candidate_count} candidate ':main-<sha7>'-only version(s)"
  fi
  total_skipped_non_main_sha=$(( total_skipped_non_main_sha + total_versions - candidate_count ))

  # Sort candidates by created_at DESC so index 0 is the newest.
  sorted="$(jq -c 'sort_by(.created_at) | reverse' <<<"$candidates")"

  # Decide per-version: keep or delete.
  #
  # Keep if ANY of:
  #   - rank < KEEP_LAST (index 0..K-1 are the K newest)
  #   - created_at >= cutoff_epoch (younger than RETENTION_DAYS days)
  #
  # Delete otherwise.
  delete_count=0
  keep_count=0
  while IFS=$'\t' read -r idx version_id created_at tags_csv; do
    [[ -z "$version_id" ]] && continue
    # Parse created_at to epoch.
    if version_epoch="$(date -u -d "$created_at" +%s 2>/dev/null)"; then
      :
    else
      # BSD date — RFC3339 input.
      version_epoch="$(date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$created_at" +%s 2>/dev/null || echo 0)"
    fi

    keep=false
    if [[ "$idx" -lt "$KEEP_LAST" ]]; then
      keep=true
      keep_reason="recency-floor (rank $idx < $KEEP_LAST)"
    elif [[ "$version_epoch" -ge "$cutoff_epoch" ]]; then
      keep=true
      keep_reason="age-floor (created_at=$created_at within ${RETENTION_DAYS}d)"
    fi

    if [[ "$keep" == "true" ]]; then
      keep_count=$(( keep_count + 1 ))
      if [[ "$VERBOSE" == "true" ]]; then
        echo "  KEEP   ${pkg}:${tags_csv} (id=${version_id}) — ${keep_reason}"
      fi
    else
      delete_count=$(( delete_count + 1 ))
      if [[ "$DRY_RUN" == "true" ]]; then
        echo "  WOULD DELETE ${pkg}:${tags_csv} (id=${version_id}, created_at=${created_at})"
      else
        echo "  DELETE ${pkg}:${tags_csv} (id=${version_id}, created_at=${created_at})"
        gh api \
          --method DELETE \
          -H "Accept: application/vnd.github+json" \
          "/users/${OWNER}/packages/container/${pkg}/versions/${version_id}" \
          >/dev/null || {
            echo "edge-image-cleanup: DELETE failed for ${pkg} version ${version_id}" >&2
            exit 1
          }
      fi
    fi
  done < <(
    jq -r 'to_entries | .[] | [.key, .value.id, .value.created_at, (.value.metadata.container.tags // [] | join(","))] | @tsv' <<<"$sorted"
  )

  echo "  ${pkg}: kept ${keep_count}, deleted ${delete_count}"
  total_deleted=$(( total_deleted + delete_count ))
  total_kept=$(( total_kept + keep_count ))
done

echo ""
echo "edge-image-cleanup: total kept=${total_kept}, deleted=${total_deleted}, skipped (non-:main-<sha7>)=${total_skipped_non_main_sha}"
if [[ "$DRY_RUN" == "true" ]]; then
  echo "edge-image-cleanup: DRY RUN — no DELETE calls were made."
fi
