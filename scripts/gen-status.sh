#!/usr/bin/env bash
#
# gen-status.sh — derive slice status from ground truth, materialize _STATUS.md.
#
# Slice status is a pure function of (git history + open PRs + branches + an
# append-only event log). This script DERIVES it instead of hand-authoring it,
# replacing the manual reconcile flow (Plans/prompts/06-status-reconcile.md).
#
#   merged     ← a non-`chore(status)` commit on the current branch closes/fixes NNN
#   in-review  ← an open PR on branch feat|fix/NNN-*
#   in-progress← a branch feat|fix/NNN-* with no open PR, not yet merged
#   <event>    ← docs/issues/_events.jsonl overlay for states git can't prove
#                (deferred / blocked / not-ready / abandoned / not-a-code-bug)
#   ready      ← filed, not merged, not in-flight, no blocking event (default)
#
# Precedence: (git-merged OR a `merged` event) > in-review > in-progress > other events > ready.
# git-merged is ground truth; a `merged` event is an explicit authoritative assertion
# (used to backfill pre-convention merges that git can't prove, e.g. the v1 backlog).
#
# NON-DESTRUCTIVE: writes docs/issues/_STATUS.generated.md by default. It does NOT
# touch the historical _STATUS.md. Adopting the generated file (archive history →
# swap) is an explicit opt-in migration; see `just status` / the README note.
#
# Portable to bash 3.2 (macOS default) — keyed logic lives in awk, not assoc arrays.
#
# Offline-testable via env injection (mirrors scripts/check-action-pins.sh):
#   ATLAS_ISSUES_DIR     issues dir            (default: <repo>/docs/issues)
#   ATLAS_GIT_LOG_FILE   pre-captured git log  (lines: <hash>|<date>|<subject>)
#   ATLAS_OPEN_PRS_FILE  pre-captured gh JSON  (gh pr list --json number,headRefName)
#   ATLAS_BRANCHES_FILE  pre-captured branches (one short ref name per line)
#   ATLAS_EVENTS_FILE    event log             (default: <issues>/_events.jsonl)
#   ATLAS_STATUS_OUT     output path           (default: <issues>/_STATUS.generated.md)
#   ATLAS_NOW            generation date       (default: today; pin for deterministic tests)
#
# Flags: --stdout  (write to stdout instead of the output file)
#        --summary (counts + ready-set + in-flight only; omit the per-slice table)
#
# Run: bash scripts/gen-status.sh [--stdout]
# Exit: 0 on success, 2 on environment error.

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ISSUES_DIR="${ATLAS_ISSUES_DIR:-$ROOT/docs/issues}"
EVENTS_FILE="${ATLAS_EVENTS_FILE:-$ISSUES_DIR/_events.jsonl}"
STATUS_OUT="${ATLAS_STATUS_OUT:-$ISSUES_DIR/_STATUS.md}"
NOW="${ATLAS_NOW:-$(date +%Y-%m-%d)}"

TO_STDOUT=0
SUMMARY=0
for arg in "$@"; do
  case "$arg" in
    --stdout)  TO_STDOUT=1 ;;
    --summary) SUMMARY=1 ;;   # counts + ready-set + in-flight only (no per-slice table)
    *) echo "gen-status: unknown argument: $arg" >&2; exit 2 ;;
  esac
done

if [ ! -d "$ISSUES_DIR" ]; then
  echo "gen-status: issues dir not found: $ISSUES_DIR" >&2
  exit 2
fi

# ---- data sources (real, or injected for offline tests) --------------------

git_log_lines() {
  if [ -n "${ATLAS_GIT_LOG_FILE:-}" ]; then
    cat "$ATLAS_GIT_LOG_FILE"
  else
    git -C "$ROOT" log --format='%h|%cs|%s'
  fi
}

open_prs_json() {
  if [ -n "${ATLAS_OPEN_PRS_FILE:-}" ]; then
    cat "$ATLAS_OPEN_PRS_FILE"
  elif command -v gh >/dev/null 2>&1; then
    gh pr list --state open --limit 1000 --json number,headRefName 2>/dev/null || echo '[]'
  else
    echo '[]'
  fi
}

branch_names() {
  if [ -n "${ATLAS_BRANCHES_FILE:-}" ]; then
    cat "$ATLAS_BRANCHES_FILE"
  else
    git -C "$ROOT" branch -a --format='%(refname:short)' 2>/dev/null || true
    git -C "$ROOT" worktree list --porcelain 2>/dev/null | sed -n 's/^branch refs\/heads\///p' || true
  fi
}

# ---- normalize all sources into one tagged, tab-separated stream -----------
# Tags: U=universe  M=merged  R=in-review  P=in-progress  E=event

stream() {
  # universe of filed slices
  shopt -s nullglob
  for f in "$ISSUES_DIR"/[0-9]*.md; do
    base="${f##*/}"
    prefix="${base%%-*}"
    key=$((10#$prefix))
    # first heading only — sed quits at the first match (no full-file read, no head fork)
    title="$(sed -En '/^# /{s/^# *//;s/^[0-9]+ *[—–-]+ *//;p;q;}' "$f" | tr '\t|' '  ' | tr -s ' ')"
    printf 'U\t%s\t%s\t%s\n' "$key" "$prefix" "$title"
  done
  shopt -u nullglob

  # merged — the convention is `type(scope): slice NNN — …` (the implementation merge).
  # Exclude `chore(status):` (bookkeeping) and `docs(issues): add|file slice…` (filing,
  # not implementation — "add"/"file" before "slice" breaks the anchor below). Also honor
  # explicit plural `closes|fixes NNN` for ride-along closures (e.g. "fixes 673"); the
  # plural avoids matching descriptive "fix 474" in filing-commit subjects.
  git_log_lines | awk -F'|' '
    function emit(n) { printf "M\t%d\t%s\t%s\t%s\n", n+0, pr, date, hash }
    { hash=$1; date=$2; subj=$3; low=tolower(subj) }
    low ~ /^chore\(status\)/ { next }       # status bookkeeping, never a merge
    low ~ /^docs\(issues\): (add|file) / { next }  # slice FILING, not implementation
    {
      pr = ""
      if (match(low, /\(#[0-9]+\)/)) pr = substr(low, RSTART+2, RLENGTH-3)
      # A) anchored implementation: ^type(scope): slice NNN
      if (match(low, /^[a-z]+(\([^)]*\))?: slice [0-9]+/)) {
        rest = substr(low, RSTART, RLENGTH); sub(/.* slice /, "", rest); emit(rest)
      }
      # B) explicit ride-along closures
      s = low
      while (match(s, /(closes|fixes) #?[0-9]+/)) {
        tok = substr(s, RSTART, RLENGTH); n = tok; gsub(/[^0-9]/, "", n); emit(n)
        s = substr(s, RSTART + RLENGTH)
      }
    }'

  # in-review — open feat|fix/NNN-* PRs
  open_prs_json | jq -r '.[] | "\(.number)\t\(.headRefName)"' 2>/dev/null \
    | awk -F'\t' '$2 ~ /^(feat|fix)\/[0-9]+-/ {
        n=$2; sub(/^(feat|fix)\//,"",n); sub(/-.*/,"",n); printf "R\t%d\t%s\n", n+0, $1 }'

  # in-progress — feat|fix/NNN-* branches (merged/in-review filtered later in awk)
  branch_names | sed 's#^origin/##' \
    | awk '$0 ~ /^(feat|fix)\/[0-9]+-/ {
        n=$0; sub(/^(feat|fix)\//,"",n); sub(/-.*/,"",n); printf "P\t%d\t%s\n", n+0, $0 }'

  # events — last per slice wins (file order)
  if [ -f "$EVENTS_FILE" ]; then
    jq -r 'select(.slice!=null) | "E\t\(.slice)\t\(.to)\t\(.note // "" | gsub("\t";" "))\t\(.ref // "")"' \
      "$EVENTS_FILE" 2>/dev/null || true
  fi
}

# ---- compose + render (awk owns the keyed state) ---------------------------

render() {
  stream | awk -F'\t' -v now="$NOW" -v summary="$SUMMARY" '
    $1=="U" { key=$2+0; uni[key]=1; if(key>maxk)maxk=key; disp[key]=$3; title[key]=$4 }
    $1=="M" { key=$2+0; if(!(key in mrg)){ mrg[key]=1; mpr[key]=$3; mdate[key]=$4; mhash[key]=$5 } }
    $1=="R" { key=$2+0; rpr[key]=$3 }
    $1=="P" { key=$2+0; if(!(key in pbr)) pbr[key]=$3 }
    $1=="E" { key=$2+0; est[key]=$3; enote[key]=$4; eref[key]=$5 }
    END {
      # compose final state per slice
      for (k=1; k<=maxk; k++) {
        if (!(k in uni)) continue
        if (k in mrg)              { st[k]="merged";      pr[k]=mpr[k];  md[k]=mdate[k]; nt[k]=mhash[k] }
        else if ((k in est) && est[k]=="merged") { st[k]="merged"; pr[k]=eref[k]; md[k]=""; nt[k]=enote[k] }
        else if (k in rpr)         { st[k]="in-review";   pr[k]=rpr[k];  md[k]="";        nt[k]="" }
        else if (k in pbr)         { st[k]="in-progress"; pr[k]="";      md[k]="";        nt[k]=pbr[k] }
        else if (k in est)         { st[k]=est[k];        pr[k]=eref[k]; md[k]="";        nt[k]=enote[k] }
        else                       { st[k]="ready";       pr[k]="";      md[k]="";        nt[k]="" }
        cnt[st[k]]++; total++
      }

      if (summary == "1") {
        print "# Slice status"
        print ""
        print "> Live program build status, derived from git history. Regenerated on every docs build."
      } else {
        print "# v1 Slice Status — GENERATED"
        print ""
        print "> **GENERATED FILE — do not edit by hand.** Produced by `scripts/gen-status.sh`"
        print "> (`just status`). State is derived from git history + open PRs + branches +"
        print "> `_events.jsonl`. To change a slice'\''s state: merge a PR, push a branch, or append"
        print "> an event with `just event <slice> <state> [note]` — never edit this file."
        print ">"
        print "> Precedence: (git-merged or a \`merged\` event) > in-review > in-progress > other events > ready."
      }
      print ""
      printf "**Generated:** %s · **Total slices:** %d\n\n", now, total

      print "## Counts\n"
      print "| State | Count |"
      print "| --- | --- |"
      norder = split("merged in-review in-progress ready not-ready blocked deferred abandoned not-a-code-bug", order, " ")
      for (i=1; i<=norder; i++) if (cnt[order[i]] > 0) printf "| %s | %d |\n", order[i], cnt[order[i]]
      print ""

      print "## Ready set\n"
      if (summary == "1") {
        printf "**%d** slice%s ready to pick up.\n", cnt["ready"]+0, (cnt["ready"]==1 ? "" : "s")
      } else {
        rs=""
        for (k=1; k<=maxk; k++) if (k in uni && st[k]=="ready") rs = rs disp[k] " "
        if (rs=="") print "_(none)_"; else { sub(/ $/,"",rs); print rs }
      }
      print ""

      print "## In-flight\n"
      print "| Slice | State | PR / Branch |"
      print "| --- | --- | --- |"
      for (k=1; k<=maxk; k++) {
        if (!(k in uni)) continue
        if (st[k]=="in-review")   printf "| %s | in-review | #%s |\n", disp[k], pr[k]
        if (st[k]=="in-progress") printf "| %s | in-progress | %s |\n", disp[k], nt[k]
      }
      print ""

      if (summary != "1") {
        print "## All slices\n"
        print "| Slice | Title | State | PR | Merged | Ref/Note |"
        print "| --- | --- | --- | --- | --- | --- |"
        for (k=1; k<=maxk; k++) {
          if (!(k in uni)) continue
          t=title[k]; if (length(t)>64) t=substr(t,1,61) "..."
          p=pr[k]; if (p!="" && substr(p,1,1)!="#") p="#" p
          printf "| %s | %s | %s | %s | %s | %s |\n", disp[k], t, st[k], p, md[k], nt[k]
        }
      }
    }'
}

# Drop any invalid UTF-8 bytes that slipped in from a slice-file title, so every
# consumer (committed _STATUS.md, the published docs page, status-check, and Python's
# strict UTF-8 decode in the mkdocs hook) gets well-formed output. One pass, whole doc.
if command -v iconv >/dev/null 2>&1; then
  sanitize() { iconv -f UTF-8 -t UTF-8 -c; }
else
  sanitize() { cat; }
fi

if [ "$TO_STDOUT" -eq 1 ]; then
  render | sanitize
else
  render | sanitize > "$STATUS_OUT"
  echo "gen-status: wrote $STATUS_OUT"
fi
