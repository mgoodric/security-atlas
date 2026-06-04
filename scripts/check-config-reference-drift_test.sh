#!/usr/bin/env bash
#
# Smoke tests for scripts/check-config-reference-drift.sh.
#
# Slice 430: exercises the drift guard against fixture .env.example /
# configuration.md pairs via the ATLAS_ENV_EXAMPLE / ATLAS_CONFIG_PAGE
# overrides, so the harness never mutates the real in-tree files and runs
# offline.
#
# Cases:
#   1. in-sync                -> exit 0
#   2. key missing from page  -> exit 1
#   3. stale key on page      -> exit 1
#   4. commented opt-in key missing from page -> exit 1
#   5. commented opt-in key present on page   -> exit 0
#   6. env template missing   -> exit 2 (env error)
#   7. THE REAL in-tree pair  -> exit 0 (AC-14: page is accurate at merge)
#
# Run: bash scripts/check-config-reference-drift_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/check-config-reference-drift.sh"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ ! -x "$SCRIPT" ]]; then
  echo "check-config-reference-drift_test: script not executable at $SCRIPT" >&2
  exit 2
fi

pass=0
fail=0
fail_messages=()

assert_exit() {
  local actual="$1" expected="$2" label="$3"
  if [[ "$actual" == "$expected" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: got exit '$actual', want exit '$expected'")
  fi
}

run() {
  # $1 env file, $2 page file; prints nothing, returns the exit code.
  local rc=0
  ATLAS_ENV_EXAMPLE="$1" ATLAS_CONFIG_PAGE="$2" bash "$SCRIPT" >/dev/null 2>&1 || rc=$?
  echo "$rc"
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# ---------- fixtures ----------

cat > "$tmp/env_basic" <<'EOF'
FOO=bar
SECRET_KEY=CHANGE_ME
# OPT_IN_FLAG=false
EOF

# 1. in-sync (active FOO + SECRET_KEY + opt-in OPT_IN_FLAG all on page)
cat > "$tmp/page_sync" <<'EOF'
| Variable | Default | Required? | Scope | Description |
| --- | --- | --- | --- | --- |
| `FOO` | bar | no | server | desc |
| `SECRET_KEY` | (operator-supplied) | yes | server | desc |
| `OPT_IN_FLAG` | false | no | server | desc |
EOF
assert_exit "$(run "$tmp/env_basic" "$tmp/page_sync")" 0 "in-sync"

# 2. active key missing from page (SECRET_KEY absent)
cat > "$tmp/page_missing" <<'EOF'
| `FOO` | bar | no | server | desc |
| `OPT_IN_FLAG` | false | no | server | desc |
EOF
assert_exit "$(run "$tmp/env_basic" "$tmp/page_missing")" 1 "active-key-missing"

# 3. stale key on page (BOGUS not in template)
cat > "$tmp/page_stale" <<'EOF'
| `FOO` | bar | no | server | desc |
| `SECRET_KEY` | (operator-supplied) | yes | server | desc |
| `OPT_IN_FLAG` | false | no | server | desc |
| `BOGUS` | x | no | server | desc |
EOF
assert_exit "$(run "$tmp/env_basic" "$tmp/page_stale")" 1 "stale-key-on-page"

# 4. commented opt-in key missing from page (OPT_IN_FLAG absent)
cat > "$tmp/page_no_optin" <<'EOF'
| `FOO` | bar | no | server | desc |
| `SECRET_KEY` | (operator-supplied) | yes | server | desc |
EOF
assert_exit "$(run "$tmp/env_basic" "$tmp/page_no_optin")" 1 "optin-missing"

# 5. commented opt-in present -> in-sync (covered by case 1, assert again
#    with a page that lists opt-in but in different order to prove parse)
cat > "$tmp/page_reorder" <<'EOF'
| `OPT_IN_FLAG` | false | no | server | desc |
| `SECRET_KEY` | (operator-supplied) | yes | server | desc |
| `FOO` | bar | no | server | desc |
EOF
assert_exit "$(run "$tmp/env_basic" "$tmp/page_reorder")" 0 "optin-present-reordered"

# 6. missing env template -> env error
assert_exit "$(run "$tmp/does_not_exist" "$tmp/page_sync")" 2 "env-template-missing"

# 7. the real in-tree pair must pass (AC-14)
assert_exit "$(run "$ROOT/deploy/docker/.env.example" "$ROOT/docs-site/docs/configuration.md")" 0 "real-pair-in-sync"

# ---------- report ----------

echo "check-config-reference-drift_test: ${pass} passed, ${fail} failed"
if [[ "$fail" -ne 0 ]]; then
  printf '  FAIL: %s\n' "${fail_messages[@]}" >&2
  exit 1
fi
exit 0
