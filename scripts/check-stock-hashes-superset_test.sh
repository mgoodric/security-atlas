#!/usr/bin/env bash
#
# Self-test for scripts/check-stock-hashes-superset.sh and the append-only
# behaviour of scripts/gen-stock-control-hashes.sh (#2963).
#
# Each case builds a throwaway git repo holding a controls/soc2 tree and a
# generated stock_manifests_gen.go, so neither script touches the real tree.
#
# Run: bash scripts/check-stock-hashes-superset_test.sh

set -eu

here="$(cd "$(dirname "$0")" && pwd)"
CHECK="$here/check-stock-hashes-superset.sh"
GEN="$here/gen-stock-control-hashes.sh"
GEN_FILE="internal/api/scfimport/stock_manifests_gen.go"

pass=0
fail=0
fail_messages=()

assert_eq() {
	if [[ "$1" == "$2" ]]; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
		fail_messages+=("$3: got '$1', want '$2'")
	fi
}

assert_contains() {
	if grep -qF -- "$2" <<<"$1"; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
		fail_messages+=("$3: output lacks '$2'; got: $1")
	fi
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# new_repo DIR: a repo with one stock control committed and its list
# generated and committed on main.
new_repo() {
	local d="$1"
	mkdir -p "$d/controls/soc2/soc2_a" "$d/scripts" "$(dirname "$d/$GEN_FILE")"
	git -C "$d" init -q -b main
	git -C "$d" config user.email t@example.com
	git -C "$d" config user.name t
	git -C "$d" config commit.gpgsign false
	printf 'bundle_id: soc2_a\nv: 1\n' >"$d/controls/soc2/soc2_a/control.yaml"
	git -C "$d" add controls
	git -C "$d" commit -q -m one
	(cd "$d" && bash "$GEN" >/dev/null)
	git -C "$d" add "$GEN_FILE"
	git -C "$d" commit -q -m gen
}

hash_lines() { grep -cE '^[[:space:]]*"[0-9a-f]{64}":' "$1/$GEN_FILE"; }

# Case 1: unchanged list is a superset of main.
r="$work/r1"
new_repo "$r"
set +e
out="$(cd "$r" && bash "$CHECK" main 2>&1)"
rc=$?
set -e
assert_eq "$rc" 0 "unchanged list"
assert_contains "$out" "holds all 1 entries" "unchanged list"

# Case 2: a hand-dropped hash fails, naming the hash and bundle.
r="$work/r2"
new_repo "$r"
dropped="$(grep -oE '[0-9a-f]{64}' "$r/$GEN_FILE")"
sed -i.bak "/$dropped/d" "$r/$GEN_FILE"
set +e
out="$(cd "$r" && bash "$CHECK" main 2>&1)"
rc=$?
set -e
assert_eq "$rc" 1 "dropped hash"
assert_contains "$out" "$dropped soc2_a" "dropped hash"

# Case 3: an unresolvable base ref is exit 2, not a pass.
set +e
(cd "$r" && bash "$CHECK" no-such-ref >/dev/null 2>&1)
rc=$?
set -e
assert_eq "$rc" 2 "unresolvable base"

# Case 4: the squash-merge loss. A hash generated from a working-tree
# revision that never gets committed survives the next regeneration.
r="$work/r4"
new_repo "$r"
printf 'bundle_id: soc2_a\nv: 2\n' >"$r/controls/soc2/soc2_a/control.yaml"
(cd "$r" && bash "$GEN" >/dev/null)
assert_eq "$(hash_lines "$r")" 2 "intermediate revision generated"
git -C "$r" add "$GEN_FILE"
git -C "$r" commit -q -m "gen with intermediate"
printf 'bundle_id: soc2_a\nv: 3\n' >"$r/controls/soc2/soc2_a/control.yaml"
(cd "$r" && bash "$GEN" >/dev/null)
assert_eq "$(hash_lines "$r")" 3 "regeneration keeps the uncommitted revision"
set +e
(cd "$r" && bash "$CHECK" main >/dev/null 2>&1)
rc=$?
set -e
assert_eq "$rc" 0 "regenerated list is a superset"

# Case 5: an added entry is not a drop.
r="$work/r5"
new_repo "$r"
sed -i.bak "s/^var stockManifestHashes = map\[string\]string{\$/&\\
	\"$(printf 'b%.0s' $(seq 64))\": \"soc2_b\",/" "$r/$GEN_FILE"
set +e
(cd "$r" && bash "$CHECK" main >/dev/null 2>&1)
rc=$?
set -e
assert_eq "$rc" 0 "added entry"

echo "check-stock-hashes-superset_test: $pass passed, $fail failed"
if ((fail > 0)); then
	printf '  %s\n' "${fail_messages[@]}" >&2
	exit 1
fi
