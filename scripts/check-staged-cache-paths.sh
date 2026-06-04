#!/usr/bin/env bash
#
# check-staged-cache-paths.sh — fail when a STAGED path matches a known
# machine-local-cache shape (e.g. `.understand-anything/`). Exits non-zero
# on any violation with an actionable message.
#
# Slice 458 — this is BOTH the pre-commit local hook worker (AC-1) and the
# `cache-path-guard` CI job worker (AC-2). Both surfaces invoke this same
# script so a contributor can reproduce a CI finding locally.
#
# Why it exists:
#   Slice 433 added `.understand-anything/` to `.gitignore` and untracked
#   the ~9.8M cache, after discovering it had ALREADY been committed to
#   `main` (commit a058b09a) — swept in by a broad `git add .` alongside a
#   legitimate status reconcile, BEFORE the ignore rule landed. `.gitignore`
#   stops the FORWARD accident only for paths not yet tracked; it does not
#   catch the NEXT class of this accident: a different machine-local cache
#   (a new analysis tool, a fresh `.cache`-style dir, an editor project DB)
#   swept in by a broad `git add` before anyone thinks to add an ignore
#   rule. The durable fix is a guard at staging/commit time, not a per-tool
#   ignore rule chased after the fact. This script is that guard.
#
# What it does:
#   Inspects the set of paths under inspection (default: the STAGED set,
#   `git diff --cached --name-only --diff-filter=ACMR`) and fails if any
#   path falls under a known machine-local-cache directory prefix or matches
#   a known machine-local-cache filename. The denylist is intentionally
#   small and editable (DENY_DIRS / DENY_FILES below) — extend it when a new
#   per-machine cache shape is identified.
#
# AC-3 (no false positives): the guard is anchored to directory PREFIXES
#   (`.cache/` matches `.cache/foo` but NOT `web/app/.cache-warmer.ts`) and
#   exact basenames (`.DS_Store`). It does NOT re-implement or modify the
#   `.understand-anything/` `.gitignore` rule (slice 433 owns that — this is
#   a defense-in-depth staging guard, a separate surface).
#
# Modes (what set of paths to inspect):
#   * default / `--staged` : the git index (pre-commit hook + the natural
#     local surface). Uses `git diff --cached`.
#   * `--all-tracked`      : every tracked path (`git ls-files`). The CI
#     surface — a contributor who skipped local hooks committed a cache;
#     CI re-checks the whole tree so they are still caught (AC-2). This is
#     also how AC-3 is proven: run against the real tree, expect a clean
#     pass.
#   * `--args FILE...`     : check an explicit list of paths (used by the
#     pre-commit framework, which passes the staged filenames as argv, and
#     by the test harness). When invoked by pre-commit with `pass_filenames:
#     true`, argv IS the staged set, so this is the hook's real path.
#
# Output:
#   * On success: one summary line on stdout + exit 0.
#   * On violation: every offending path on stderr with the cache shape it
#     matched + a remediation hint, then exit 1.
#
# Env:
#   ATLAS_CACHE_GUARD_ROOT  Override the repo root the git commands run in
#                           (default: the script's parent dir). Used by the
#                           test harness to point at a synthetic fixture
#                           repo without touching the real tree.
#
# Exit codes:
#   0 — no staged/inspected path matches a machine-local-cache shape
#   1 — one or more paths match (must be unstaged + gitignored, not committed)
#   2 — usage error

set -Eeuo pipefail

# --------------------------------------------------------------------
# Denylist — the known machine-local-cache shapes. Directory prefixes
# (matched as `<dir>/...`) and exact basenames. Keep this small and
# obvious; extend it when a new per-machine cache shape is identified.
# Each entry here mirrors a `.gitignore` rule (slice 433 + the cache
# block in .gitignore) — the guard catches the path BEFORE it is
# committed; .gitignore catches it only while untracked.
# --------------------------------------------------------------------
DENY_DIRS=(
  ".understand-anything" # slice 433 — the cache that triggered this guard
  ".cache"               # generic tool cache
  ".ruff_cache"          # ruff
  ".mypy_cache"          # mypy
  ".pytest_cache"        # pytest
  ".idea"                # JetBrains project DB
  ".vscode"              # VS Code workspace state (project-shared cfg is rare; flag it)
  "node_modules"         # npm install tree (already gitignored; guarded too)
  ".next"                # Next.js build cache
  ".turbo"               # Turborepo cache
  ".gradle"              # Gradle cache
)
DENY_FILES=(
  ".DS_Store" # macOS Finder metadata
)

usage() {
  cat >&2 <<'USAGE'
usage: check-staged-cache-paths.sh [--staged | --all-tracked | --args FILE...]
  --staged       inspect the git index (default; pre-commit local surface)
  --all-tracked  inspect every tracked path (CI surface; AC-2)
  --args FILE... inspect the explicit FILE list (pre-commit pass_filenames)
USAGE
}

# Resolve the repo root (unless overridden by the test env).
if [[ -n "${ATLAS_CACHE_GUARD_ROOT:-}" ]]; then
  ROOT="$ATLAS_CACHE_GUARD_ROOT"
else
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
fi

mode="staged"
explicit_paths=()
case "${1:-}" in
"" | --staged) mode="staged" ;;
--all-tracked) mode="all-tracked" ;;
--args)
  mode="args"
  shift
  explicit_paths=("$@")
  ;;
-h | --help)
  usage
  exit 0
  ;;
*)
  # Bare path arguments (no flag) are treated as an explicit list too —
  # this is how the pre-commit framework invokes the hook (it appends the
  # staged filenames as argv with `pass_filenames: true`).
  mode="args"
  explicit_paths=("$@")
  ;;
esac

# Collect the paths to inspect.
paths=()
case "$mode" in
staged)
  # The staged set: added/copied/modified/renamed (not deleted). Run in
  # the repo root so the paths are repo-relative.
  while IFS= read -r p; do
    [[ -n "$p" ]] && paths+=("$p")
  done < <(git -C "$ROOT" diff --cached --name-only --diff-filter=ACMR 2>/dev/null || true)
  ;;
all-tracked)
  while IFS= read -r p; do
    [[ -n "$p" ]] && paths+=("$p")
  done < <(git -C "$ROOT" ls-files 2>/dev/null || true)
  ;;
args)
  paths=("${explicit_paths[@]:-}")
  ;;
esac

# Walk the paths and match each against the denylist.
violations=0
findings_tmp="$(mktemp)"
trap 'rm -f "$findings_tmp"' EXIT

is_violation() {
  local path="$1"
  local d f
  # Directory-prefix match: `<dir>/...` anywhere a path segment begins.
  # We match the path as a sequence of `/`-separated segments so that
  # `.cache/foo` and `web/.cache/foo` both match `.cache`, but
  # `web/app/.cache-warmer.ts` does NOT (`.cache` is not a full segment).
  for d in "${DENY_DIRS[@]}"; do
    # Leading-segment form: ".understand-anything/..."
    if [[ "$path" == "$d/"* ]]; then
      echo "$d/"
      return 0
    fi
    # Nested-segment form: ".../node_modules/..."
    if [[ "$path" == *"/$d/"* ]]; then
      echo "$d/"
      return 0
    fi
  done
  # Exact-basename match: `.DS_Store` anywhere.
  for f in "${DENY_FILES[@]}"; do
    if [[ "$path" == "$f" || "$path" == *"/$f" ]]; then
      echo "$f"
      return 0
    fi
  done
  return 1
}

for p in "${paths[@]:-}"; do
  [[ -z "$p" ]] && continue
  if matched="$(is_violation "$p")"; then
    violations=$((violations + 1))
    printf '%s  (matched machine-local-cache shape: %s)\n' "$p" "$matched" >>"$findings_tmp"
  fi
done

if [[ $violations -eq 0 ]]; then
  echo "check-staged-cache-paths: clean — no machine-local-cache paths in the inspected set (${mode})"
  exit 0
fi

{
  echo "check-staged-cache-paths: ${violations} machine-local-cache path(s) staged for commit:"
  echo ""
  cat "$findings_tmp"
  echo ""
  echo "This looks like a machine-local cache (per-developer tool/editor state),"
  echo "not a shared source artifact. It belongs in .gitignore, NOT in the tree."
  echo ""
  echo "To unstage and ignore it:"
  echo "  git restore --staged <path>            # remove from the commit"
  echo "  echo '<dir>/' >> .gitignore             # ignore it going forward"
  echo ""
  echo "Background: a 9.8M .understand-anything/ cache was once swept into main"
  echo "by a broad 'git add .' before its ignore rule landed (slice 433). This"
  echo "guard blocks the NEXT instance of that footgun at staging time."
  echo "If this path is genuinely a shared artifact, edit the DENY_DIRS /"
  echo "DENY_FILES denylist in scripts/check-staged-cache-paths.sh."
} >&2
exit 1
