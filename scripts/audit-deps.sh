#!/usr/bin/env bash
#
# audit-deps.sh — classify every direct dependency across the four
# manifests of the security-atlas monorepo as USED / USED-VIA-CONFIG /
# USED-VIA-SCRIPT / PHANTOM.
#
# Surfaced 2026-05-16 in the /loop dep-review analysis of PR #154
# (lucide-react phantom). Slice 120 ships this script + the initial
# removal pass.
#
# Output is TSV to stdout, one row per direct dep, with header:
#
#     ecosystem<TAB>package<TAB>classification<TAB>evidence
#
# Classifications:
#   USED              — appears in import / require / from-import in
#                       non-test, non-lockfile source files
#   USED-VIA-CONFIG   — appears in an allowlisted config file
#                       (eslint / postcss / tailwind / vitest / playwright
#                       / next / tsconfig / mkdocs / pre-commit /
#                       [tool.*] pyproject sections / CSS @import)
#   USED-VIA-SCRIPT   — appears as a CLI invocation in package.json
#                       scripts: block, justfile recipes, or
#                       .github/workflows/*.yml run-steps
#   PHANTOM           — none of the above
#
# Anti-criteria (per slice 120):
#   P0-A2  — go-modules: emits a recommendation to run `go mod tidy` if
#            any direct dep classifies as PHANTOM; never edits go.mod.
#   P0-A4  — the USED-VIA-CONFIG allowlist is enumerated explicitly in
#            this file (search for CONFIG_GLOBS); not a TODO.
#
# Reproducibility:
#   - Manifest deps emitted in sorted order (LC_ALL=C lexicographic).
#   - Within an ecosystem the row order is deterministic (sorted).
#   - The script does not consult network state, current time, or any
#     non-source-tree input. Same tree + same script = byte-identical
#     output (use `diff` to verify in CI).
#
# Flags:
#   --ecosystem <npm|go|pip-bridge|pip-docs>
#       Scope the run to a single ecosystem. Default is all four.
#       AC-6 — useful when only one manifest changed in a PR.
#   --help
#       Print this header and exit 0.
#
# Env:
#   AUDIT_DEPS_ROOT   Override the repo root the script audits. Defaults
#                     to the git-toplevel resolved from the script's own
#                     location. Used by the test harness to point at a
#                     synthetic fixture tree.
#
# Exit codes:
#   0 — completed (PHANTOMs are reported, not errored)
#   1 — environment misconfigured (missing ripgrep / jq, unreadable
#       manifest, invalid --ecosystem)
#
# Performance target (AC-5): < 30s on the current repo tree.
# Uses ripgrep for the source scan.

set -Eeuo pipefail

# --------------------------------------------------------------------
# Argument + env parsing
# --------------------------------------------------------------------
ECOSYSTEM_FILTER=""
while (( $# > 0 )); do
  case "$1" in
    --ecosystem)
      shift
      ECOSYSTEM_FILTER="${1:-}"
      case "$ECOSYSTEM_FILTER" in
        npm|go|pip-bridge|pip-docs) ;;
        *)
          echo "audit-deps: --ecosystem must be one of: npm, go, pip-bridge, pip-docs" >&2
          exit 1
          ;;
      esac
      ;;
    --help|-h)
      sed -n '2,/^set -Eeuo pipefail$/p' "$0" | sed -E 's/^# ?//;/^set -Eeuo/d'
      exit 0
      ;;
    *)
      echo "audit-deps: unknown argument: $1" >&2
      exit 1
      ;;
  esac
  shift
done

for tool in rg jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "audit-deps: missing required tool '$tool' on PATH" >&2
    exit 1
  fi
done

# Resolve the repo root. AUDIT_DEPS_ROOT overrides for the test harness.
if [[ -n "${AUDIT_DEPS_ROOT:-}" ]]; then
  ROOT="$AUDIT_DEPS_ROOT"
else
  ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fi
cd "$ROOT"

# --------------------------------------------------------------------
# Helpers
# --------------------------------------------------------------------

# Emit one TSV row.
emit_row() {
  printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4"
}

# Run ripgrep with the same flags everywhere. We want fixed-string
# matches with line numbers, and we want zero-exit-when-no-match to be
# survivable (rg returns 1 on no match).
#
# The trailing `.` path arg is load-bearing: when rg is invoked without
# any path arg AND in a context where stdin is wired (e.g. inside a
# `while read … <<<…` loop), it reads from stdin instead of walking
# the tree, which silently consumes the next iteration's input. With an
# explicit path arg, rg always walks the tree.
rg_fixed() {
  rg --no-heading --line-number --color=never -F "$@" . 2>/dev/null || true
}
rg_regex() {
  rg --no-heading --line-number --color=never "$@" . 2>/dev/null || true
}

# The USED-VIA-CONFIG allowlist (P0-A4 — enumerated explicitly, not a
# TODO). This is the list of globs whose presence-of-a-package-name
# counts as a config-driven consumer signal.
#
# Keep this list anchored on file-name patterns, not directory paths:
# config files float around the repo (web/eslint.config.mjs but
# .pre-commit-config.yaml at the repo root, etc.).
CONFIG_GLOBS=(
  # JS/TS tooling
  '.eslintrc*'
  'eslint.config.*'
  '.prettierrc*'
  'prettier.config.*'
  'postcss.config.*'
  'vitest.config.*'
  'playwright.config.*'
  'tailwind.config.*'
  'next.config.*'
  'tsconfig*.json'
  'components.json'        # shadcn CLI config
  # CSS @import statements (Tailwind / shadcn entrypoints often import packages
  # from CSS rather than TS — e.g. `@import "tw-animate-css"`).
  '*.css'
  # Python tooling
  'pyproject.toml'         # [tool.*] sections
  # Docs
  'mkdocs.yml'
  # Pre-commit
  '.pre-commit-config.yaml'
)

# Convert CONFIG_GLOBS into ripgrep `--glob` args. Returns the args via
# the CONFIG_ARGS array (set by the caller). Using a named-array
# convention rather than command substitution avoids filename expansion
# of glob patterns like `.github/workflows/*.yml` by the calling shell.
build_config_args() {
  CONFIG_ARGS=()
  local g
  for g in "${CONFIG_GLOBS[@]}"; do
    CONFIG_ARGS+=( -g "$g" )
  done
}

# Locations where USED-VIA-SCRIPT references can live.
SCRIPT_GLOBS=(
  'package.json'           # scripts: block (handled specially: per-workspace, not the lockfile)
  '**/package.json'
  'justfile'
  '.github/workflows/*.yml'
  '.github/workflows/*.yaml'
  # Build / codegen scripts. The oscal-bridge gen_proto.sh invokes
  # `python -m grpc_tools.protoc`, which is the only USED signal for
  # the `grpcio-tools` pip dep. Without this glob, build-time deps that
  # ship no source-level import classify as PHANTOM.
  'scripts/*.sh'
  '**/scripts/*.sh'
  'scripts/*.mjs'
  '**/scripts/*.mjs'
)
build_script_args() {
  SCRIPT_ARGS=()
  local g
  for g in "${SCRIPT_GLOBS[@]}"; do
    SCRIPT_ARGS+=( -g "$g" )
  done
}

# Initialize once — the glob lists are constant across all classifier
# calls so we build the rg-arg arrays at script load time.
build_config_args
build_script_args

# True if the ecosystem filter is empty (= run all) or matches the arg.
ecosystem_enabled() {
  [[ -z "$ECOSYSTEM_FILTER" || "$ECOSYSTEM_FILTER" == "$1" ]]
}

# --------------------------------------------------------------------
# Header
# --------------------------------------------------------------------
printf 'ecosystem\tpackage\tclassification\tevidence\n'

# --------------------------------------------------------------------
# Classifier: npm (web/package.json)
# --------------------------------------------------------------------
audit_npm() {
  local pkg_json="web/package.json"
  if [[ ! -f "$pkg_json" ]]; then
    return 0
  fi

  # All direct deps (dependencies + devDependencies), sorted.
  local deps
  deps="$(jq -r '
    (.dependencies // {}) * (.devDependencies // {})
    | keys[]
  ' "$pkg_json" | LC_ALL=C sort -u)"

  local pkg evidence classification runtime_pkg
  while IFS= read -r pkg; do
    [[ -z "$pkg" ]] && continue
    evidence=""
    classification="PHANTOM"

    # 0) Ambient TypeScript type packages (`@types/<pkg>`) are loaded by
    #    `tsc` via automatic node-modules `@types/` resolution — no
    #    source-level `import` syntax exists. Treat them as
    #    USED-VIA-CONFIG when the corresponding runtime package is
    #    present in deps OR when tsconfig.json is present (always true
    #    for the web workspace; the typescript dep itself confirms it).
    if [[ "$pkg" == "@types/"* ]]; then
      runtime_pkg="${pkg#@types/}"
      # Special-case @types/node — Node ambient types, no runtime pkg.
      if [[ "$runtime_pkg" == "node" ]]; then
        evidence="web/tsconfig.json (ambient @types/node)"
      else
        # Check whether the corresponding runtime pkg is declared.
        if jq -e --arg p "$runtime_pkg" '
          ((.dependencies // {}) * (.devDependencies // {}))
          | has($p)
        ' "$pkg_json" >/dev/null 2>&1; then
          evidence="web/tsconfig.json (ambient @types/${runtime_pkg})"
        else
          # Orphan @types/X with no runtime X — that IS a phantom.
          emit_row "npm" "$pkg" "PHANTOM" ""
          continue
        fi
      fi
      emit_row "npm" "$pkg" "USED-VIA-CONFIG" "$evidence"
      continue
    fi

    # 1) USED — direct import / require in JS/TS sources under web/.
    #    Patterns covered:
    #      import x from "pkg"
    #      import { a, b } from "pkg"
    #      import "pkg"
    #      import("pkg")
    #      require("pkg")
    #      from "pkg" (re-exports)
    #    `pkg` matches both bare and scoped (@scope/name); rg handles
    #    the literal `@` in scoped names without trouble.
    #
    #    Exclude config files (eslint.config.*, vitest.config.*, etc.) —
    #    they get the USED-VIA-CONFIG classification on the next probe
    #    even when they happen to contain `import` syntax.
    evidence="$(rg_regex \
      -g 'web/**/*.ts' -g 'web/**/*.tsx' -g 'web/**/*.js' \
      -g 'web/**/*.mjs' -g 'web/**/*.cjs' -g 'web/**/*.jsx' \
      -g '!**/node_modules/**' \
      -g '!**/*.config.*' -g '!**/eslint.config.*' \
      -g '!**/.eslintrc*' -g '!**/.prettierrc*' \
      -g '!**/tsconfig*.json' \
      "(from|import|require)\s*\(?\s*[\"']${pkg}(/|[\"'])" \
      | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED"
      emit_row "npm" "$pkg" "$classification" "$evidence"
      continue
    fi

    # 2) USED-VIA-CONFIG — referenced in an allowlisted config file.
    #    Bare-name match anywhere on a line; the config files are small
    #    enough that we don't need stricter regex.
    evidence="$(rg_fixed "${CONFIG_ARGS[@]}" -g '!**/node_modules/**' "$pkg" | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED-VIA-CONFIG"
      emit_row "npm" "$pkg" "$classification" "$evidence"
      continue
    fi

    # 3) USED-VIA-SCRIPT — referenced in scripts: blocks / justfile /
    #    workflows. We match the bare package name; CLI tools like
    #    `shadcn` (invoked via `npx shadcn`) and `next` (invoked via
    #    `next build` in the scripts: block) both land here.
    #
    #    Filters applied:
    #      a) Skip the package's own declaration line in package.json
    #         (`"lucide-react": "^1.16.0"` — would otherwise self-match
    #         every dep).
    #      b) Skip @types/<pkg> declarations — the substring `react-dom`
    #         appearing inside `"@types/react-dom"` would falsely
    #         classify `react-dom` as USED.
    #      c) Skip this audit script itself — it enumerates dep names
    #         as data and is not a consumer.
    #      d) Use ripgrep's `--word-regexp` semantics on the regex
    #         variant to avoid substring matches like `react` inside
    #         `react-dom`.
    evidence="$(rg_regex "${SCRIPT_ARGS[@]}" \
        -g '!**/node_modules/**' \
        -g '!scripts/audit-deps.sh' \
        -g '!scripts/audit-deps_test.sh' \
        "(^|[^[:alnum:]/_.-])${pkg//./\\.}([^[:alnum:]/_.-]|$)" \
      | rg -v "\"${pkg//./\\.}\"[[:space:]]*:" \
      | rg -v "@types/" \
      | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED-VIA-SCRIPT"
      emit_row "npm" "$pkg" "$classification" "$evidence"
      continue
    fi

    # 4) PHANTOM — fall through.
    emit_row "npm" "$pkg" "PHANTOM" ""
  done <<<"$deps"
}

# --------------------------------------------------------------------
# Classifier: go (root go.mod)
# --------------------------------------------------------------------
audit_go() {
  local gomod="go.mod"
  if [[ ! -f "$gomod" ]]; then
    return 0
  fi

  # Direct deps only — i.e. lines in `require (...)` blocks that are NOT
  # marked `// indirect`. Strip the leading tab and the version + comment.
  # The grammar supports both single-line `require` and the block form;
  # we handle the block form (which is what `gofmt -mod=mod` emits).
  local deps
  deps="$(awk '
    /^require[[:space:]]*\(/ { inblock = 1; next }
    inblock && /^\)/        { inblock = 0; next }
    inblock {
      # Skip indirect deps.
      if ($0 ~ /\/\/ indirect/) next
      # First field is the module path.
      print $1
    }
  ' "$gomod" | LC_ALL=C sort -u)"

  local pkg evidence classification phantom_count=0
  while IFS= read -r pkg; do
    [[ -z "$pkg" ]] && continue
    evidence=""
    classification="PHANTOM"

    # USED — appears in a Go import statement. Quoted import path is the
    # canonical signal; we match `"pkg"` and `"pkg/<sub>"`.
    evidence="$(rg_regex -g '**/*.go' -g '!vendor/**' -g '!**/node_modules/**' \
      "\"${pkg}(/[^\"]*)?\"" | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED"
      emit_row "go" "$pkg" "$classification" "$evidence"
      continue
    fi

    # No USED-VIA-CONFIG / USED-VIA-SCRIPT path for go-modules: there
    # are no config-file references to Go packages, and `go run <pkg>`
    # invocations would still need an import elsewhere in the tree.
    emit_row "go" "$pkg" "PHANTOM" ""
    phantom_count=$((phantom_count + 1))
  done <<<"$deps"

  # P0-A2 — emit a recommendation to stderr if any go phantoms surfaced.
  # The script never edits go.mod; the maintainer runs `go mod tidy`.
  if (( phantom_count > 0 )); then
    echo "audit-deps: go ecosystem has $phantom_count PHANTOM direct dep(s); run 'go mod tidy' to let Go reconcile and re-run the audit." >&2
  fi
}

# --------------------------------------------------------------------
# Classifier: pip-bridge (oscal-bridge/pyproject.toml)
# --------------------------------------------------------------------
audit_pip_bridge() {
  local pyp="oscal-bridge/pyproject.toml"
  if [[ ! -f "$pyp" ]]; then
    return 0
  fi

  # Extract the package name from each `[project] dependencies` entry.
  # Lines look like:  "compliance-trestle>=4.0.2,<4.1.0",
  # We strip quotes, the version specifier (everything from the first of
  # =, <, >, !, ~, ;, [, space), and trailing commas.
  local deps
  deps="$(awk '
    /^\[project\.optional-dependencies\]/ { in_proj = 0 }
    /^\[project\]/                         { in_proj = 1; next }
    /^\[/ && !/^\[project\]/               { in_proj = 0 }
    in_proj && /^dependencies[[:space:]]*=/ { in_deps = 1; next }
    in_deps && /^\]/                       { in_deps = 0 }
    in_deps {
      line = $0
      # strip leading whitespace + quotes + trailing comma
      gsub(/^[[:space:]]*"?/, "", line)
      gsub(/"?,?[[:space:]]*(#.*)?$/, "", line)
      # split on first version-specifier character
      n = match(line, /[<>=!~; \[]/)
      if (n > 0) line = substr(line, 1, n-1)
      if (length(line) > 0) print line
    }
  ' "$pyp" | LC_ALL=C sort -u)"

  local pkg evidence classification import_name
  while IFS= read -r pkg; do
    [[ -z "$pkg" ]] && continue
    evidence=""
    classification="PHANTOM"

    # Python pip-name → import-name mapping. Most packages follow the
    # PEP 8 hyphen-to-underscore convention (`compliance-trestle` →
    # `compliance_trestle`); a meaningful minority do not. Common
    # exceptions are enumerated here. New entries should land in the
    # slice's decisions log so the rationale is preserved.
    import_name="${pkg//-/_}"
    declare -a candidates=( "$pkg" "$import_name" )
    case "$pkg" in
      compliance-trestle)  candidates+=( "trestle" ) ;;
      grpcio)              candidates+=( "grpc" ) ;;
      grpcio-tools)        candidates+=( "grpc_tools" ) ;;
      pyyaml)              candidates+=( "yaml" ) ;;
      pillow)              candidates+=( "PIL" ) ;;
      beautifulsoup4)      candidates+=( "bs4" ) ;;
      pycryptodome)        candidates+=( "Crypto" ) ;;
      python-dateutil)     candidates+=( "dateutil" ) ;;
    esac

    # Build a regex alternation from candidates.
    local pattern=""
    local cand
    for cand in "${candidates[@]}"; do
      [[ -z "$pattern" ]] || pattern+="|"
      pattern+="${cand//./\\.}"
    done

    evidence="$(rg_regex -g 'oscal-bridge/**/*.py' \
      "^(import|from)[[:space:]]+(${pattern})([[:space:]]|\\.|$)" \
      | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED"
      emit_row "pip-bridge" "$pkg" "$classification" "$evidence"
      continue
    fi

    # USED-VIA-CONFIG — referenced in a [tool.*] section of any
    # pyproject.toml under the bridge. Match the package name appearing
    # after `[tool.` (e.g. `[tool.ruff]` for ruff, `[tool.hatch.build]`
    # for hatch).
    evidence="$(rg_regex -g 'oscal-bridge/**/pyproject.toml' -g 'pyproject.toml' \
      "^\\[tool\\.${pkg//./\\.}(\\.|\\])" | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED-VIA-CONFIG"
      emit_row "pip-bridge" "$pkg" "$classification" "$evidence"
      continue
    fi

    # USED-VIA-SCRIPT — referenced in justfile / workflows / shell
    # scripts. Probe each candidate name (pip pkg name + the alias map
    # entries above) so build-time codegen invocations are recognized
    # — e.g. `python -m grpc_tools.protoc` should classify
    # `grpcio-tools` as USED-VIA-SCRIPT.
    for cand in "${candidates[@]}"; do
      evidence="$(rg_fixed "${SCRIPT_ARGS[@]}" "$cand" | head -1 || true)"
      [[ -n "$evidence" ]] && break
    done
    if [[ -n "$evidence" ]]; then
      classification="USED-VIA-SCRIPT"
      emit_row "pip-bridge" "$pkg" "$classification" "$evidence"
      continue
    fi

    emit_row "pip-bridge" "$pkg" "PHANTOM" ""
  done <<<"$deps"
}

# --------------------------------------------------------------------
# Classifier: pip-docs (docs-site/requirements.txt)
# --------------------------------------------------------------------
audit_pip_docs() {
  local req="docs-site/requirements.txt"
  if [[ ! -f "$req" ]]; then
    return 0
  fi

  # Strip comments, blank lines, and version specifiers.
  local deps
  deps="$(awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    {
      line = $0
      sub(/[[:space:]]*#.*$/, "", line)
      n = match(line, /[<>=!~; \[]/)
      if (n > 0) line = substr(line, 1, n-1)
      gsub(/[[:space:]]/, "", line)
      if (length(line) > 0) print line
    }
  ' "$req" | LC_ALL=C sort -u)"

  local pkg evidence classification
  while IFS= read -r pkg; do
    [[ -z "$pkg" ]] && continue
    evidence=""
    classification="PHANTOM"

    # mkdocs plugins / themes are referenced by their plugin slug, which
    # is usually a substring of the pip package name (e.g. pip pkg
    # `mkdocs-git-revision-date-localized-plugin` → mkdocs slug
    # `git-revision-date-localized`). The safest heuristic is to look
    # for the full pip package name OR its stripped form (without the
    # `mkdocs-` prefix / `-plugin` suffix) inside docs-site/mkdocs.yml.
    local stripped="$pkg"
    stripped="${stripped#mkdocs-}"
    stripped="${stripped%-plugin}"

    evidence="$(rg_regex -g 'docs-site/mkdocs.yml' \
      "(${pkg//./\\.}|${stripped//./\\.})" | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED-VIA-CONFIG"
      emit_row "pip-docs" "$pkg" "$classification" "$evidence"
      continue
    fi

    # USED-VIA-SCRIPT — referenced in justfile / workflows / docs-publish.
    evidence="$(rg_fixed "${SCRIPT_ARGS[@]}" "$pkg" | head -1 || true)"
    if [[ -n "$evidence" ]]; then
      classification="USED-VIA-SCRIPT"
      emit_row "pip-docs" "$pkg" "$classification" "$evidence"
      continue
    fi

    emit_row "pip-docs" "$pkg" "PHANTOM" ""
  done <<<"$deps"
}

# --------------------------------------------------------------------
# Run the requested ecosystems.
# --------------------------------------------------------------------
ecosystem_enabled "npm"        && audit_npm
ecosystem_enabled "go"         && audit_go
ecosystem_enabled "pip-bridge" && audit_pip_bridge
ecosystem_enabled "pip-docs"   && audit_pip_docs

exit 0
