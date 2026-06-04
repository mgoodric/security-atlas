#!/usr/bin/env bash
#
# check-config-reference-drift.sh — slice 430 drift-detect guard.
#
# Asserts the published configuration reference page
# (docs-site/docs/configuration.md) stays in lockstep with the
# authoritative environment template (deploy/docker/.env.example).
#
# The threat the guard closes: the reference page is the surface an
# operator treats as authoritative. If a variable is added to
# .env.example (a new toggle, a new secret) and the page is not updated,
# the operator reads a page that silently omits a real knob — possibly a
# security-critical one. The inverse (a page key with no .env.example
# backing) means the page documents a variable that does not exist.
#
# What it checks, in order:
#
#   1. **Missing-from-page** — every key matched by `^[A-Z_]+=` in
#      .env.example (the active / always-present set) appears in
#      configuration.md. This is the load-bearing direction (AC-9 / AC-14).
#
#   2. **Commented opt-in keys** — every key matched by `^# *[A-Z_]+=`
#      in .env.example (documented-but-default-off variables, e.g.
#      ATLAS_METRICS_FALLBACK_ENABLE, the OTEL_* opt-ins) also appears in
#      the page. The threat model (E) requires these be documented WITH
#      their warnings even though they ship commented out.
#
#   3. **Stale page keys** — every variable the page documents as a code
#      span at the start of a table row (`| \`VAR\` |`) resolves to a key
#      that exists in .env.example (active or commented). Catches a page
#      row for a variable that was renamed/removed from the template.
#
# Exit codes:
#   0 — no drift; the page and the template agree
#   1 — drift detected
#   2 — environment misconfigured (missing file / tool)
#
# Local repro (offline, idempotent):
#   bash scripts/check-config-reference-drift.sh
#   just config-reference-drift-check
#
# CI: the `config-reference-drift-check` job in
# .github/workflows/docs-publish.yml invokes this same script on every PR
# that touches the page or the template. Informational, matching the
# docs-publish workflow's advisory posture (slice 058) — a docs-hygiene
# failure should surface loudly on the PR without blocking an unrelated
# code merge; the page-vs-template completeness is re-verified at every
# docs build.
#
# Test override: ATLAS_ENV_EXAMPLE / ATLAS_CONFIG_PAGE point the script
# at fixture files so scripts/check-config-reference-drift_test.sh can
# exercise it without mutating the real in-tree files.

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ENV_EXAMPLE="${ATLAS_ENV_EXAMPLE:-$ROOT/deploy/docker/.env.example}"
CONFIG_PAGE="${ATLAS_CONFIG_PAGE:-$ROOT/docs-site/docs/configuration.md}"

# ----- environment checks -----

for tool in grep sed sort comm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "check-config-reference-drift: missing required tool '$tool' on PATH" >&2
    exit 2
  fi
done

if [[ ! -f "$ENV_EXAMPLE" ]]; then
  echo "check-config-reference-drift: env template not found at $ENV_EXAMPLE" >&2
  exit 2
fi

if [[ ! -f "$CONFIG_PAGE" ]]; then
  echo "check-config-reference-drift: reference page not found at $CONFIG_PAGE" >&2
  echo "                              Slice 430 expects docs-site/docs/configuration.md." >&2
  exit 2
fi

tmp_active="$(mktemp)"
tmp_commented="$(mktemp)"
tmp_all_keys="$(mktemp)"
tmp_page_keys="$(mktemp)"
# shellcheck disable=SC2317
trap 'rm -f "$tmp_active" "$tmp_commented" "$tmp_all_keys" "$tmp_page_keys"' EXIT

# Active keys: `^VAR=`
grep -E '^[A-Z][A-Z0-9_]*=' "$ENV_EXAMPLE" \
  | sed -E 's/=.*//' | sort -u > "$tmp_active"

# Commented opt-in keys: `^# VAR=` (default-off, documented-with-warning)
grep -E '^# *[A-Z][A-Z0-9_]*=' "$ENV_EXAMPLE" \
  | sed -E 's/^# *//; s/=.*//' | sort -u > "$tmp_commented"

sort -u "$tmp_active" "$tmp_commented" > "$tmp_all_keys"

# Page keys: a table row whose first cell is a code-spanned variable
# name, e.g.  | `ATLAS_TEST_MODE` | ...
grep -E '^\| *`[A-Z][A-Z0-9_]*` *\|' "$CONFIG_PAGE" \
  | sed -E 's/^\| *`([A-Z][A-Z0-9_]*)`.*/\1/' | sort -u > "$tmp_page_keys"

drift=0

# ----- check 1 + 2: every template key (active + commented) on the page -----
missing_from_page="$(comm -23 "$tmp_all_keys" "$tmp_page_keys" || true)"
if [[ -n "$missing_from_page" ]]; then
  drift=1
  echo "check-config-reference-drift: keys in .env.example but MISSING from the page:" >&2
  echo "" >&2
  printf '  %s\n' $missing_from_page >&2
  echo "" >&2
  echo "  Fix: add a table row for each in $CONFIG_PAGE." >&2
  echo "       Secret-typed (*_PASSWORD / *_KEY / *_TOKEN) vars must show a" >&2
  echo "       placeholder + 'openssl rand -hex 32' guidance, never a value." >&2
  echo "" >&2
fi

# ----- check 3: every page key resolves to a real template key -----
stale_on_page="$(comm -13 "$tmp_all_keys" "$tmp_page_keys" || true)"
if [[ -n "$stale_on_page" ]]; then
  drift=1
  echo "check-config-reference-drift: keys documented on the page but ABSENT from .env.example:" >&2
  echo "" >&2
  printf '  %s\n' $stale_on_page >&2
  echo "" >&2
  echo "  Fix: remove the stale row from $CONFIG_PAGE, or (if the variable" >&2
  echo "       is real and the template is the one that drifted) add it to" >&2
  echo "       $ENV_EXAMPLE." >&2
  echo "" >&2
fi

if [[ "$drift" -ne 0 ]]; then
  exit 1
fi

active_count="$(grep -cE '.' "$tmp_active" || true)"
commented_count="$(grep -cE '.' "$tmp_commented" || true)"
total=$((active_count + commented_count))
echo "check-config-reference-drift: no drift (${total} variables documented; ${active_count} active + ${commented_count} opt-in match the page)"
exit 0
