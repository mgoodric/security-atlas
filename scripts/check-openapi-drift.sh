#!/usr/bin/env bash
#
# check-openapi-drift.sh — slice 140 BLOCKING drift-detect guard.
#
# Asserts two things, in order:
#
#   1. **Inventory drift** — the committed `docs/openapi.yaml` matches
#      the output of `cmd/atlas-openapi` against the in-tree
#      `internal/api/openapi.RouteSpecs` slice. Catches the
#      "edited routes.go but forgot to regen the spec" case.
#
#   2. **Coverage drift** — every chi route registration discovered in
#      `internal/api/*/` is present in `RouteSpecs`. Catches the
#      "added a chi route but forgot to declare it in RouteSpecs" case.
#
# Exit codes:
#
#   0 — no drift; spec is in lockstep with code
#   1 — drift detected (one of the two checks failed)
#   2 — environment misconfigured (missing tool, repo layout wrong)
#
# Local repro (offline-safe, idempotent):
#
#   bash scripts/check-openapi-drift.sh
#
# CI: the `openapi-drift-check` job in `.github/workflows/ci.yml`
# invokes this same script. The job is BLOCKING (slice 140 D3 / P0-A2)
# and lives in `.github/branch-protection.json` `required_status_checks`
# contexts; operator post-merge runs `bash scripts/apply-branch-protection.sh`
# (slice 127's apply ritual) to push the new context to live.
#
# Why BLOCKING and not informational:
#   The spec being out of sync with handler reality is the only failure
#   mode that makes the spec actively misleading. An operator who codes
#   against a stale spec, gets surprising 404s / 401s / wrong shapes, and
#   discovers via support that the spec lied — that's the threat model.
#   The 30-second CI re-run cost is cheaper than the trust-damage cost.
#
# Slice 128's `actions-pin-check` is the structural template; slice 127's
# `branch-protection-drift` is informational because reconciliation
# friction (not silent control degradation) is its failure mode. This
# script chooses BLOCKING because misleading-spec IS silent control
# degradation.

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SPEC_FILE="$ROOT/docs/openapi.yaml"
ROUTES_GO="$ROOT/internal/api/openapi/routes.go"

# ----- environment checks -----

for tool in go grep awk sed sort diff; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "check-openapi-drift: missing required tool '$tool' on PATH" >&2
    exit 2
  fi
done

if [[ ! -f "$SPEC_FILE" ]]; then
  echo "check-openapi-drift: committed spec not found at $SPEC_FILE" >&2
  echo "                    Run 'just openapi-generate' and commit the result." >&2
  exit 2
fi

if [[ ! -f "$ROUTES_GO" ]]; then
  echo "check-openapi-drift: routes.go not found at $ROUTES_GO" >&2
  exit 2
fi

# ----- check 1: inventory drift -----
#
# Regenerate the spec into a tempfile and diff against the committed
# file. If they differ, the maintainer edited RouteSpecs OR the
# generator without committing the regenerated spec. Either way, the
# fix is `just openapi-generate && git add docs/openapi.yaml`.

tmp_spec="$(mktemp)"
tmp_actual="$(mktemp)"
tmp_declared="$(mktemp)"
# shellcheck disable=SC2317
trap 'rm -f "$tmp_spec" "$tmp_actual" "$tmp_declared"' EXIT

if ! ( cd "$ROOT" && go run ./cmd/atlas-openapi --out "$tmp_spec" >/dev/null 2>&1 ); then
  echo "check-openapi-drift: 'go run ./cmd/atlas-openapi' failed; cannot validate" >&2
  echo "                    Re-run locally: cd '$ROOT' && go run ./cmd/atlas-openapi --out /tmp/openapi.yaml" >&2
  exit 2
fi

if ! diff -u "$SPEC_FILE" "$tmp_spec" >/dev/null 2>&1; then
  echo "check-openapi-drift: drift detected between committed docs/openapi.yaml and the generator output." >&2
  echo "" >&2
  echo "Diff (committed → generator):" >&2
  diff -u "$SPEC_FILE" "$tmp_spec" | head -80 >&2 || true
  echo "" >&2
  echo "Fix:" >&2
  echo "  1. cd $ROOT" >&2
  echo "  2. just openapi-generate     # regenerates docs/openapi.yaml" >&2
  echo "  3. git add docs/openapi.yaml internal/api/openapi/routes.go" >&2
  echo "  4. git commit --amend --no-edit  # or a new commit" >&2
  exit 1
fi

# ----- check 2: coverage drift -----
#
# Extract every chi route registration from `internal/api/*/` via grep
# and compare against the declared RouteSpecs list. A new route in
# httpserver.go (or any RegisterRoutes/Routes method) that does NOT
# appear in routes.go is a coverage-drift violation.

# Extract actual chi route registrations from the codebase (production
# files only; tests use the same pattern but are not part of the API
# surface). Matches:
#   root.METHOD("/path", ...)       — httpserver.go style
#   r.METHOD("/path", ...)          — RegisterRoutes/Routes style
#   root.Method(http.MethodX, "/path", ...) — version handler style
#   r.Method(http.MethodX, "/path", ...)    — defensive
#   r.METHOD(PathConst, ...)        — internal/api/oauth const-based style (block C)
#
# Output: sorted "METHOD PATH" lines.
{
  grep -rhE '(root|r)\.(Get|Post|Patch|Put|Delete)\("(/v1/|/auth/|/health)' \
    "$ROOT/internal/api/" --include="*.go" --exclude="*_test.go" \
    | sed -E 's/.*\.(Get|Post|Patch|Put|Delete)\("([^"]+)".*/\1 \2/' \
    | awk '{print toupper($1)" "$2}'
  grep -rhE '(root|r)\.Method\(http\.Method(Get|Post|Patch|Put|Delete), "[^"]+"' \
    "$ROOT/internal/api/" --include="*.go" --exclude="*_test.go" \
    | sed -E 's/.*Method\(http\.Method(Get|Post|Patch|Put|Delete), "([^"]+)".*/\1 \2/' \
    | awk '{print toupper($1)" "$2}'
  # Block C (slice 339): the OAuth AS package registers its /oauth/* and
  # /.well-known/* routes via `r.Get(PathConst, ...)` / `r.Post(PathConst, ...)`
  # — a CONST identifier, not a string literal, and on prefixes the two
  # literal-greps above intentionally do not match. Resolve each const to
  # its declared literal so these routes appear in the coverage set.
  # Without this block, declaring them in RouteSpecs would falsely trip the
  # stale-entry check (declared-but-not-registered). See slice 339
  # decisions log D2.
  # Patterns avoid \b and \s so they behave identically on GNU sed (CI,
  # Linux) and BSD sed (macOS, local repro) — neither escape is portable.
  oauth_dir="$ROOT/internal/api/oauth"
  if [[ -d "$oauth_dir" ]]; then
    {
      # const defs:  PathX = "/literal"   ->  emit "C PathX /literal"
      grep -rhE '(Path[A-Za-z0-9]+)[ ]*=[ ]*"(/oauth/|/\.well-known/)' \
        "$oauth_dir" --include="*.go" --exclude="*_test.go" \
        | sed -E 's/.*(Path[A-Za-z0-9]+)[ ]*=[ ]*"([^"]+)".*/C \1 \2/'
      # Mount calls:  r.Get(PathX, ...)   ->  emit "M Get PathX"
      grep -rhE 'r\.(Get|Post|Patch|Put|Delete)\(Path[A-Za-z0-9]+,' \
        "$oauth_dir" --include="*.go" --exclude="*_test.go" \
        | sed -E 's/.*r\.(Get|Post|Patch|Put|Delete)\((Path[A-Za-z0-9]+),.*/M \1 \2/'
    } | awk '
      $1 == "C" { lit[$2] = $3; next }
      $1 == "M" { if ($3 in lit) print toupper($2)" "lit[$3] }
    '
  fi
} | sort -u > "$tmp_actual"

# Extract declared RouteSpecs from routes.go via the same line shape
# the file uses:
#   {Method: "GET", Path: "/v1/foo", ...
grep -E '^\s*\{Method: "[A-Z]+", Path: "[^"]+"' "$ROUTES_GO" \
  | sed -E 's/.*Method: "([A-Z]+)", Path: "([^"]+)".*/\1 \2/' \
  | sort -u > "$tmp_declared"

# Diff: lines in actual but not declared = coverage drift (missing
# from RouteSpecs). Lines in declared but not actual = stale entries
# (registered route was removed).
missing_in_declared="$(comm -23 "$tmp_actual" "$tmp_declared")"
stale_in_declared="$(comm -13 "$tmp_actual" "$tmp_declared")"

if [[ -n "$missing_in_declared" ]] || [[ -n "$stale_in_declared" ]]; then
  echo "check-openapi-drift: coverage drift detected." >&2
  echo "" >&2
  if [[ -n "$missing_in_declared" ]]; then
    echo "Routes registered in code but NOT declared in internal/api/openapi/routes.go:" >&2
    echo "" >&2
    printf '  %s\n' $missing_in_declared >&2
    echo "" >&2
    echo "  Fix: add a {Method: \"...\", Path: \"...\", Tag: \"...\", Tier: \"...\", Internal: false, Summary: \"...\"}" >&2
    echo "       entry to RouteSpecs in $ROUTES_GO for each of the above," >&2
    echo "       then re-run 'just openapi-generate' and commit both files." >&2
    echo "" >&2
  fi
  if [[ -n "$stale_in_declared" ]]; then
    echo "Routes declared in RouteSpecs but NOT registered in code (stale entries):" >&2
    echo "" >&2
    printf '  %s\n' $stale_in_declared >&2
    echo "" >&2
    echo "  Fix: remove the matching entries from RouteSpecs in $ROUTES_GO," >&2
    echo "       then re-run 'just openapi-generate' and commit both files." >&2
    echo "" >&2
  fi
  exit 1
fi

# ----- success -----

declared_count="$(wc -l < "$tmp_declared" | tr -d ' ')"
echo "check-openapi-drift: no drift (${declared_count} routes documented; spec matches generator output)"
exit 0
