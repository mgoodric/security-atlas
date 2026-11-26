#!/usr/bin/env bash
#
# check-stub-twins.sh — #2099
#
# Fail when a path-filtered job in .github/workflows/ci.yml and its stub twin
# disagree. The slice-061 pattern (docs/ci/PATH_FILTERING.md) posts each gated
# check from one of two jobs: the real job, gated `== 'true'` on a `changes`
# output, and a stub with the SAME `name:`, gated `!= 'true'` on the SAME
# output. Exactly one runs, so the named check always reports. If the names
# drift, or the twin reads a different output (a real job on `go`, its stub
# still on `code`), a PR can end up with neither running, the named check
# never reports, and the merge waits forever; or with both running.
#
# Recognised gates, after stripping `${{ }}` and whitespace (X is one of the
# `changes` outputs code, go, web):
#
#   real                                                       stub
#   needs.changes.outputs.X == 'true'                          needs.changes.outputs.X != 'true'
#   always() && needs.changes.outputs.X == 'true'              needs.changes.outputs.X != 'true'
#   github.event_name == 'pull_request'                        github.event_name != 'pull_request'
#     && needs.changes.outputs.X == 'true'                       || needs.changes.outputs.X != 'true'
#
# Findings:
#   MISSING-TWIN     a real job with no other job of the same `name:`
#   NOT-COMPLEMENT   a twin exists but its `if:` is not the real job's complement
#   ORPHAN-STUB      a stub with no real job of the same `name:` and complement
#   DUPLICATE        more than two jobs share a gated `name:`
#   UNRECOGNISED     a job-level `if:` reads changes.outputs.{code,go,web} in a
#                    shape not listed above, so its twin cannot be checked
#
# NO_TWIN names a real job that deliberately has no stub, with the reason.
# An entry fails the check when it goes stale (the job is gone, is not a
# recognised real job, or has a twin after all).
#
# Exit codes:
#   0 — every gated job has a complementary twin or a NO_TWIN entry
#   1 — at least one finding, or a stale NO_TWIN entry
#   2 — the workflow is unreadable or its layout is not recognised
#
# Usage:
#   bash scripts/check-stub-twins.sh
#   CI_WORKFLOW=/path/to/ci.yml bash scripts/check-stub-twins.sh
#
# Needs a python3 that can import PyYAML (ubuntu-latest's can); elsewhere set
# STUB_TWINS_PYTHON, as for scripts/check-merge-gate-needs.sh.
#
# Self-test:
#   bash scripts/check-stub-twins_test.sh

set -euo pipefail

WORKFLOW="${CI_WORKFLOW:-.github/workflows/ci.yml}"
PYTHON="${STUB_TWINS_PYTHON:-python3}"

# "<job> <reason>", one per line. STUB_TWINS_NO_TWIN replaces it for the
# self-test only; ci.yml never sets it.
NO_TWIN="${STUB_TWINS_NO_TWIN-build-atlas not a required check; the playwright jobs that need it post their own named checks (slice 695)
tests-integration-shard per-leg checks are not required; the roll-up tests-integration carries the twin (slice 417)
}"

if [[ ! -f "$WORKFLOW" || ! -r "$WORKFLOW" ]]; then
  echo "check-stub-twins: workflow not readable: $WORKFLOW" >&2
  exit 2
fi

set +e
"$PYTHON" - "$WORKFLOW" "$NO_TWIN" <<'PY'
import re
import sys

try:
    import yaml
except ImportError:
    print("check-stub-twins: PyYAML is not importable; set STUB_TWINS_PYTHON", file=sys.stderr)
    sys.exit(2)

path, no_twin_raw = sys.argv[1], sys.argv[2]
try:
    with open(path) as f:
        doc = yaml.safe_load(f)
except yaml.YAMLError as e:
    print(f"check-stub-twins: {path} is not valid YAML: {e}", file=sys.stderr)
    sys.exit(2)

jobs = doc.get("jobs") if isinstance(doc, dict) else None
if not isinstance(jobs, dict) or not jobs:
    print(f"check-stub-twins: no jobs found under 'jobs:' in {path}", file=sys.stderr)
    sys.exit(2)

OUT = r"needs\.changes\.outputs\.(code|go|web)"
PR = "github.event_name=='pull_request'"
NOT_PR = "github.event_name!='pull_request'"
REAL = [
    ("plain", re.compile(rf"(?:always\(\)&&)?{OUT}=='true'")),
    ("pr", re.compile(rf"{re.escape(PR)}&&{OUT}=='true'")),
]
STUB = [
    ("plain", re.compile(rf"{OUT}!='true'")),
    ("pr", re.compile(rf"{re.escape(NOT_PR)}\|\|{OUT}!='true'")),
]


def normalise(cond):
    cond = cond.strip()
    m = re.fullmatch(r"\$\{\{(.*)\}\}", cond, re.S)
    if m:
        cond = m.group(1)
    return re.sub(r"\s+", "", cond).replace('"', "'")


def classify(cond):
    for shape, rx in REAL:
        m = rx.fullmatch(cond)
        if m:
            return "real", (shape, m.group(1))
    for shape, rx in STUB:
        m = rx.fullmatch(cond)
        if m:
            return "stub", (shape, m.group(1))
    return None, None


findings = []
gated = {}  # job -> (role, key, name)
for jid, job in jobs.items():
    if not isinstance(job, dict):
        print(f"check-stub-twins: job '{jid}' is not a mapping in {path}", file=sys.stderr)
        sys.exit(2)
    cond = job.get("if")
    if not isinstance(cond, str):
        continue
    norm = normalise(cond)
    role, key = classify(norm)
    if role is None:
        if re.search(OUT + r"\b", norm):
            findings.append(f"UNRECOGNISED {jid}: if: {cond.strip()}")
        continue
    gated[jid] = (role, key, job.get("name", jid))

by_name = {}
for jid, (role, key, name) in gated.items():
    by_name.setdefault(name, []).append(jid)

no_twin = {}
for line in no_twin_raw.splitlines():
    parts = line.split(None, 1)
    if parts:
        no_twin[parts[0]] = parts[1] if len(parts) > 1 else ""

for name, ids in sorted(by_name.items()):
    if len(ids) > 2:
        findings.append(f"DUPLICATE '{name}': {', '.join(sorted(ids))}")

for jid, (role, key, name) in sorted(gated.items()):
    others = [o for o in by_name[name] if o != jid]
    if role == "real":
        if not others:
            if jid not in no_twin:
                findings.append(f"MISSING-TWIN {jid}: no stub job has name '{name}'")
            continue
        for o in others:
            orole, okey, _ = gated[o]
            if orole != "stub" or okey != key:
                findings.append(
                    f"NOT-COMPLEMENT {jid} / {o}: '{name}' real gate {key[0]}:{key[1]}=='true' "
                    f"but twin is {orole} {okey[0]}:{okey[1]}"
                )
    else:
        if not any(gated[o][0] == "real" and gated[o][1] == key for o in others):
            findings.append(
                f"ORPHAN-STUB {jid}: no real job named '{name}' gated {key[0]}:{key[1]}=='true'"
            )

for jid, reason in sorted(no_twin.items()):
    if not reason:
        findings.append(f"NO_TWIN entry '{jid}' has no reason")
    g = gated.get(jid)
    if g is None or g[0] != "real" or len(by_name[g[2]]) > 1:
        findings.append(f"stale NO_TWIN entry '{jid}' (missing, not a real gated job, or it has a twin); remove it")

for f in findings:
    print(f"check-stub-twins: {f}", file=sys.stderr)
if findings:
    sys.exit(1)
print(f"check-stub-twins: OK ({sum(1 for g in gated.values() if g[0] == 'real')} gated jobs, "
      f"{sum(1 for g in gated.values() if g[0] == 'stub')} stubs)")
PY
rc=$?
set -e
exit $rc
