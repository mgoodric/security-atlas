#!/usr/bin/env bash
#
# check-migration-timestamps.sh — #1880
#
# Fail when two migrations under migrations/sql/ share a timestamp prefix.
#
# A migration is named <timestamp>_<name>.sql, with an optional
# <timestamp>_<name>.down.sql. `just migrate-up` and CI apply them in glob
# order, and the ledger keys on the filename, so two names on one prefix
# still apply. But the order between them is decided by <name>, not by
# when they were written, which is the order the prefix exists to fix.
# Two branches that each pick "the next timestamp" collide this way; this
# check makes the second one renumber before it merges.
#
# GRANDFATHERED lists the migrations that already shared a prefix when the
# check landed (six prefixes, not only the 20260811000000 pair #1880
# named). A prefix passes if every migration on it is grandfathered, so a
# new migration on a grandfathered prefix still fails.
#
# With MIGRATIONS_BASE_REF set to a git revision (CI passes main's tip,
# fetched when the job runs), it also fails when a migration in the
# directory that the base revision lacks carries a timestamp less than or
# equal to the base's latest (#2883). A PR's pull_request run checks the
# tree as it stood when the run started; once another PR lands the same
# "next timestamp" on main, that run stays green over a collision. Comparing
# against main at check time catches the PR that is behind; it still only
# runs when the PR's checks run.
#
# Exit codes:
#   0 — no prefix carries two migrations, outside the grandfathered set,
#       and no new migration is at or below the base's latest
#   1 — at least one of those holds
#   2 — the migrations directory is not readable, or the base ref is not
#
# Usage:
#   bash scripts/check-migration-timestamps.sh
#   MIGRATIONS_DIR=/path/to/sql bash scripts/check-migration-timestamps.sh
#   MIGRATIONS_BASE_REF=FETCH_HEAD bash scripts/check-migration-timestamps.sh
#
# Self-test:
#   bash scripts/check-migration-timestamps_test.sh

set -euo pipefail

DIR="${MIGRATIONS_DIR:-migrations/sql}"

# Migrations (without .sql / .down.sql) that shared a prefix before #1880.
# They are applied and in the ledger under these names; renaming them would
# re-apply them on every existing install. Never add to this list: renumber
# the new migration instead.
GRANDFATHERED="
20260511000000_extensions
20260511000000_init
20260608000000_oscal_imported_profiles
20260608000000_slack_webhook_channels
20260612110000_access_review_campaigns
20260612110000_personnel_security_workflows
20260612110000_questionnaire_answer_runs
20260612110000_questionnaire_mapping_proposals
20260730000000_security_awareness_training
20260730000000_vendor_commercial_fields
20260804000000_evidence_receipt_status_lookup
20260804000000_incident_register
20260811000000_aggregation_evaluation_meta_risk_pointer
20260811000000_oauth_client_tenant_binding
"

if [[ ! -d "$DIR" || ! -r "$DIR" ]]; then
  echo "check-migration-timestamps: migrations directory not readable: $DIR" >&2
  exit 2
fi

# One line per migration: "<prefix> <base>", where base drops .down.sql /
# .sql so an up and its down count once.
pairs="$(
  find "$DIR" -maxdepth 1 -type f -name '*.sql' -exec basename {} \; |
    sed -E 's/(\.down)?\.sql$//' |
    sort -u |
    awk -F_ '{ print $1, $0 }'
)"

dup_prefixes="$(printf '%s\n' "$pairs" | awk 'NF { print $1 }' | uniq -d)"

status=0
for prefix in $dup_prefixes; do
  bases="$(printf '%s\n' "$pairs" | awk -v p="$prefix" '$1 == p { print $2 }')"
  ungrandfathered=""
  for base in $bases; do
    if ! printf '%s\n' "$GRANDFATHERED" | grep -qxF "$base"; then
      ungrandfathered="$ungrandfathered $base"
    fi
  done
  if [[ -n "$ungrandfathered" ]]; then
    echo "check-migration-timestamps: timestamp $prefix is used by more than one migration:" >&2
    for base in $bases; do
      echo "  $base" >&2
    done
    echo "  renumber the newer one to a timestamp no migration uses." >&2
    status=1
  fi
done

if [[ -n "${MIGRATIONS_BASE_REF:-}" ]]; then
  # ls-tree prints paths relative to the -C directory, so this lists the
  # base revision's copy of the same directory by bare filename.
  if ! base_files="$(git -C "$DIR" ls-tree --name-only "$MIGRATIONS_BASE_REF" -- . 2>/dev/null)"; then
    echo "check-migration-timestamps: base ref not readable: $MIGRATIONS_BASE_REF" >&2
    exit 2
  fi
  base_bases="$(printf '%s\n' "$base_files" | grep -E '\.sql$' | sed -E 's/(\.down)?\.sql$//' | sort -u || true)"
  base_max="$(printf '%s\n' "$base_bases" | awk -F_ 'NF { print $1 }' | sort | tail -n 1)"
  if [[ -n "$base_max" ]]; then
    while read -r prefix base; do
      [[ -n "$base" ]] || continue
      # A here-string, not a pipe: grep -q closing early would SIGPIPE
      # printf, and pipefail would read a match as a miss.
      if grep -qxF "$base" <<<"$base_bases"; then
        continue
      fi
      if [[ ! "$prefix" > "$base_max" ]]; then
        echo "check-migration-timestamps: new migration $base is not after the base's latest timestamp $base_max ($MIGRATIONS_BASE_REF)." >&2
        echo "  merge main and renumber it to a timestamp after $base_max." >&2
        status=1
      fi
    done <<<"$pairs"
  fi
fi

if [[ $status -eq 0 ]]; then
  echo "check-migration-timestamps: OK"
fi
exit $status
