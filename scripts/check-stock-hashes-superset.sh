#!/usr/bin/env bash
# Fails when internal/api/scfimport/stock_manifests_gen.go has lost an entry
# that the base branch's copy holds (#2963). The file lists every stock
# control.yaml revision the platform has shipped; a dropped hash makes
# IsStockManifest treat a tenant still on that revision as custom, with no
# warning. scripts/gen-stock-control-hashes.sh is append-only, so a drop
# arrives some other way (a merge-conflict resolution, a hand edit), and this
# check catches it.
#
# Usage: bash scripts/check-stock-hashes-superset.sh [BASE_REF]
#   BASE_REF defaults to origin/main.
# Exit 0: superset (or the base has no such file). Exit 1: entries dropped.
# Exit 2: BASE_REF does not resolve.
set -euo pipefail

base="${1:-origin/main}"
root="$(git rev-parse --show-toplevel)"
rel="internal/api/scfimport/stock_manifests_gen.go"

if ! git -C "$root" rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
	echo "check-stock-hashes-superset: base ref '$base' does not resolve" >&2
	exit 2
fi
if ! git -C "$root" cat-file -e "$base:$rel" 2>/dev/null; then
	echo "check-stock-hashes-superset: $base has no $rel; nothing to compare"
	exit 0
fi

entries() {
	sed -n 's/^[[:space:]]*"\([0-9a-f]\{64\}\)":[[:space:]]*"\([^"]*\)",$/\1 \2/p' | sort -u
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
git -C "$root" show "$base:$rel" | entries >"$tmp/base"
entries <"$root/$rel" >"$tmp/head"
comm -23 "$tmp/base" "$tmp/head" >"$tmp/dropped"

if [ -s "$tmp/dropped" ]; then
	echo "check-stock-hashes-superset: $rel dropped $(wc -l <"$tmp/dropped" | tr -d ' ') entries that $base holds:" >&2
	sed 's/^/  /' "$tmp/dropped" >&2
	echo "Restore them; the stock list is append-only (#2963)." >&2
	exit 1
fi
echo "check-stock-hashes-superset: $rel holds all $(wc -l <"$tmp/base" | tr -d ' ') entries of $base"
