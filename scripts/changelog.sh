#!/usr/bin/env bash
#
# changelog.sh — per-PR changelog fragments (#2357).
#
# Every PR with a user-visible change adds one file under changelog.d/
# instead of editing the top of CHANGELOG.md, so parallel PRs no longer
# conflict on it. The fragments are assembled into CHANGELOG.md at
# release time.
#
# A fragment is `changelog.d/<name>.<type>.md`:
#   <name>  the PR or issue number by convention (`2357`); any
#           [a-z0-9-] name is accepted (`2357-2`, `slice-279`).
#   <type>  one of the TYPES below; it picks the `### ` section.
#   body    one or more Markdown bullets, the first line starting `- `.
#
# Usage:
#   scripts/changelog.sh check            validate every fragment
#   scripts/changelog.sh render           print the assembled sections
#   scripts/changelog.sh assemble <ver>   write the sections under the
#                                         `## [<ver>]` heading of
#                                         CHANGELOG.md, delete fragments
#   scripts/changelog.sh require          CI gate: read a PR's changed
#                                         files on stdin, fail when a
#                                         user-visible PR adds no fragment
#
# `require` reads PR_LABELS (newline-separated), PR_AUTHOR and
# PR_HEAD_REF from the environment. A PR is exempt when it carries the
# `no-changelog` label, is opened by Dependabot, is a release-please PR,
# or changes only docs (*.md, docs/, docs-site/, Plans/). Each stdin
# line is `<status>\t<path>` with a GitHub PR-files status (added,
# modified, removed, renamed, ...); a bare `<path>` reads as modified.
# Only an added or modified fragment at the top of the directory counts.
#
# Env (for the self-test): CHANGELOG_DIR (default changelog.d),
# CHANGELOG_FILE (default CHANGELOG.md).
#
# Self-test: bash scripts/changelog_test.sh

set -euo pipefail
# A failure inside $(...) must abort too (bash >= 4.4; macOS /bin/bash is
# 3.2, so the explicit status checks below do not rely on it).
shopt -s inherit_errexit 2>/dev/null || true
# Byte order for sort and globbing, so output is the same on every host.
export LC_ALL=C

DIR="${CHANGELOG_DIR:-changelog.d}"
FILE="${CHANGELOG_FILE:-CHANGELOG.md}"

# Section order in the rendered changelog, and each type's heading.
TYPES="added changed deprecated removed fixed security performance documentation quality"
heading() {
  case "$1" in
    added) echo "Added" ;;
    changed) echo "Changed" ;;
    deprecated) echo "Deprecated" ;;
    removed) echo "Removed" ;;
    fixed) echo "Fixed" ;;
    security) echo "Security" ;;
    performance) echo "Performance" ;;
    documentation) echo "Documentation" ;;
    quality) echo "Quality" ;;
  esac
}

NAME_RE='^[a-z0-9][a-z0-9-]*\.(added|changed|deprecated|removed|fixed|security|performance|documentation|quality)\.md$'

# Fragment paths, excluding the directory's README.
fragments() {
  [[ -d "$DIR" ]] || return 0
  find "$DIR" -maxdepth 1 -type f ! -name README.md | sort
}

cmd_check() {
  local bad=0 f base first
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    base="$(basename "$f")"
    if [[ ! "$base" =~ $NAME_RE ]]; then
      echo "changelog: $f: name must be <name>.<type>.md, type one of: $TYPES" >&2
      bad=1
      continue
    fi
    first="$(grep -m1 -v '^[[:space:]]*$' "$f" || true)"
    if [[ "$first" != "- "* ]]; then
      echo "changelog: $f: must start with a Markdown bullet ('- ')" >&2
      bad=1
    fi
  done < <(fragments)
  return "$bad"
}

# Fragments of one type, newest first: a leading number sorts
# descending, names without one sort after, alphabetically.
of_type() {
  local type="$1" f base name num
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    base="$(basename "$f")"
    [[ "$base" == *".$type.md" ]] || continue
    name="${base%".$type.md"}"
    num="$(sed -n 's/^\([0-9][0-9]*\).*/\1/p' <<<"$name")"
    printf '%s\t%s\t%s\n' "${num:-0}" "$name" "$f"
  done < <(fragments) | sort -t "$(printf '\t')" -k1,1nr -k2,2 | cut -f3
}

# Every fragment render would print, in render order.
rendered_files() {
  local type
  for type in $TYPES; do
    of_type "$type"
  done
}

cmd_render() {
  cmd_check || return 1
  local type f files first=1
  for type in $TYPES; do
    files="$(of_type "$type")"
    [[ -n "$files" ]] || continue
    [[ $first -eq 1 ]] || echo
    first=0
    printf '### %s\n' "$(heading "$type")"
    while IFS= read -r f; do
      echo
      # Strip trailing blank lines so entries are separated by exactly one.
      sed -e :a -e '/^\n*$/{$d;N;ba' -e '}' "$f"
    done <<<"$files"
  done
}

cmd_assemble() {
  local version="${1:-}"
  if [[ -z "$version" ]]; then
    echo "usage: changelog.sh assemble <version>" >&2
    return 2
  fi
  local target="## [$version]"
  if ! grep -qF "$target" "$FILE"; then
    echo "changelog: no '$target' heading in $FILE; run this on the release PR branch" >&2
    return 1
  fi
  # Fail closed: an invalid fragment stops assemble before CHANGELOG.md
  # is touched or anything is deleted.
  cmd_check || return 1
  local body tmp files
  files="$(rendered_files)" || return 1
  body="$(cmd_render)" || return 1
  if [[ -z "$body" ]]; then
    echo "changelog: no fragments to assemble" >&2
    return 0
  fi
  tmp="$(mktemp)"
  # Insert after the heading line and the blank line that follows it.
  BODY="$body" TARGET="$target" awk '
    done == 0 && index($0, ENVIRON["TARGET"]) == 1 { print; print ""; print ENVIRON["BODY"]; done = 1; next }
    { print }
  ' "$FILE" >"$tmp"
  mv "$tmp" "$FILE"
  # Delete exactly the files that were rendered, nothing else.
  while IFS= read -r f; do
    [[ -z "$f" ]] || rm -- "$f"
  done <<<"$files"
  echo "changelog: assembled into $target"
}

is_docs() {
  case "$1" in
    *.md | docs/* | docs-site/* | Plans/*) return 0 ;;
    *) return 1 ;;
  esac
}

cmd_require() {
  local label
  while IFS= read -r label; do
    if [[ "$label" == "no-changelog" ]]; then
      echo "changelog: exempt (label no-changelog)"
      return 0
    fi
  done <<<"${PR_LABELS:-}"
  if [[ "${PR_AUTHOR:-}" == "dependabot[bot]" ]]; then
    echo "changelog: exempt (Dependabot PR)"
    return 0
  fi
  if [[ "${PR_HEAD_REF:-}" == release-please--* ]]; then
    echo "changelog: exempt (release-please PR)"
    return 0
  fi
  local line status f rest code=0 fragment=0
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    if [[ "$line" == *$'\t'* ]]; then
      status="${line%%$'\t'*}"
      f="${line#*$'\t'}"
    else
      status=modified
      f="$line"
    fi
    rest="${f#"$DIR/"}"
    if [[ "$f" == "$DIR/"* ]]; then
      # A fragment counts only when added or modified, directly in the
      # directory, with a valid name. Deletions and subdirectory files
      # do not, and neither does the README.
      if [[ "$status" == added || "$status" == modified ]] &&
        [[ "$rest" != */* && "$rest" =~ $NAME_RE ]]; then
        fragment=1
      fi
    elif ! is_docs "$f"; then
      code=1
    fi
  done
  if [[ $fragment -eq 1 ]]; then
    echo "changelog: fragment present"
    return 0
  fi
  if [[ $code -eq 0 ]]; then
    echo "changelog: exempt (docs-only change)"
    return 0
  fi
  cat >&2 <<EOF
changelog: this PR changes code but adds no fragment under $DIR/.
Add $DIR/<pr-or-issue>.<type>.md (see $DIR/README.md), or, if nothing
in it is user-visible, add the 'no-changelog' label and re-run this job.
EOF
  return 1
}

case "${1:-}" in
  check) cmd_check ;;
  render) cmd_render ;;
  assemble) cmd_assemble "${2:-}" ;;
  require) cmd_require ;;
  *)
    echo "usage: changelog.sh check|render|assemble <version>|require" >&2
    exit 2
    ;;
esac
