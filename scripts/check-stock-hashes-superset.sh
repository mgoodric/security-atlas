#!/usr/bin/env bash
# Fails when internal/api/scfimport/stock_manifests_gen.go has lost an entry
# that the base branch's copy holds (#2963). The file lists every stock
# control.yaml revision the platform has shipped; a dropped hash makes
# IsStockManifest treat a tenant still on that revision as custom, with no
# warning. scripts/gen-stock-control-hashes.sh is append-only, so a drop
# arrives some other way (a merge-conflict resolution, a hand edit), and this
# check catches it.
#
# Retiring a wrong entry on purpose, mirroring stock-supersede-check (#2891):
# the PR carries the `drops-stock-hash` label (--labels, one per line, as CI
# passes the PR's labels) AND every dropped hash, in full, appears in a
# docs/audit-log/*.md the PR adds or changes since BASE_REF. The check then
# passes and prints a notice listing the dropped entries.
#
# Usage: bash scripts/check-stock-hashes-superset.sh [--labels LABELS] [BASE_REF]
#   BASE_REF defaults to origin/main.
# Exit 0: superset, an acknowledged drop, or the base has no such file.
# Exit 1: entries dropped without acknowledgement. Exit 2: usage error or
# BASE_REF does not resolve.
set -euo pipefail

ack_label="drops-stock-hash"
dlog_dir="docs/audit-log"
labels=""
base=""
while [ $# -gt 0 ]; do
	case "$1" in
	--labels)
		[ $# -ge 2 ] || {
			echo "check-stock-hashes-superset: --labels needs a value" >&2
			exit 2
		}
		labels="$2"
		shift 2
		;;
	-*)
		echo "check-stock-hashes-superset: unknown flag $1" >&2
		exit 2
		;;
	*)
		base="$1"
		shift
		;;
	esac
done
base="${base:-origin/main}"
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

if [ ! -s "$tmp/dropped" ]; then
	echo "check-stock-hashes-superset: $rel holds all $(wc -l <"$tmp/base" | tr -d ' ') entries of $base"
	exit 0
fi

labelled=0
while IFS= read -r l; do
	if [ "$l" = "$ack_label" ]; then labelled=1; fi
done <<<"$labels"

# Decisions logs the PR adds or changes, committed or not.
{
	git -C "$root" diff --name-only --diff-filter=AM "$base" -- "$dlog_dir"
	git -C "$root" ls-files --others --exclude-standard -- "$dlog_dir"
} | sed -n '/\.md$/p' | sort -u >"$tmp/dlogs"

unlogged=0
: >"$tmp/report"
while read -r hash bundle; do
	logged="no decisions-log entry"
	while IFS= read -r d; do
		if grep -qF -- "$hash" "$root/$d"; then
			logged="named in $d"
			break
		fi
	done <"$tmp/dlogs"
	if [ "$logged" = "no decisions-log entry" ]; then unlogged=1; fi
	printf '  %s %s (%s)\n' "$hash" "$bundle" "$logged" >>"$tmp/report"
done <"$tmp/dropped"

n="$(wc -l <"$tmp/dropped" | tr -d ' ')"
if [ "$labelled" = 1 ] && [ "$unlogged" = 0 ]; then
	echo "check-stock-hashes-superset: $rel drops $n entries that $base holds, acknowledged:"
	cat "$tmp/report"
	echo "The PR carries \"$ack_label\" and names every dropped hash in a changed $dlog_dir/*.md."
	if [ -n "${GITHUB_ACTIONS:-}" ]; then
		echo "::notice title=Stock hashes dropped::$n stock hash(es) retired under the $ack_label label; see the step log"
	fi
	exit 0
fi

echo "check-stock-hashes-superset: $rel dropped $n entries that $base holds:" >&2
cat "$tmp/report" >&2
echo "Restore them; the stock list is append-only (#2963). To retire a wrong" >&2
echo "entry on purpose, add the \"$ack_label\" label and name each hash above" >&2
echo "in a $dlog_dir/*.md this PR changes." >&2
exit 1
