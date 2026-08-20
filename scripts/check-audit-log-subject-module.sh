#!/usr/bin/env bash
#
# check-audit-log-subject-module.sh - OE-451 / PRIV-7
#
# Assert that every audit-log-family table created in migrations carries the
# slice-180 `subject_module TEXT NOT NULL DEFAULT 'core'` marker, unless it is
# one of the three pre-slice-180 tables deliberately scoped out by that slice.
#
# This is the discovery primitive for the subject_module pre-commitment: a new
# audit-log table cannot silently land without either adding the column in the
# creating/companion migration or deliberately changing this rule.

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MIGRATIONS_DIR="${AUDIT_LOG_SUBJECT_MODULE_MIGRATIONS_DIR:-$REPO_ROOT/migrations/sql}"

if [[ ! -d "$MIGRATIONS_DIR" ]]; then
  echo "check-audit-log-subject-module: migrations dir not found: $MIGRATIONS_DIR" >&2
  exit 2
fi

created_tmp="$(mktemp)"
covered_tmp="$(mktemp)"
allow_tmp="$(mktemp)"
files_tmp="$(mktemp)"
trap 'rm -f "$created_tmp" "$covered_tmp" "$allow_tmp" "$files_tmp"' EXIT

read -r -d '' ALLOWED_WITHOUT_SUBJECT_MODULE <<'ALLOWLIST' || true
artifact_access_log
decisions_audit
audit_sink_failures
ALLOWLIST

printf '%s\n' "$ALLOWED_WITHOUT_SUBJECT_MODULE" | sed '/^[[:space:]]*$/d' | sort -u > "$allow_tmp"

find "$MIGRATIONS_DIR" -maxdepth 1 -type f -name '*.sql' ! -name '*.down.sql' | sort > "$files_tmp"
if [[ ! -s "$files_tmp" ]]; then
  echo "check-audit-log-subject-module: no forward migration files found in $MIGRATIONS_DIR" >&2
  exit 2
fi

# Audit-log-family table names are the historically-used forms in this repo:
# *_audit_log, *_audit, *_log, plus audit_sink_failures from slice 126.
xargs grep -hE '^[[:space:]]*CREATE TABLE( IF NOT EXISTS)?[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]*\(' < "$files_tmp" 2>/dev/null \
  | sed -E 's/^[[:space:]]*CREATE TABLE( IF NOT EXISTS)?[[:space:]]+([A-Za-z_][A-Za-z0-9_]*).*/\2/' \
  | grep -E '(_audit_log|_audit|_log|^audit_sink_failures)$' \
  | sort -u > "$created_tmp" || true

while IFS= read -r table; do
  [[ -z "$table" ]] && continue
  if xargs grep -hE "^[[:space:]]*ALTER TABLE[[:space:]]+$table([[:space:]]|$)" < "$files_tmp" 2>/dev/null \
    | grep -q 'ALTER TABLE'; then
    if xargs grep -lE "^[[:space:]]*ALTER TABLE[[:space:]]+$table([[:space:]]|$)" < "$files_tmp" 2>/dev/null \
      | xargs grep -h 'subject_module' >/dev/null 2>&1; then
      echo "$table"
      continue
    fi
  fi

  # Future creating migrations may include the column directly in CREATE TABLE.
  if awk -v table="$table" '
    $0 ~ "^[[:space:]]*CREATE TABLE( IF NOT EXISTS)?[[:space:]]+" table "[[:space:]]*\\(" { in_table=1 }
    in_table && /subject_module/ { found=1 }
    in_table && /^[[:space:]]*\);/ { in_table=0 }
    END { exit found ? 0 : 1 }
  ' $(< "$files_tmp"); then
    echo "$table"
  fi
done < "$created_tmp" | sort -u > "$covered_tmp"

missing="$(comm -23 "$created_tmp" <(cat "$covered_tmp" "$allow_tmp" | sort -u))"
if [[ -n "$missing" ]]; then
  echo "check-audit-log-subject-module: FAIL - audit-log-family table(s) lack subject_module:" >&2
  while IFS= read -r table; do
    [[ -z "$table" ]] && continue
    echo "    - $table" >&2
  done <<< "$missing"
  echo "" >&2
  echo "Fix: add subject_module TEXT NOT NULL DEFAULT 'core' via ADD COLUMN IF NOT EXISTS" >&2
  echo "or explicitly change the allowlist with a decision record. See CONTRIBUTING.md" >&2
  echo "'Module isolation discipline' and docs/audit-log/180-privacy-module-foundation-decisions.md." >&2
  exit 1
fi

created_n="$(wc -l < "$created_tmp" | tr -d ' ')"
covered_n="$(wc -l < "$covered_tmp" | tr -d ' ')"
allow_n="$(wc -l < "$allow_tmp" | tr -d ' ')"
echo "check-audit-log-subject-module: OK - ${created_n} audit-log-family table(s); ${covered_n} carry subject_module; ${allow_n} deliberately scoped out."
