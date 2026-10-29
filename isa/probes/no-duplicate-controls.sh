#!/usr/bin/env bash
#
# no-duplicate-controls.sh -- falsifier for isa/ISA.md ISC-2: "No control in
# the catalog is duplicated across frameworks." It is C1 (one control, N
# framework satisfactions) stated as something checkable.
#
# What a duplicate is (Matt's ruling on #1698, recorded in isa/ISA.md): one
# control copied per framework onto the same SCF anchor. That is the thing C1
# and ADR-0013 forbid. Distinct controls written for ONE framework that share
# an anchor are not duplicates: the five SOC 2 controls on GOV-01 are five
# controls, not one control five times.
#
# Where the framework comes from: the catalog's layout. The shipped catalog is
# controls/<framework>/<bundle>/control.yaml, and that directory is the only
# place a control's framework is recorded. Neither the bundle format nor the
# `controls` row carries one, and satisfactions are derived from the anchor
# alone. So the falsifier is: one scf_anchor_id appearing under two different
# controls/<framework>/ directories. No database is involved.
#
# What it cannot see: a bundle a tenant uploads has no framework attached, so
# a copy made that way is invisible here. That limit is written into the ISA
# answer rather than papered over.
#
# Self-test, every run: before scanning the real catalog, the probe scans two
# fixtures under isa/probes/testdata/no-duplicate-controls/:
#   holds/  -- three soc2 controls sharing GOV-01, plus an iso27001 control on
#              a different anchor. Must report no duplicate.
#   false/  -- the same, plus an iso27001 copy on GOV-01. Must report it.
# If either answers wrong, the scan itself is broken and nothing it says about
# the real catalog can be trusted, so the probe exits 3.
#
# Usage: bash isa/probes/no-duplicate-controls.sh [catalog-root]
#        (default: controls/ at the repo root)
#
# Exit 0  no SCF anchor appears under two framework directories.
# Exit 1  at least one does (claim false), each one named.
# Exit 3  cannot run: the catalog or fixtures are missing, or the self-test
#         failed.
set -uo pipefail

name=no-duplicate-controls
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
FIXTURES="$REPO_ROOT/isa/probes/testdata/no-duplicate-controls"
CATALOG="${1:-$REPO_ROOT/controls}"

# pairs ROOT -- one "ANCHOR FRAMEWORK" line per anchored bundle under
# ROOT/<framework>/<bundle>/control.yaml. An unanchored bundle is not in the
# graph, so it cannot be a duplicate, and is left out.
pairs() {
  local root=$1 file rel anchor
  find "$root" -mindepth 3 -maxdepth 3 -name control.yaml -type f | sort |
    while IFS= read -r file; do
      rel=${file#"$root"/}
      anchor=$(sed -nE 's/^scf_anchor_id:[[:space:]]*["'\'']?([^"'\'' #]+).*/\1/p' "$file" | head -n 1)
      [ -n "$anchor" ] && printf '%s %s\n' "$anchor" "${rel%%/*}"
    done
  return 0
}

# dupes ROOT -- "ANCHOR fw1,fw2" for every anchor under 2+ frameworks.
dupes() {
  pairs "$1" | sort -u | awk '
    { fws[$1] = ($1 in fws) ? fws[$1] "," $2 : $2; n[$1]++ }
    END { for (a in n) if (n[a] > 1) print a, fws[a] }' | sort
}

for d in "$FIXTURES/holds" "$FIXTURES/false"; do
  if [ ! -d "$d" ]; then
    echo "$name: CANNOT RUN -- self-test fixture $d is missing" >&2
    exit 3
  fi
done
if [ -n "$(dupes "$FIXTURES/holds")" ]; then
  echo "$name: CANNOT RUN -- self-test: the holds/ fixture (distinct soc2 controls sharing GOV-01) was reported as a duplicate" >&2
  exit 3
fi
if [ -z "$(dupes "$FIXTURES/false")" ]; then
  echo "$name: CANNOT RUN -- self-test: the false/ fixture (an iso27001 copy on GOV-01) was not reported" >&2
  exit 3
fi

if [ ! -d "$CATALOG" ]; then
  echo "$name: CANNOT RUN -- no catalog at $CATALOG" >&2
  exit 3
fi
bundles=$(pairs "$CATALOG" | wc -l | tr -d ' ')
if [ "$bundles" -eq 0 ]; then
  echo "$name: CANNOT RUN -- $CATALOG holds no anchored <framework>/<bundle>/control.yaml" >&2
  exit 3
fi

found=$(dupes "$CATALOG")
if [ -n "$found" ]; then
  echo "$name: FALSE -- an SCF anchor carries a control under more than one framework directory (anchor frameworks):" >&2
  printf '  %s\n' "$found" >&2
  echo "$name: one control copied per framework is the duplication C1 forbids; map the second framework through the anchor instead" >&2
  exit 1
fi

frameworks=$(pairs "$CATALOG" | cut -d' ' -f2 | sort -u | wc -l | tr -d ' ')
echo "$name: holds -- $bundles anchored bundles under $frameworks framework director(ies), no SCF anchor under two frameworks (self-test passed)"
exit 0
