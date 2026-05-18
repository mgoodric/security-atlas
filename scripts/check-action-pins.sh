#!/usr/bin/env bash
#
# check-action-pins.sh — assert every `uses:` line in every workflow
# under `.github/workflows/*.yml` pins its action to a 40-character
# commit SHA (not a floating tag). Exits non-zero on any violation.
#
# Slice 128 — this is the local-repro script for AC-2 and the worker
# that the `actions-pin-check` CI job in `.github/workflows/ci.yml`
# invokes. Both surfaces use the same comparison logic so contributors
# can reproduce a CI finding by running this script locally.
#
# Why it exists:
#   Tag-pinned actions are subject to the well-known tag-jacking
#   supply-chain attack class: an attacker who compromises an action's
#   git push permissions can move a floating tag like `v6` to point at
#   malicious code, and every consumer pinned to `@v6` silently picks it
#   up on the next CI run. SHA pins are immutable; an attacker cannot
#   retroactively change what `@<sha>` resolves to. Slice 117 already
#   pinned `step-security/harden-runner` to a SHA; slice 128 extended
#   that discipline to every action in every workflow. This script
#   guards the discipline by failing CI on any future regression.
#
# Scope: every `uses:` line found by `grep -hE '^[[:space:]]+(- )?uses: '`
# across `.github/workflows/*.yml`. Both YAML styles are accepted:
#   - the named-step form `        uses: foo/bar@<sha>`
#   - the compact list-item form `      - uses: foo/bar@<sha>`
# The check is "is the reference after the `@` a 40-character
# lowercase-hex string?". The optional trailing `# <tag>` comment is
# allowed and recommended (so Dependabot's `# <tag>` lookup convention
# still works) but the SHA itself is what the check enforces.
#
# Output:
#   - On success (no tag-pinned actions): one line
#     "no tag-pinned actions detected (N pinned across M files)" + exit 0.
#   - On violation: prints every offending line with file path + line
#     number + the offending `uses: ...` text + a reconcile hint, then
#     exits 1.
#
# Env:
#   ATLAS_WORKFLOWS_DIR  Override the workflows directory the script
#                        scans (default: $REPO/.github/workflows).
#                        Used by the test harness to point at a
#                        synthetic fixture tree without polluting the
#                        real in-tree files.
#
# Required:
#   - `grep` on PATH (every POSIX system has it)
#   - `awk` on PATH (every POSIX system has it)
#   - bash 3.2+ (macOS default)
#
# Exit codes:
#   0 — every `uses:` line in every workflow is SHA-pinned
#   1 — one or more `uses:` lines reference a non-SHA tag
#   2 — environment misconfigured (workflows dir missing, no .yml files)

set -Eeuo pipefail

# Resolve the workflows directory (unless overridden by the test env).
if [[ -n "${ATLAS_WORKFLOWS_DIR:-}" ]]; then
  WORKFLOWS_DIR="$ATLAS_WORKFLOWS_DIR"
else
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
  WORKFLOWS_DIR="$ROOT/.github/workflows"
fi

if [[ ! -d "$WORKFLOWS_DIR" ]]; then
  echo "check-action-pins: workflows directory does not exist: $WORKFLOWS_DIR" >&2
  exit 2
fi

# Glob the .yml files. Compgen handles the empty-glob case cleanly.
shopt -s nullglob
yml_files=("$WORKFLOWS_DIR"/*.yml)
shopt -u nullglob

if [[ ${#yml_files[@]} -eq 0 ]]; then
  echo "check-action-pins: no .yml files found in $WORKFLOWS_DIR" >&2
  exit 2
fi

# Walk every `uses:` line in every workflow. The regex extracts the
# `<repo>@<ref>` after the colon-space. Any ref that is NOT a 40-char
# lowercase hex string is a violation.
#
# Inline `# <tag>` comments are permitted (and recommended for human
# readability + Dependabot's update convention). The match strips the
# trailing comment before testing the ref.
#
# The shape we accept:
#   uses: <org>/<repo>@<40-char-sha>
#   uses: <org>/<repo>@<40-char-sha> # <anything>
#   uses: <org>/<repo>/<sub-path>@<40-char-sha>
#   uses: <org>/<repo>/<sub-path>@<40-char-sha> # <anything>
#
# Anything else (tag, branch, semver) is a violation.

violations=0
pinned=0

# We emit findings to a temp file so the final summary line is
# unambiguous on stdout while the per-line findings go to stderr.
findings_tmp="$(mktemp)"
trap 'rm -f "$findings_tmp"' EXIT

for f in "${yml_files[@]}"; do
  # grep -nE prints `<line>:<text>` for each match. We then iterate
  # and apply the SHA test. The grep pattern accepts BOTH YAML styles:
  # the named-step form `        uses: ...` and the compact list-item
  # form `      - uses: ...`.
  while IFS= read -r match; do
    [[ -z "$match" ]] && continue
    # match looks like: `48:        uses: actions/checkout@de0fac... # v6`
    #                OR: `53:      - uses: actions/checkout@de0fac... # v6`
    lineno="${match%%:*}"
    rest="${match#*:}"
    # Strip leading whitespace.
    rest="${rest#"${rest%%[![:space:]]*}"}"
    # Strip leading `- ` if present (compact list-item form).
    if [[ "$rest" == "- "* ]]; then
      rest="${rest#- }"
    fi
    # Extract the value after `uses: `.
    uses_val="${rest#uses: }"
    # Drop the trailing `# <tag>` comment if present, plus the
    # whitespace between the ref and the `#`.
    ref_part="${uses_val%% #*}"
    # ref_part is now `<repo>@<ref>` (no trailing comment).
    ref="${ref_part##*@}"
    # If `ref` is exactly 40 lowercase-hex chars, pass.
    if [[ "$ref" =~ ^[a-f0-9]{40}$ ]]; then
      pinned=$((pinned + 1))
    else
      violations=$((violations + 1))
      printf '%s:%s: uses: %s — ref %q is not a 40-char SHA\n' \
        "$f" "$lineno" "$ref_part" "$ref" >>"$findings_tmp"
    fi
  done < <(grep -nE '^[[:space:]]+(- )?uses: ' "$f" || true)
done

if [[ $violations -eq 0 ]]; then
  echo "check-action-pins: no tag-pinned actions detected (${pinned} pinned across ${#yml_files[@]} files)"
  exit 0
fi

echo "check-action-pins: ${violations} tag-pinned action(s) found (must be SHA-pinned)" >&2
echo "" >&2
cat "$findings_tmp" >&2
echo "" >&2
echo "To reconcile each finding:" >&2
echo "  1. Look up the action's current SHA for the tag, e.g.:" >&2
echo "       gh api repos/<owner>/<repo>/git/refs/tags/<tag> --jq '.object.sha'" >&2
echo "  2. If '.object.type' is 'tag' (annotated), dereference:" >&2
echo "       gh api repos/<owner>/<repo>/git/tags/<sha> --jq '.object.sha'" >&2
echo "  3. Replace the workflow line:" >&2
echo "       uses: <action>@<tag>  ->  uses: <action>@<40-char-sha> # <tag>" >&2
echo "" >&2
echo "Why: tag-pinned actions are exposed to the tag-jacking supply-chain" >&2
echo "attack class. SHA pins are immutable. See CONTRIBUTING.md \"Action" >&2
echo "pinning\" subsection + docs/audit-log/128-sha-pin-all-github-actions-decisions.md." >&2
exit 1
