#!/usr/bin/env bash
#
# Smoke tests for scripts/audit-deps.sh.
#
# Covers the classifier behaviour against a synthetic fixture tree so the
# tests are independent of which deps happen to be in the live tree on
# the day CI runs:
#
#   1. USED         — a TypeScript file imports a package; script outputs USED
#   2. USED-VIA-CONFIG (TS config) — a package referenced in eslint.config / vitest.config
#   3. USED-VIA-CONFIG (CSS)       — a package referenced in `@import "<pkg>"` in a .css file
#   4. USED-VIA-SCRIPT             — a package invoked in package.json scripts: block / justfile
#   5. PHANTOM      — declared in manifest, no consumer signal anywhere
#   6. Reproducible — two runs against the same fixture produce identical output
#   7. TSV header   — output is `ecosystem\tpackage\tclassification\tevidence`
#   8. --ecosystem flag scopes the run (npm-only does not emit go/pip rows)
#   9. Go ecosystem — direct go.mod deps classified by .go import scan;
#      PHANTOM emits a "run `go mod tidy`" hint (P0-A2: never edits go.mod)
#   10. pip-bridge — pyproject.toml deps; consumer is .py import OR [tool.*] section
#   11. pip-docs   — requirements.txt deps; consumer is mkdocs.yml plugins / theme name
#
# Run: bash scripts/audit-deps_test.sh
# Exits non-zero on first failed assertion.

set -eu

SCRIPT="$(cd "$(dirname "$0")" && pwd)/audit-deps.sh"
if [[ ! -x "$SCRIPT" ]]; then
  echo "audit-deps_test: script not executable at $SCRIPT" >&2
  exit 2
fi

pass=0
fail=0
fail_messages=()

assert_contains() {
  local haystack="$1" needle="$2" label="$3"
  if grep -qF -- "$needle" <<<"$haystack"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: expected output to contain '$needle'")
    fail_messages+=("  full output:")
    while IFS= read -r line; do
      fail_messages+=("    $line")
    done <<<"$haystack"
  fi
}

assert_not_contains() {
  local haystack="$1" needle="$2" label="$3"
  if ! grep -qF -- "$needle" <<<"$haystack"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: expected output to NOT contain '$needle'")
  fi
}

assert_eq() {
  local actual="$1" expected="$2" label="$3"
  if [[ "$actual" == "$expected" ]]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    fail_messages+=("$label: got '$actual', want '$expected'")
  fi
}

# --------------------------------------------------------------------
# Build a synthetic monorepo under a temp directory and point the script
# at it via the AUDIT_DEPS_ROOT env override.
# --------------------------------------------------------------------
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

# --- npm fixture ---
mkdir -p "$fixture/web/lib" "$fixture/web/app" "$fixture/web/components"
cat >"$fixture/web/package.json" <<'PKG'
{
  "name": "fixture-web",
  "scripts": {
    "build": "next build",
    "lint": "eslint",
    "scaffold": "npx test-cli-tool add"
  },
  "dependencies": {
    "test-imported": "1.0.0",
    "test-config-ts": "1.0.0",
    "test-css-import": "1.0.0",
    "test-cli-tool": "1.0.0",
    "test-phantom": "1.0.0",
    "next": "16.0.0"
  },
  "devDependencies": {
    "test-vitest-config": "1.0.0"
  }
}
PKG

# Consumer signals:
cat >"$fixture/web/app/page.tsx" <<'TSX'
import { thing } from "test-imported";
export default function Page() { return thing(); }
TSX

cat >"$fixture/web/eslint.config.mjs" <<'ESL'
import config from "test-config-ts";
export default [config];
ESL

cat >"$fixture/web/vitest.config.ts" <<'VIT'
import { defineConfig } from "vitest/config";
import preset from "test-vitest-config";
export default defineConfig({ ...preset });
VIT

cat >"$fixture/web/app/globals.css" <<'CSS'
@import "test-css-import";
@import "tailwindcss";
CSS

# next is referenced in package.json scripts: ("next build") — USED-VIA-SCRIPT
# test-cli-tool is referenced in scripts: ("npx test-cli-tool add") — USED-VIA-SCRIPT
# test-phantom is declared but referenced nowhere — PHANTOM

# --- go fixture ---
mkdir -p "$fixture/internal/foo" "$fixture/cmd/bar"
cat >"$fixture/go.mod" <<'GOMOD'
module example.com/fixture

go 1.26

require (
	github.com/test/imported v1.0.0
	github.com/test/phantom v1.0.0
)
GOMOD

cat >"$fixture/internal/foo/foo.go" <<'GOF'
package foo

import "github.com/test/imported"

func F() { imported.Do() }
GOF

# --- pip-bridge fixture ---
mkdir -p "$fixture/oscal-bridge/atlas_oscal_bridge"
cat >"$fixture/oscal-bridge/pyproject.toml" <<'PYP'
[project]
name = "fixture-bridge"
version = "0.1.0"
dependencies = [
    "test-py-imported>=1.0",
    "test-py-tool>=1.0",
    "test-py-phantom>=1.0",
]

[tool.test-py-tool]
key = "value"
PYP

cat >"$fixture/oscal-bridge/atlas_oscal_bridge/__init__.py" <<'PYF'
import test_py_imported
PYF

# --- pip-docs fixture ---
mkdir -p "$fixture/docs-site"
cat >"$fixture/docs-site/requirements.txt" <<'REQ'
test-mkdocs-theme==1.0.0
test-mkdocs-plugin==1.0.0
test-docs-phantom==1.0.0
REQ

cat >"$fixture/docs-site/mkdocs.yml" <<'MKD'
site_name: fixture
theme:
  name: test-mkdocs-theme
plugins:
  - search
  - test-mkdocs-plugin
MKD

# Empty justfile + empty .github/workflows — required-path probes should
# not crash if the file is absent.
cat >"$fixture/justfile" <<'JUST'
default:
	@echo ok
JUST

mkdir -p "$fixture/.github/workflows"
cat >"$fixture/.github/workflows/ci.yml" <<'YML'
name: CI
on: [push]
jobs:
  noop:
    runs-on: ubuntu-latest
    steps:
      - run: echo "no deps referenced here"
YML

# --------------------------------------------------------------------
# Run 1 — full audit, capture output.
# --------------------------------------------------------------------
out1="$(AUDIT_DEPS_ROOT="$fixture" "$SCRIPT" 2>/dev/null)"

# Test 7: header line is exactly `ecosystem\tpackage\tclassification\tevidence`.
header_line="$(head -1 <<<"$out1")"
expected_header=$'ecosystem\tpackage\tclassification\tevidence'
assert_eq "$header_line" "$expected_header" "AC-4 TSV header"

# Test 1: USED via direct TS import.
assert_contains "$out1" $'npm\ttest-imported\tUSED' "AC-2 USED via TS import"

# Test 2: USED-VIA-CONFIG via eslint.config.mjs.
assert_contains "$out1" $'npm\ttest-config-ts\tUSED-VIA-CONFIG' "AC-3 USED-VIA-CONFIG via eslint.config"

# Test 2b: USED-VIA-CONFIG via vitest.config.ts (devDep path).
assert_contains "$out1" $'npm\ttest-vitest-config\tUSED-VIA-CONFIG' "AC-3 USED-VIA-CONFIG via vitest.config"

# Test 3: USED-VIA-CONFIG via CSS @import.
assert_contains "$out1" $'npm\ttest-css-import\tUSED-VIA-CONFIG' "AC-3 USED-VIA-CONFIG via CSS @import"

# Test 4: USED-VIA-SCRIPT — referenced in package.json scripts: block.
assert_contains "$out1" $'npm\ttest-cli-tool\tUSED-VIA-SCRIPT' "AC-2 USED-VIA-SCRIPT via package.json scripts"
assert_contains "$out1" $'npm\tnext\tUSED-VIA-SCRIPT' "AC-2 USED-VIA-SCRIPT via package.json scripts (next build)"

# Test 5: PHANTOM — declared but referenced nowhere.
assert_contains "$out1" $'npm\ttest-phantom\tPHANTOM' "AC-2 PHANTOM classification"

# Go ecosystem.
assert_contains "$out1" $'go\tgithub.com/test/imported\tUSED' "Go USED via .go import"
assert_contains "$out1" $'go\tgithub.com/test/phantom\tPHANTOM' "Go PHANTOM classification"

# Test 9: PHANTOM in go ecosystem emits a stderr hint about `go mod tidy`
# (P0-A2 — never edits go.mod, only recommends).
hint_stderr="$(AUDIT_DEPS_ROOT="$fixture" "$SCRIPT" --ecosystem go 2>&1 >/dev/null)"
assert_contains "$hint_stderr" "go mod tidy" "P0-A2 go mod tidy recommendation on PHANTOM"

# pip-bridge ecosystem.
assert_contains "$out1" $'pip-bridge\ttest-py-imported\tUSED' "pip-bridge USED via .py import"
assert_contains "$out1" $'pip-bridge\ttest-py-tool\tUSED-VIA-CONFIG' "pip-bridge USED-VIA-CONFIG via [tool.*]"
assert_contains "$out1" $'pip-bridge\ttest-py-phantom\tPHANTOM' "pip-bridge PHANTOM"

# pip-docs ecosystem.
assert_contains "$out1" $'pip-docs\ttest-mkdocs-theme\tUSED-VIA-CONFIG' "pip-docs USED-VIA-CONFIG via mkdocs theme"
assert_contains "$out1" $'pip-docs\ttest-mkdocs-plugin\tUSED-VIA-CONFIG' "pip-docs USED-VIA-CONFIG via mkdocs plugins"
assert_contains "$out1" $'pip-docs\ttest-docs-phantom\tPHANTOM' "pip-docs PHANTOM"

# Test 6: reproducible — second run produces identical output.
out2="$(AUDIT_DEPS_ROOT="$fixture" "$SCRIPT" 2>/dev/null)"
if [[ "$out1" == "$out2" ]]; then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
  fail_messages+=("AC-1 reproducible: run 1 and run 2 produce different output")
fi

# Test 8: --ecosystem flag scopes the run.
out_npm="$(AUDIT_DEPS_ROOT="$fixture" "$SCRIPT" --ecosystem npm 2>/dev/null)"
assert_contains "$out_npm" $'npm\ttest-phantom\tPHANTOM' "AC-6 --ecosystem npm includes npm rows"
assert_not_contains "$out_npm" $'go\tgithub.com/test/imported' "AC-6 --ecosystem npm excludes go rows"
assert_not_contains "$out_npm" "pip-bridge" "AC-6 --ecosystem npm excludes pip-bridge rows"
assert_not_contains "$out_npm" "pip-docs" "AC-6 --ecosystem npm excludes pip-docs rows"

out_go="$(AUDIT_DEPS_ROOT="$fixture" "$SCRIPT" --ecosystem go 2>/dev/null)"
assert_contains "$out_go" $'go\tgithub.com/test/imported' "AC-6 --ecosystem go includes go rows"
assert_not_contains "$out_go" "npm" "AC-6 --ecosystem go excludes npm rows"

# Invalid --ecosystem value exits non-zero.
set +e
AUDIT_DEPS_ROOT="$fixture" "$SCRIPT" --ecosystem bogus >/dev/null 2>&1
ec=$?
set -e
if [[ "$ec" -ne 0 ]]; then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
  fail_messages+=("AC-6 --ecosystem bogus must exit non-zero")
fi

# --------------------------------------------------------------------
# Report.
# --------------------------------------------------------------------
echo "audit-deps_test: $pass passed, $fail failed"
if (( fail > 0 )); then
  printf '%s\n' "${fail_messages[@]}" >&2
  exit 1
fi
