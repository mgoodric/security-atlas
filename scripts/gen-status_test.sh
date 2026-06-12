#!/usr/bin/env bash
#
# Smoke tests for scripts/gen-status.sh and scripts/slice-event.sh.
#
# Exercises the deriver against injected fixtures (git log / open PRs / branches /
# events) via the ATLAS_*_FILE env overrides, so it runs offline and never touches
# the real repo state. Cases cover each derivation path and the precedence rule:
#   merged > in-review > in-progress > event > ready.
#
# Run: bash scripts/gen-status_test.sh
# Exits non-zero on first failed assertion.

set -eu

DIR="$(cd "$(dirname "$0")" && pwd)"
GEN="$DIR/gen-status.sh"
EVT="$DIR/slice-event.sh"
[[ -x "$GEN" ]] || { echo "gen-status_test: $GEN not executable" >&2; exit 2; }
[[ -x "$EVT" ]] || { echo "gen-status_test: $EVT not executable" >&2; exit 2; }

pass=0
fail=0
fail_messages=()

assert_contains() {
  local haystack="$1" needle="$2" label="$3"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: expected to find >>$needle<<")
  fi
}
assert_not_contains() {
  local haystack="$1" needle="$2" label="$3"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    fail=$((fail + 1))
    fail_messages+=("$label: did NOT expect >>$needle<<")
  else
    pass=$((pass + 1))
  fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ISSUES="$TMP/issues"
mkdir -p "$ISSUES"
# universe: 5 filed slices
printf '# 100 — merged via closes\n' > "$ISSUES/100-merged-slice.md"
printf '# 101 — open PR in review\n'  > "$ISSUES/101-inreview-slice.md"
printf '# 102 — branch in progress\n' > "$ISSUES/102-inprogress-slice.md"
printf '# 103 — deferred via event\n' > "$ISSUES/103-deferred-slice.md"
printf '# 104 — untouched ready\n'    > "$ISSUES/104-ready-slice.md"
printf '# 105 — merged wins over event\n' > "$ISSUES/105-merged-over-event.md"
printf '# 106 — filed not implemented\n' > "$ISSUES/106-filed-not-merged.md"
printf '# 107 — merged event beats a stale branch\n' > "$ISSUES/107-event-beats-branch.md"

# git log fixture: 100 merged via anchored `slice 100`; 105 via anchored slice; a
# chore(status) noise line referencing 104 that must be IGNORED; and a docs(issues)
# FILING commit for 106 that must NOT mark 106 merged (it is filing, not implementation).
cat > "$TMP/gitlog" <<'EOF'
aaa1111|2026-06-01|feat(x): slice 100 — do the thing (#900)
ccc3333|2026-06-03|fix(z): slice 105 — another (#905)
bbb2222|2026-06-02|chore(status): batch 7 claim-stake (104 should be ignored) (#901)
ddd4444|2026-06-04|docs(issues): add slice 106 — initial spec, will fix 474 later (#906)
EOF

# open PRs fixture: 101 in review (feat/101-*); a dependabot PR that must be ignored
cat > "$TMP/prs.json" <<'EOF'
[{"number":910,"headRefName":"feat/101-open-pr-in-review"},
 {"number":911,"headRefName":"dependabot/go_modules/foo-1.2.3"}]
EOF

# branches fixture: 102 has a branch with no PR; 100 has a stale branch but is merged
cat > "$TMP/branches" <<'EOF'
main
feat/102-inprogress-slice
feat/100-merged-slice
feat/107-event-beats-branch
origin/fix/999-no-such-file
EOF

# events fixture: 103 deferred; 105 also has an event but git-merged must win
cat > "$TMP/events.jsonl" <<'EOF'
{"slice":103,"to":"deferred","ts":"2026-06-02","ref":"","note":"waiting on design"}
{"slice":105,"to":"blocked","ts":"2026-06-02","ref":"","note":"should be overridden by merge"}
{"slice":107,"to":"merged","ts":"2026-05-15","ref":"","note":"v1 backfill — beats stale branch"}
EOF

OUT="$(ATLAS_ISSUES_DIR="$ISSUES" \
      ATLAS_GIT_LOG_FILE="$TMP/gitlog" \
      ATLAS_OPEN_PRS_FILE="$TMP/prs.json" \
      ATLAS_BRANCHES_FILE="$TMP/branches" \
      ATLAS_EVENTS_FILE="$TMP/events.jsonl" \
      ATLAS_NOW="2026-06-10" \
      bash "$GEN" --stdout)"

# title comes from the slice file H1, not the commit subject
assert_contains "$OUT" "| 100 | merged via closes | merged | #900 | 2026-06-01 |" "100 merged via closes"
assert_contains "$OUT" "| 101 | open PR in review | in-review | #910 |"      "101 in-review from open PR"
# the In-flight section row proves both state and branch unambiguously
assert_contains "$OUT" "| 102 | in-progress | feat/102-inprogress-slice |"   "102 in-progress from branch"
assert_contains "$OUT" "| 103 | deferred via event | deferred |"            "103 deferred from event"
assert_contains "$OUT" "| 104 | untouched ready | ready |"                  "104 default ready"
assert_contains "$OUT" "| 105 | merged wins over event | merged | #905 |"   "105 merged beats event"
assert_not_contains "$OUT" "| 105 | merged wins over event | blocked"       "105 event must NOT win"
assert_contains "$OUT" "| 106 | filed not implemented | ready |"            "106 filing commit is NOT a merge"
# a `merged` event is authoritative — it beats a still-present branch (in-progress)
assert_contains "$OUT" "| 107 | merged event beats a stale branch | merged |" "107 merged-event beats branch"
assert_contains "$OUT" "104"                                                "104 appears in ready set"
assert_contains "$OUT" "GENERATED FILE — do not edit"                       "generated header present"
# 999 branch references a non-existent slice file → must not appear as a row
assert_not_contains "$OUT" "| 999 |"                                        "999 phantom slice excluded"

# slice-event.sh: append + validation
EV="$TMP/append.jsonl"
ATLAS_EVENTS_FILE="$EV" ATLAS_NOW="2026-06-10" bash "$EVT" 683 not-ready "edge lag" "#1261" >/dev/null
assert_contains "$(cat "$EV")" '"slice":683' "event appended slice"
assert_contains "$(cat "$EV")" '"to":"not-ready"' "event appended state"
if ATLAS_EVENTS_FILE="$EV" bash "$EVT" 683 bogus-state 2>/dev/null; then
  fail=$((fail + 1)); fail_messages+=("slice-event should reject unknown state")
else
  pass=$((pass + 1))
fi

# --summary mode: counts + ready-set + in-flight, but NO per-slice table
SUM="$(ATLAS_ISSUES_DIR="$ISSUES" \
      ATLAS_GIT_LOG_FILE="$TMP/gitlog" \
      ATLAS_OPEN_PRS_FILE="$TMP/prs.json" \
      ATLAS_BRANCHES_FILE="$TMP/branches" \
      ATLAS_EVENTS_FILE="$TMP/events.jsonl" \
      ATLAS_NOW="2026-06-10" \
      bash "$GEN" --stdout --summary)"
assert_contains "$SUM" "## Counts"        "summary keeps counts"
assert_contains "$SUM" "## Ready set"     "summary keeps ready set"
assert_contains "$SUM" "ready to pick up" "summary shows ready COUNT, not the list"
assert_contains "$SUM" "## In-flight"     "summary keeps in-flight"
assert_not_contains "$SUM" "## All slices" "summary omits the per-slice table"
assert_not_contains "$SUM" "| 100 | merged via closes |" "summary omits per-slice rows"
assert_contains "$SUM" "# Slice status"   "summary uses the public header"

echo "gen-status_test: $pass passed, $fail failed"
if [[ "$fail" -gt 0 ]]; then
  printf '  FAIL: %s\n' "${fail_messages[@]}" >&2
  exit 1
fi
