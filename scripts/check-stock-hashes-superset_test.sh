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

# Case 6: the #2956 flow. main and a PR branch each regenerate with a
# working-tree revision that never gets committed, so each side's file holds
# a hash the other's history cannot reproduce. Merge main, then regenerate.
# Whether git merges the file cleanly depends on where the two hashes sort;
# a conflicted file parses the same way, since both sides' entry lines stay
# between the markers. The pre-#2963 generator dropped main's draft here
# (cc6_7 `263888e5`, twice: #2955 and #2956's main merge).
r="$work/r6"
new_repo "$r"
git -C "$r" checkout -q -b pr
printf 'bundle_id: soc2_a\nv: pr-draft\n' >"$r/controls/soc2/soc2_a/control.yaml"
(cd "$r" && bash "$GEN" >/dev/null)
git -C "$r" checkout -q -- controls
git -C "$r" commit -q -am "pr: gen with a draft"
git -C "$r" checkout -q main
printf 'bundle_id: soc2_a\nv: main-draft\n' >"$r/controls/soc2/soc2_a/control.yaml"
(cd "$r" && bash "$GEN" >/dev/null)
git -C "$r" checkout -q -- controls
git -C "$r" commit -q -am "main: gen with a draft"
main_draft="$(git -C "$r" diff HEAD~1 -- "$GEN_FILE" | sed -n 's/^+[[:space:]]*"\([0-9a-f]\{64\}\)".*/\1/p')"
git -C "$r" checkout -q pr
git -C "$r" merge -q --no-edit main >/dev/null 2>&1 || true
(cd "$r" && bash "$GEN" >/dev/null)
assert_eq "$(hash_lines "$r")" 3 "regenerating after the merge keeps both drafts"
set +e
(cd "$r" && bash "$CHECK" main >/dev/null 2>&1)
rc=$?
set -e
assert_eq "$rc" 0 "merge plus regeneration is a superset of main"

# Case 7: the merge resolved by keeping the branch's pre-merge copy of the
# file drops main's draft, and the check names it.
git -C "$r" show "ORIG_HEAD:$GEN_FILE" >"$r/$GEN_FILE"
set +e
out="$(cd "$r" && bash "$CHECK" main 2>&1)"
rc=$?
set -e
assert_eq "$rc" 1 "merge resolved with the branch copy"
assert_contains "$out" "$main_draft soc2_a" "merge resolved with the branch copy"

# Case 8: a conflicted file (markers and all) regenerates to the union.
r="$work/r8"
new_repo "$r"
kept="$(grep -oE '[0-9a-f]{64}' "$r/$GEN_FILE")"
ours="$(printf 'c%.0s' $(seq 64))"
theirs="$(printf 'd%.0s' $(seq 64))"
cat >"$r/$GEN_FILE" <<EOF
package scfimport

var stockManifestHashes = map[string]string{
<<<<<<< HEAD
	"$ours": "soc2_a",
=======
	"$theirs": "soc2_a",
>>>>>>> main
	"$kept": "soc2_a",
}
EOF
(cd "$r" && bash "$GEN" >/dev/null)
assert_eq "$(hash_lines "$r")" 3 "conflicted file regenerates to the union"
assert_eq "$(grep -c '^[<=>]\{7\}' "$r/$GEN_FILE" || true)" 0 "conflict markers are gone"

# Case 9: a deliberate drop passes with the drops-stock-hash label and a
# changed decisions log naming the hash, and the output says so.
r="$work/r9"
new_repo "$r"
retired="$(grep -oE '[0-9a-f]{64}' "$r/$GEN_FILE")"
sed -i.bak "/$retired/d" "$r/$GEN_FILE"
mkdir -p "$r/docs/audit-log"
printf 'Retire %s: wrong bundle.\n' "$retired" >"$r/docs/audit-log/9-retire.md"
set +e
out="$(cd "$r" && bash "$CHECK" --labels "$(printf 'bug\ndrops-stock-hash')" main 2>&1)"
rc=$?
set -e
assert_eq "$rc" 0 "labelled and logged drop"
assert_contains "$out" "acknowledged" "labelled and logged drop"
assert_contains "$out" "$retired soc2_a (named in docs/audit-log/9-retire.md)" "labelled and logged drop"

# Case 10: the label alone, or the decisions log alone, is not enough.
set +e
(cd "$r" && bash "$CHECK" main >/dev/null 2>&1)
rc=$?
set -e
assert_eq "$rc" 1 "logged drop without the label"
rm "$r/docs/audit-log/9-retire.md"
set +e
out="$(cd "$r" && bash "$CHECK" --labels drops-stock-hash main 2>&1)"
rc=$?
set -e
assert_eq "$rc" 1 "labelled drop without a decisions log"
assert_contains "$out" "$retired soc2_a (no decisions-log entry)" "labelled drop without a decisions log"

echo "check-stock-hashes-superset_test: $pass passed, $fail failed"
if ((fail > 0)); then
	printf '  %s\n' "${fail_messages[@]}" >&2
	exit 1
fi
