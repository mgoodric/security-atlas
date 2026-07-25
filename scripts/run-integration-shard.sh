#!/usr/bin/env bash
#
# run-integration-shard.sh — slice 417
#
# Run ONE leg of the sharded integration matrix. Reads the package->leg
# assignment from scripts/integration-shards.txt (the source of truth)
# and runs `go test -tags=integration -p 1` over exactly that leg's
# packages, emitting a leg-scoped coverage profile.
#
# `-p 1` is ALWAYS passed (slice 417 P0-4 / AC-4): the parallelism is
# ACROSS runners (each leg is a separate GHA matrix job with its own
# Postgres), never `-p N` within a runner. This helper hard-codes `-p 1`
# so a leg can never accidentally run packages in parallel against its
# own shared DB.
#
# Usage:
#   scripts/run-integration-shard.sh <LEG> <coverprofile-path>
#     <LEG>               one of: A B1 B2 B3 B4 B5 (must match column 1 of the manifest)
#     <coverprofile-path> where to write this leg's coverage profile
#
# Env:
#   SHARD_MANIFEST   override manifest path (default: scripts/integration-shards.txt)
#   GO_TEST_EXTRA    extra flags appended to the go test invocation (e.g. -race)
#
# Exit codes:
#   0 — the leg's packages passed
#   1 — at least one package failed
#   2 — environment misconfigured (bad leg, empty leg, manifest unreadable)

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

MANIFEST="${SHARD_MANIFEST:-$REPO_ROOT/scripts/integration-shards.txt}"

LEG="${1:-}"
COVERPROFILE="${2:-}"

if [[ -z "$LEG" || -z "$COVERPROFILE" ]]; then
  echo "usage: run-integration-shard.sh <LEG> <coverprofile-path>" >&2
  exit 2
fi
if [[ ! -r "$MANIFEST" ]]; then
  echo "run-integration-shard: manifest not readable: $MANIFEST" >&2
  exit 2
fi
case "$LEG" in
  A | B1 | B2 | B3 | B4 | B5) : ;;
  *)
    echo "run-integration-shard: unknown leg '$LEG' (expected A|B1|B2|B3|B4|B5)" >&2
    exit 2
    ;;
esac

# Collect this leg's package args (column 2 where column 1 == LEG).
mapfile -t PKGS < <(
  grep -vE '^[[:space:]]*#|^[[:space:]]*$' "$MANIFEST" \
    | awk -v leg="$LEG" '$1 == leg {print $2}'
)

if [[ "${#PKGS[@]}" -eq 0 ]]; then
  echo "run-integration-shard: leg '$LEG' has zero packages in $MANIFEST" >&2
  exit 2
fi

echo "run-integration-shard: leg ${LEG} — ${#PKGS[@]} package(s), -p 1 (serial within leg)"
printf '  %s\n' "${PKGS[@]}"

# shellcheck disable=SC2086  # GO_TEST_EXTRA is intentionally word-split.
go test -tags=integration -p 1 -v \
  -covermode=atomic \
  -coverprofile="$COVERPROFILE" \
  -coverpkg=./... \
  ${GO_TEST_EXTRA:-} \
  "${PKGS[@]}"
