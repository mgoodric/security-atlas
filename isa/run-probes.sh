#!/usr/bin/env bash
# Run every ISA probe and fail ONLY on a regression.
#
# Ported from tend's isa/run-probes.sh with the same semantics
# (security-atlas#1690). It replaces the retired engine as the thing that runs
# isa/probes/; CI runs it in the `ISA · probes` job against a migrated Postgres.
#
# The distinction this script exists for: a probe reporting its claim false is
# not a broken build. Most claims in this repo are open, and an open claim's
# probe is SUPPOSED to exit 1 -- that is the whole point of writing the
# falsifier before the feature. Gating on every exit 1 would red-wall every
# pull request from the day it was switched on; tend's first version of this
# job sat unmerged for eleven days for exactly that reason.
#
# So the rule is: a probe for a CLOSED claim -- one ticked `- [x]` in its epic
# -- must pass. If it fails, something that was true has stopped being true,
# and that is a regression worth blocking a merge for. Everything else is
# reported and ignored.
#
# Exit 3 (cannot run) is never a failure. It means no database, no toolchain,
# or no credentials, and reporting that as a defect trains people to ignore the
# job.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 3

# Runs first, and blocks. A probe that answers no claim, or a claim answered by
# two probes, means the results below cannot be trusted to mean what they say --
# so there is no point spending several minutes producing them.
if ! bash isa/check-probes.sh; then
  echo
  echo "FAIL: the probe files and the Test Strategy tables disagree (see above)."
  exit 1
fi
echo

# Which probes belong to a closed claim. A probe is matched to its claim by the
# tool string in the epic's Test Strategy table, so this cannot drift from the
# strategy the way a hand-kept list would.
closed_probes() {
  python3 - <<'PY'
import re, glob, os

closed = set()
rows = []
for path in glob.glob("isa/*.md"):
    epic = os.path.basename(path)
    text = open(path).read()
    for m in re.finditer(r"^- \[x\] (ISC-\d+):", text, re.M):
        closed.add((epic, m.group(1)))
    for line in text.splitlines():
        if not line.startswith("| ISC-"):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        if len(cells) < 6:
            continue
        # Same pattern as check-probes.sh, so the two cannot disagree on
        # which file a row names.
        for probe in re.findall(r"isa/probes/([A-Za-z0-9_-]+\.sh)", cells[5]):
            rows.append((epic, cells[0], probe))

for epic, isc, probe in rows:
    if (epic, isc) in closed:
        print(probe)
PY
}

# A newline-separated string rather than an array: macOS ships bash 3.2, which
# has no `mapfile`, and `set -u` treats an empty array expansion as an unbound
# variable. This script has to run identically on a laptop and in CI, or the
# thing that gates merges is a thing nobody can reproduce.
must_pass=$(closed_probes | sort -u)
gating_count=$(printf '%s' "$must_pass" | grep -c . || true)
printf 'probes gating this build (closed claims): %s\n' "$gating_count"

pass=0; failed=0; unrunnable=0; regressions=""
for probe in isa/probes/*.sh; do
  base=$(basename "$probe")
  case "$base" in _*) continue ;; esac

  echo "::group::${probe}"
  bash "$probe"; rc=$?
  echo "::endgroup::"

  gating=no
  # A here-string, not `printf | grep -q`: under pipefail that is the SIGPIPE
  # race check-probes.sh blocks in probes.
  if grep -qx "$base" <<<"$must_pass"; then gating=yes; fi

  if [ "$rc" = "3" ]; then
    unrunnable=$((unrunnable + 1))
    echo "SKIP  ${base} (exit 3, unrunnable here)"
  elif [ "$rc" = "0" ]; then
    pass=$((pass + 1))
  else
    failed=$((failed + 1))
    if [ "$gating" = "yes" ]; then
      regressions="${regressions}${base}
"
      echo "REGRESSION  ${base} — this probe belongs to a CLOSED claim"
    else
      echo "open  ${base} (exit $rc — claim is open, expected)"
    fi
  fi
done

echo
echo "pass=${pass} false=${failed} unrunnable=${unrunnable}"

regression_count=$(printf '%s' "$regressions" | grep -c . || true)
if [ "$regression_count" -gt 0 ]; then
  echo
  echo "FAIL: ${regression_count} probe(s) for CLOSED claims are now false:"
  printf '%s' "$regressions" | sed 's/^/  /'
  echo "Either the change broke something that was true, or the claim was ticked without being earned."
  exit 1
fi

exit 0
