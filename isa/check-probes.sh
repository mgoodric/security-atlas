#!/usr/bin/env bash
# Keep the probe files and the Test Strategy tables honest about each other.
#
# Ported from tend's isa/check-probes.sh with the same semantics
# (security-atlas#1690). tend wrote it after ten pull requests each added a
# second probe for a claim that already had one, every one under a different
# filename, and nothing in the repo could tell. A naming convention
# (`<epic>-<isc>.sh`) would have caught only duplicates and traded readable
# names for mechanical ones, so the check is structural instead, against the
# Test Strategy tables, which are already the authority for which probe answers
# which claim:
#
#   COLLISION  two different probe files answer the same (epic, claim). This is
#              the duplicate case.
#   ORPHAN     a probe file no strategy row references. This is the OTHER half
#              of a duplicate: a change that replaced a row leaves the previous
#              probe behind, still running, answering nothing.
#   MISSING    a strategy row naming a probe file that does not exist. Reported
#              but NOT blocking. A claim may name a probe that has not been
#              written yet -- that is how a machine-checkable claim avoids
#              being filed as manual -- so blocking on it would turn this check
#              into an obstacle to the workflow it exists to protect. It is
#              still worth printing, because the same line appears when a probe
#              is DELETED and its row is left behind.
#
# security-atlas's Test Strategy tables carry the same six columns as tend's
# (isc | tier | type | check | threshold | tool), with the probe path in the
# sixth, so the parser below is tend's unchanged.
#
# Exit 0  the tables and the files agree.
# Exit 1  they do not, with each disagreement named.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 3

python3 - <<'PY'
import glob
import os
import re
import sys

# (epic, isc) -> set of probe filenames named by that claim's rows. A claim MAY
# have several rows (ISC-3 has a service one and a pure one), so the collision
# test is on distinct PROBE FILES for one claim, not on row count.
by_claim: dict[tuple[str, str], set[str]] = {}
referenced: set[str] = set()
missing: list[str] = []

for path in sorted(glob.glob("isa/*.md")):
    epic = os.path.basename(path)
    for line in open(path).read().splitlines():
        if not line.startswith("| ISC-"):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        if len(cells) < 6:
            continue
        isc = cells[0]
        for probe in re.findall(r"isa/probes/([A-Za-z0-9_-]+\.sh)", cells[5]):
            by_claim.setdefault((epic, isc), set()).add(probe)
            referenced.add(probe)
            if not os.path.exists(f"isa/probes/{probe}"):
                missing.append(f"{epic} {isc} names isa/probes/{probe}, which does not exist")

on_disk = {
    os.path.basename(p)
    for p in glob.glob("isa/probes/*.sh")
    # Shared helpers and the runners are not probes and answer no claim.
    if not os.path.basename(p).startswith("_")
    and os.path.basename(p) not in {"run-probes.sh", "check-probes.sh"}
}

collisions = [
    f"{epic} {isc} is answered by {len(files)} different probes: {', '.join(sorted(files))}"
    for (epic, isc), files in sorted(by_claim.items())
    if len(files) > 1
]
orphans = [
    f"isa/probes/{p} is referenced by no Test Strategy row"
    for p in sorted(on_disk - referenced)
]

# A pipeline ending in `grep -q` inside a `pipefail` script is a RACE, not a
# style question. `grep -q` exits on its first match and closes the pipe; the
# writer on the left then dies of SIGPIPE (141), `pipefail` makes that the
# pipeline's status, and the `if` reads it as FALSE. So the probe silently
# reports "no match" for input that matches, depending on whether the writer
# finished first -- which depends on payload size and machine load.
#
# That is exactly how a tend probe blocked a merge and then passed a re-run
# of the identical commit (mgoodric/tend#486): 59KB of app.py lost the
# race 22 times in 30 on an idle box, and a busy CI runner lost it on 24KB too.
# A probe that answers differently on the same input is worse than no probe,
# because it teaches people to re-run rather than read.
#
# Checked here rather than left to review: the shape is easy to write, reads
# perfectly natural, and the failure only shows up once a file grows.
sigpipe_races = []
for path in sorted(glob.glob("isa/probes/*.sh")):
    text = open(path).read()
    if "pipefail" not in text:
        continue
    for n, line in enumerate(text.splitlines(), 1):
        if re.search(r"\|\s*grep\s+-[A-Za-z]*q", line):
            sigpipe_races.append(
                f"{path}:{n} pipes into `grep -q` under pipefail -- a SIGPIPE race "
                f"(use `grep -q PAT <<<\"$var\"`, or drop -q and redirect to /dev/null)"
            )

# Only collisions, orphans and races block. See the header on MISSING.
problems = collisions + orphans + sigpipe_races
for label, items in (
    ("COLLISION", collisions),
    ("ORPHAN", orphans),
    ("SIGPIPE-RACE", sigpipe_races),
    ("MISSING", missing),
):
    for item in items:
        print(f"{label}: {item}")

print(
    f"\n{len(on_disk)} probe files, {len(by_claim)} claims with a probe, "
    f"{len(problems)} blocking problem(s), {len(missing)} not-yet-written probe(s)"
)
sys.exit(1 if problems else 0)
PY
