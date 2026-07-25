#!/usr/bin/env bash
#
# summarize-latency.sh — compute the slice 335 Experiment 1 steady-state
# metrics (P50 / P95 / max latency, error rate) from an
# scripts/chaos/evidence-traffic.sh CSV, optionally windowed by wall clock.
#
# Input CSV columns: epoch_s,op,http_code,latency_ms
#
# A request counts as an error when its HTTP code is not 2xx. curl transport
# failures are recorded by the generator as code 000 and count as errors.
#
# Percentiles use the nearest-rank method on the sorted latency list
# (rank = ceil(p * n)) — the standard discrete definition, no interpolation.
#
# Usage:
#   scripts/chaos/summarize-latency.sh --csv run.csv [--from-s N] [--to-s N]
#
# Exit: 0 summary printed, 2 env error.

set -eu

CSV=""
FROM_S=0
TO_S=99999999999

die() {
  echo "summarize-latency: $1" >&2
  exit 2
}

is_uint() {
  case "$1" in
  '' | *[!0-9]*) return 1 ;;
  *) return 0 ;;
  esac
}

while [ $# -gt 0 ]; do
  case "$1" in
  --csv)
    CSV="${2:-}"
    shift 2
    ;;
  --from-s)
    FROM_S="${2:-}"
    shift 2
    ;;
  --to-s)
    TO_S="${2:-}"
    shift 2
    ;;
  -h | --help)
    sed -n '2,18p' "$0"
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[ -n "$CSV" ] || die "--csv is required"
[ -f "$CSV" ] || die "csv not found: $CSV"
is_uint "$FROM_S" || die "--from-s must be an integer"
is_uint "$TO_S" || die "--to-s must be an integer"

# counts <op> -> "count errors"
counts() {
  awk -F, -v op="$1" -v lo="$FROM_S" -v hi="$TO_S" '
    NR == 1 { next }
    $2 != op { next }
    $1 + 0 < lo || $1 + 0 > hi { next }
    { n++; if ($3 + 0 < 200 || $3 + 0 > 299) e++ }
    END { printf "%d %d\n", n + 0, e + 0 }
  ' "$CSV"
}

# latencies <op> -> one latency per line, numerically sorted
latencies() {
  awk -F, -v op="$1" -v lo="$FROM_S" -v hi="$TO_S" '
    NR == 1 { next }
    $2 == op && $1 + 0 >= lo && $1 + 0 <= hi { print $4 + 0 }
  ' "$CSV" | sort -n
}

printf '%-6s %8s %8s %10s %8s %8s %8s\n' op count errors error_pct p50_ms p95_ms max_ms

for op in read write; do
  pair="$(counts "$op")"
  n="${pair% *}"
  errs="${pair#* }"
  if [ "$n" -eq 0 ]; then
    printf '%-6s %8d %8d %10s %8s %8s %8s\n' "$op" 0 0 "n/a" "n/a" "n/a" "n/a"
    continue
  fi
  latencies "$op" | awk -v op="$op" -v n="$n" -v errs="$errs" '
    { lat[NR] = $1 }
    END {
      p50 = lat[int((0.50 * n) + 0.999999)]
      p95 = lat[int((0.95 * n) + 0.999999)]
      printf "%-6s %8d %8d %9.3f%% %8d %8d %8d\n", op, n, errs, (errs * 100.0) / n, p50, p95, lat[n]
    }
  '
done

echo
echo "non-2xx status breakdown:"
awk -F, -v lo="$FROM_S" -v hi="$TO_S" '
  NR == 1 { next }
  $1 + 0 < lo || $1 + 0 > hi { next }
  $3 + 0 >= 200 && $3 + 0 <= 299 { next }
  { c[$2 " " $3]++ }
  END { if (length(c) == 0) print "  (none)"; else for (k in c) printf "  %s x%d\n", k, c[k] }
' "$CSV"
