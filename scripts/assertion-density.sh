#!/usr/bin/env bash
#
# assertion-density.sh — slice 353 (Q-6 from slice 333's QA audit)
#
# Count assertion calls per Go test file and warn where the assertion
# density (assertions per N lines of test code) is below a threshold.
# A cheap mutation-testing proxy: a test that invokes a function and
# inspects a side effect with one fragile check lifts the line-coverage
# number while testing almost nothing. This surfaces those files.
#
# "Assertion" = a call to any of:
#   t.Error  t.Errorf  t.Fatal  t.Fatalf
#   require.*  assert.*   (testify)
#   want/got comparison helpers are NOT counted (too noisy to detect
#   reliably in bash) — testify + the testing.T failure verbs are the
#   high-signal set.
#
# ADVISORY ONLY. This script NEVER exits non-zero on a low-density file
# (exit 0 even with warnings). It exists for visibility — the same
# stance as the slice-350 security-critical coverage advisory. A future
# slice may decide to promote it to a soft/hard gate; that is a
# deliberate slice decision, not this script's default. The only
# non-zero exits are environment misconfig (exit 2).
#
# Density math: a file's density = assertions / (test_loc / DENSITY_LOC).
# Equivalently it warns when assertions * DENSITY_LOC < test_loc, i.e.
# fewer than 1 assertion per DENSITY_LOC lines. Blank lines and the
# package/import preamble are still counted as LOC (cheap, deterministic).
#
# Inputs (env):
#   DENSITY_ROOT      Directory to walk for *_test.go (default: .)
#   DENSITY_LOC       LOC-per-assertion threshold (default: 20)
#   DENSITY_MIN_LOC   Skip files smaller than this many LOC (default: 15)
#                     — tiny files (a single TestMain, a 2-line helper)
#                     are noise; the proxy is meaningful only at scale.
#   DENSITY_FORMAT    "text" (default) or "json"
#   DENSITY_VERBOSE   "true" to also list files that PASS (default: false)
#
# Exit codes:
#   0 — always, on a successful run (warnings are printed but do not fail)
#   2 — environment misconfigured (root unreadable)
#
# Usage:
#   bash scripts/assertion-density.sh
#   DENSITY_LOC=15 bash scripts/assertion-density.sh
#   DENSITY_FORMAT=json bash scripts/assertion-density.sh
#
# Self-test:
#   bash scripts/assertion-density_test.sh

set -euo pipefail

ROOT="${DENSITY_ROOT:-.}"
LOC_PER_ASSERT="${DENSITY_LOC:-20}"
MIN_LOC="${DENSITY_MIN_LOC:-15}"
FORMAT="${DENSITY_FORMAT:-text}"
VERBOSE="${DENSITY_VERBOSE:-false}"

if [[ ! -d "$ROOT" ]]; then
  echo "assertion-density: root not a directory: $ROOT" >&2
  exit 2
fi

# Assertion regex. Word-boundary-ish: match the failure verbs and the
# testify package qualifiers when they appear as a call.
assert_re='(\bt\.(Error|Errorf|Fatal|Fatalf)\()|(\b(require|assert)\.[A-Za-z]+\()'

low_count=0
total_files=0
warnings=()
json_rows=()

# Find test files, skipping vendored / generated trees.
while IFS= read -r f; do
  total_files=$((total_files + 1))

  loc="$(wc -l < "$f" | tr -d ' ')"
  if (( loc < MIN_LOC )); then
    continue
  fi

  asserts="$(grep -E -c "$assert_re" "$f" || true)"
  asserts="${asserts:-0}"

  # warn when fewer than 1 assertion per LOC_PER_ASSERT lines:
  #   asserts * LOC_PER_ASSERT < loc
  if (( asserts * LOC_PER_ASSERT < loc )); then
    low_count=$((low_count + 1))
    # density expressed as one-assert-per-N-loc (integer, guard /0)
    if (( asserts > 0 )); then
      per=$(( loc / asserts ))
    else
      per="$loc+"
    fi
    warnings+=("$f: $asserts assertion(s) over $loc LOC (~1 per ${per} LOC; threshold 1 per ${LOC_PER_ASSERT})")
    json_rows+=("{\"file\":\"$f\",\"assertions\":$asserts,\"loc\":$loc,\"below_threshold\":true}")
  elif [[ "$VERBOSE" == "true" || "$FORMAT" == "json" ]]; then
    json_rows+=("{\"file\":\"$f\",\"assertions\":$asserts,\"loc\":$loc,\"below_threshold\":false}")
  fi
done < <(find "$ROOT" -type f -name '*_test.go' \
            -not -path '*/vendor/*' \
            -not -path '*/gen/*' \
            -not -path '*/node_modules/*' | sort)

if [[ "$FORMAT" == "json" ]]; then
  printf '{"threshold_loc_per_assertion":%s,"files_scanned":%s,"below_threshold":%s,"rows":[' \
    "$LOC_PER_ASSERT" "$total_files" "$low_count"
  first=1
  for r in "${json_rows[@]:-}"; do
    [[ -z "$r" ]] && continue
    if (( first )); then first=0; else printf ','; fi
    printf '%s' "$r"
  done
  printf ']}\n'
  exit 0
fi

echo "assertion-density: scanned $total_files test file(s); threshold 1 assertion per ${LOC_PER_ASSERT} LOC (advisory)"
if (( low_count > 0 )); then
  echo "assertion-density: WARNING — $low_count file(s) below the density threshold:" >&2
  for w in "${warnings[@]}"; do
    echo "  - $w" >&2
  done
  echo "" >&2
  echo "  Advisory only — this does NOT fail the build. Low density often means a" >&2
  echo "  test exercises code for coverage without asserting on the result. Review" >&2
  echo "  flagged files for missing assertions. Slice 333 Q-6 / slice 353." >&2
else
  echo "assertion-density: OK — every scanned file meets the density threshold."
fi
exit 0
