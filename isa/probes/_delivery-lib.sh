# shellcheck shell=bash
#
# _delivery-lib.sh -- shared by the database probes for isa/assessor-delivery.md
# (ISC-1, -2, -3, -4, -6, -7). Sourced, never run. The leading underscore keeps
# it out of isa/check-probes.sh and isa/run-probes.sh: it answers no claim.
#
# Why one shared file. Assessor delivery is unbuilt (the epic is `phase:
# specified`). Each probe has to state what `assessor_destinations` and
# `assessor_deliveries` look like in order to query them, and six probes each
# guessing a different shape -- which is what the retired engine's August
# drafts did (#1625-#1631) -- would leave six queries to reconcile against the
# real migration instead of one. The assumed shape is written once, below, as
# DL_SCHEMA. When the migration lands and differs, a probe's query errors
# against the real tables and the probe exits 3 naming this file; that is the
# one place to reconcile.
#
# The assumed shape, from the epic's claims and Not yet specified answers:
#
#   assessor_destinations  one row per registered endpoint. `endpoint` is
#                          immutable: changing where a destination points is a
#                          new row, so a past delivery still joins to the
#                          endpoint it was sent to (ISC-1). The credential is
#                          held as `credential_ref`, a `scheme://` reference
#                          (op://, vault://, env://) resolved at delivery
#                          time. An encrypted credential would be a separate
#                          `*_ciphertext` column. Never plaintext (ISC-6).
#   assessor_deliveries    one row per departure, appended, never updated in
#                          place (redelivery is a new row). `endpoint` is the
#                          address actually dialed. `manifest` is a JSON array
#                          of {evidence_record_id, content_hash} for every
#                          record transmitted, whatever the adapter says it
#                          accepted. `manifest_digest` is the digest of that
#                          manifest taken at send time, in the canonical form
#                          DL_DIGEST below.
#
# Verdicts, and why "unbuilt" is exit 1. A probe reports FALSE when the tables
# its claim governs do not exist. The one way evidence leaves today is the
# OSCAL bundle download (internal/api/oscalexport), which goes to whichever
# caller asks, records no departure, and passes through no delivery gate. So
# the claims are false of the platform as it stands, not unverifiable.
# isa/probes/delivery-open-adapter.sh (ISC-8) already answers absence the same
# way. Exit 0 is never reached on nothing: once the tables exist, a probe whose
# check finds no violation still exits 3 if they hold no rows (dl_require_rows),
# as isa/probes/no-duplicate-controls.sh does for an empty catalog. A database
# that is not migrated at all (no evidence_records) is exit 3, not FALSE.
#
# Self-test, every run. Before it reads the real schema, each probe runs its
# own check query inside rolled-back transactions, against temporary tables
# built from DL_SCHEMA (temporary tables shadow public ones of the same name,
# so the query runs unchanged): once on DL_BASE, a clean fixture that must
# produce no rows, and once per violation the probe names, on DL_BASE plus that
# violation, each of which must produce at least one. If any answers wrong the
# query is broken, nothing it says about the real tables can be trusted, and
# the probe exits 3.
#
# Env:
#   DATABASE_URL  a role that sees every tenant's rows: a superuser or a
#                 BYPASSRLS role. Through RLS a cross-tenant scan sees nothing
#                 and would pass vacuously, so the probes refuse any other role.

# DL_DIGEST -- the canonical digest of a set of (record id, hash) pairs, as a
# SQL aggregate over rows exposing `rid` (text) and `rhash` (text): sha256 of
# the "id:hash" lines in id order, joined by newlines, hex-encoded.
DL_DIGEST="encode(sha256(convert_to(coalesce(string_agg(rid || ':' || rhash, E'\\n' ORDER BY rid), ''), 'UTF8')), 'hex')"

DL_SCHEMA="
CREATE TEMP TABLE evidence_records (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL,
  observed_at timestamptz NOT NULL, hash text NOT NULL);
CREATE TEMP TABLE audit_periods (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, frozen_at timestamptz);
CREATE TEMP TABLE assessor_destinations (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, adapter text NOT NULL,
  endpoint text NOT NULL, credential_ref text);
CREATE TEMP TABLE assessor_deliveries (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL,
  destination_id uuid, audit_period_id uuid,
  endpoint text NOT NULL, status text NOT NULL,
  manifest jsonb NOT NULL DEFAULT '[]', manifest_digest text NOT NULL DEFAULT '',
  delivered_by text, ai_assisted boolean NOT NULL DEFAULT false,
  human_approved boolean NOT NULL DEFAULT false, human_approver text,
  receipt jsonb, delivered_at timestamptz);
"

# Tenant A (...a1) owns a destination, a period frozen on 2026-02-01, and two
# records observed before the freeze; one delivery sends exactly those two,
# AI-assisted and approved. Tenant B's record (...e9) and A's post-freeze
# record (...e5) exist so a probe's violation can reach for them.
DL_BASE="
INSERT INTO evidence_records VALUES
  ('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000a1', '2026-01-10T00:00:00Z', 'hash-e1'),
  ('00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000a1', '2026-01-20T00:00:00Z', 'hash-e2'),
  ('00000000-0000-0000-0000-0000000000e5', '00000000-0000-0000-0000-0000000000a1', '2026-03-01T00:00:00Z', 'hash-e5'),
  ('00000000-0000-0000-0000-0000000000e9', '00000000-0000-0000-0000-0000000000b1', '2026-01-10T00:00:00Z', 'hash-e9');
INSERT INTO audit_periods VALUES
  ('00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-0000000000a1', '2026-02-01T00:00:00Z');
INSERT INTO assessor_destinations VALUES
  ('00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-0000000000a1', 'oscal-http',
   'https://intake.example.test/v1', 'op://atlas/assessor/credential');
INSERT INTO assessor_deliveries (id, tenant_id, destination_id, audit_period_id, endpoint, status,
                                 manifest, delivered_by, ai_assisted, human_approved, human_approver)
VALUES ('00000000-0000-0000-0000-000000000011', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-0000000000f1',
        'https://intake.example.test/v1', 'delivered',
        '[{\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e1\", \"content_hash\": \"hash-e1\"},
          {\"evidence_record_id\": \"00000000-0000-0000-0000-0000000000e2\", \"content_hash\": \"hash-e2\"}]',
        'operator@example.test', true, true, 'operator@example.test');
"

# DL_SEAL -- set every delivery's manifest_digest from its own manifest, the
# way the delivery path would at send time. A violation that adds a delivery
# runs it again.
DL_SEAL="
UPDATE assessor_deliveries d SET manifest_digest = (
  SELECT $DL_DIGEST FROM (
    SELECT m->>'evidence_record_id' AS rid, m->>'content_hash' AS rhash
    FROM jsonb_array_elements(d.manifest) m) s);
"

dl_psql() {
  psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -At -F $'\t' "$@"
}

# dl_run [PSQL_ARGS...] -- run psql with SQL on stdin (or -c), leaving stdout in
# DL_OUT and stderr in DL_ERR, and return psql's status. Kept apart so a
# NOTICE or WARNING on stderr is never read as a result row. Feed it with a
# here-string, never a pipe: a pipe runs it in a subshell and its DL_OUT is
# lost, leaving the previous call's.
dl_run() {
  local errf rc
  errf=$(mktemp) || return 1
  DL_OUT=$(dl_psql "$@" 2>"$errf")
  rc=$?
  DL_ERR=$(cat "$errf")
  rm -f "$errf"
  return "$rc"
}

# dl_preflight NAME -- exit 3 unless psql, DATABASE_URL, a connection, a role
# that sees every tenant and a migrated schema are all present.
dl_preflight() {
  local name=$1
  if ! command -v psql >/dev/null 2>&1; then
    echo "$name: CANNOT RUN -- psql not on PATH" >&2
    exit 3
  fi
  if [[ -z "${DATABASE_URL:-}" ]]; then
    echo "$name: CANNOT RUN -- DATABASE_URL is not set" >&2
    exit 3
  fi
  if ! dl_run -c "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user"; then
    echo "$name: CANNOT RUN -- cannot query Postgres via DATABASE_URL: $DL_ERR" >&2
    exit 3
  fi
  if [[ "$DL_OUT" != "t" ]]; then
    echo "$name: CANNOT RUN -- the DATABASE_URL role is neither superuser nor BYPASSRLS, so RLS would hide other tenants' rows and the scan would pass on nothing" >&2
    exit 3
  fi
  if ! dl_run -c "SELECT to_regclass('public.evidence_records') IS NOT NULL" || [[ "$DL_OUT" != "t" ]]; then
    echo "$name: CANNOT RUN -- no evidence_records table; the schema is not migrated (just migrate-up) ${DL_ERR}" >&2
    exit 3
  fi
}

# dl_selftest NAME CHECK_SQL VIOLATION_SQL... -- see the header. Exits 3 if the
# check misjudges the clean fixture or any violation; returns otherwise.
dl_selftest() {
  local name=$1 check=$2 violation n=0
  shift 2
  if ! dl_run <<<"BEGIN;$DL_SCHEMA$DL_BASE$DL_SEAL$check
ROLLBACK;"; then
    echo "$name: CANNOT RUN -- self-test: the check errored on the clean fixture: $DL_ERR" >&2
    exit 3
  fi
  if [[ -n "$DL_OUT" ]]; then
    echo "$name: CANNOT RUN -- self-test: the check reported a clean fixture false:" >&2
    printf '%s\n' "$DL_OUT" >&2
    exit 3
  fi
  for violation in "$@"; do
    n=$((n + 1))
    if ! dl_run <<<"BEGIN;$DL_SCHEMA$DL_BASE$DL_SEAL$violation
$check
ROLLBACK;"; then
      echo "$name: CANNOT RUN -- self-test: the check errored on violating fixture $n: $DL_ERR" >&2
      exit 3
    fi
    if [[ -z "$DL_OUT" ]]; then
      echo "$name: CANNOT RUN -- self-test: the check passed violating fixture $n" >&2
      exit 3
    fi
  done
  echo "$name: self-test ok (clean fixture holds, $n violating fixture(s) caught)"
}

# dl_require_tables NAME CLAIM REASON TABLE... -- exit 1 with REASON if any
# TABLE is absent from public. See "unbuilt is exit 1" in the header.
dl_require_tables() {
  local name=$1 claim=$2 reason=$3 t missing=""
  shift 3
  for t in "$@"; do
    if ! dl_run -c "SELECT to_regclass('public.$t') IS NOT NULL"; then
      echo "$name: CANNOT RUN -- catalog lookup for $t failed: $DL_ERR" >&2
      exit 3
    fi
    [[ "$DL_OUT" == "t" ]] || missing="$missing $t"
  done
  if [[ -n "$missing" ]]; then
    echo "$name: FALSE ($claim) -- no table:$missing. $reason"
    exit 1
  fi
}

# dl_check NAME CLAIM CHECK_SQL HEADER -- run CHECK_SQL against the real
# tables. Rows are violations: print them under HEADER and exit 1. An error
# means the real schema differs from DL_SCHEMA: exit 3.
dl_check() {
  local name=$1 claim=$2 check=$3 header=$4
  if ! dl_run <<<"$check"; then
    echo "$name: CANNOT RUN -- the check errored against the real tables; reconcile DL_SCHEMA in isa/probes/_delivery-lib.sh with the migration: $DL_ERR" >&2
    exit 3
  fi
  if [[ -n "$DL_OUT" ]]; then
    echo "$name: FALSE ($claim) -- violating rows:"
    printf '%s\n' "$header" "$DL_OUT"
    exit 1
  fi
}

# dl_require_rows NAME TABLE... -- run after dl_check found nothing. Exit 3 if
# any TABLE is empty: a check over no rows has shown nothing, and exit 0 there
# would let a claim close before a single delivery was made.
dl_require_rows() {
  local name=$1 t
  shift
  for t in "$@"; do
    if ! dl_run -c "SELECT EXISTS (SELECT 1 FROM public.$t)"; then
      echo "$name: CANNOT RUN -- row count for $t failed: $DL_ERR" >&2
      exit 3
    fi
    if [[ "$DL_OUT" != "t" ]]; then
      echo "$name: CANNOT RUN -- $t has no rows; no violation found, but there is nothing to check yet" >&2
      exit 3
    fi
  done
}
