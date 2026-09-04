#!/usr/bin/env bash
#
# delivery-secret-redaction.sh — falsifier for isa/assessor-delivery.md
# ISC-6 (D3 · the egress boundary): "No destination credential is readable
# back." Falsifier: a plaintext credential in an API response, CLI output,
# log line, error string or ledger row.
#
# The check has two phases, same shape as the sibling D1/D2 probes
# (delivery-registered-only.sh, delivery-replay.sh, delivery-approver-guard.sh,
# delivery-freeze-horizon.sh), because the feature it guards has not shipped:
#
#   Phase 1 (today). `assessor_destinations` does not exist as a table
#   anywhere in migrations/sql/, and no Go source under internal/, cmd/,
#   connectors/ or web/ names an "assessor" symbol at all (checked below,
#   not assumed). That means there is no location where a destination
#   credential is even stored — no column, no Go type, no redaction
#   contract. A guarantee that "no credential is readable back" cannot hold
#   when nothing yet reads a credential FROM anywhere: the redaction guard
#   has no attachment point. That is ISC-6 failing today, not an unrunnable
#   probe — the missing piece is the feature under test, not the ability to
#   test it (same reasoning delivery-approver-guard.sh and
#   delivery-registered-only.sh use for ISC-3 and ISC-1).
#
#   This repo already carries the shape assessor-delivery is expected to
#   follow: internal/llm/cloud (Store + Crypter) stores a tenant's cloud-LLM
#   provider API key AES-256-GCM-encrypted in
#   tenant_llm_routing.api_key_ciphertext, exposes only a MaskedConfig
#   {HasAPIKey bool} to any API caller, and never persists, logs, or returns
#   the plaintext (internal/llm/cloud/store.go doc comment: "there is no
#   method that returns the plaintext to an API caller"). The epic's own
#   Constraints commit its egress path to reusing internal/notify's Secret /
#   ScrubSecret for the same purpose. Neither primitive is wired to
#   anything named "assessor" yet.
#
#   Phase 2 (once assessor_destinations ships). Two checks, both driven by
#   the schema and source tree rather than assumed:
#     2a. No column of assessor_destinations that looks like it holds a
#         credential (name matches credential|secret|token|api_key|apikey|
#         password|bearer|passphrase) is stored in a shape that isn't
#         obviously protected at rest — its name must also mention
#         ciphertext/encrypted/ref (mirroring
#         tenant_llm_routing.api_key_ciphertext). A bare `credential TEXT`
#         column is the plaintext-at-rest shape ISC-6's "ledger row"
#         disjunct forbids.
#     2b. assessor_deliveries — the departure ledger ISC-2 reads — carries
#         NO column matching that same credential-ish regex at all. The
#         ledger records what left and when; it has no reason to ever carry
#         destination credential material, encrypted or not.
#     2c. Every Go struct field in internal/ or cmd/ whose name matches the
#         credential-ish regex AND whose containing file mentions
#         "assessor" is typed as a Secret (notify.Secret / a package-local
#         Secret mirroring it, per internal/llm/cloud's precedent) or is a
#         private (lowercase) field. An exported plaintext-typed field with
#         no json:"-" tag is exactly the leak an API response or CLI print
#         of the containing struct would surface.
#
# Schema assumption for 2a/2b (not yet built — D3 is unimplemented): a
# single assessor_destinations table and a single assessor_deliveries
# table, per the Test Strategy's own naming. If the real migration splits
# credentials into a separate table, 2a's target table is the first thing
# to reconcile against that migration, not this probe's intent.
#
# Required env:
#   DATABASE_URL — full-catalog-visibility role (atlas_migrate / BYPASSRLS),
#                  same role scripts/audit-rls.sh and delivery-registered-only.sh /
#                  delivery-approver-guard.sh / delivery-freeze-horizon.sh use.
#                  This is schema/catalog introspection, not tenant-scoped
#                  data, so the migrate role is correct here, not
#                  DATABASE_URL_APP.
#
# Exit codes (the ISA falsifier contract):
#   0 — no destination-credential surface exists that isn't protected: no
#       plaintext-shaped column in assessor_destinations, no credential-ish
#       column in assessor_deliveries, no unguarded Go field
#   1 — the claim does not hold (see stderr for which reason)
#   3 — CANNOT RUN: no DATABASE_URL, no psql, no Postgres connection, or (in
#       phase 2) assessor_destinations exists but no credential-ish column
#       can be identified to check — the probe cannot tell which column is
#       the credential, which is a precondition gap, not a verdict

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

CRED_REGEX='(credential|secret|token|api_key|apikey|password|bearer|passphrase)'

if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "delivery-secret-redaction: CANNOT RUN -- DATABASE_URL is not set" >&2
  exit 3
fi

if ! command -v psql >/dev/null 2>&1; then
  echo "delivery-secret-redaction: CANNOT RUN -- psql not on PATH" >&2
  exit 3
fi

if ! psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc "SELECT 1" >/dev/null 2>&1; then
  echo "delivery-secret-redaction: CANNOT RUN -- cannot connect via DATABASE_URL" >&2
  exit 3
fi

has_destinations="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc \
  "SELECT to_regclass('public.assessor_destinations') IS NOT NULL;")"

if [[ "$has_destinations" != "t" ]]; then
  # Positive confirmation of absence, not a single failed query: also check
  # the source tree carries no "assessor" symbol at all, so this isn't a
  # migration that merely hasn't been applied to this database.
  source_hits="$(rg -il 'assessor' "$REPO_ROOT/internal" "$REPO_ROOT/cmd" "$REPO_ROOT/connectors" "$REPO_ROOT/web" \
    --glob '!*_test.go' --glob '!*.test.ts' 2>/dev/null || true)"

  cat >&2 <<MSG
delivery-secret-redaction: FALSE (ISC-6) — no assessor_destinations table
exists in the schema, and no Go or TypeScript source under internal/, cmd/,
connectors/ or web/ references an "assessor" symbol$(if [[ -n "$source_hits" ]]; then echo " (except: $source_hits)"; else echo " at all"; fi).
There is therefore no location where a destination credential is stored,
returned by an API, printed by a CLI, written to a log line, or embedded in
a ledger row — which means nothing today enforces that such a credential,
once it exists, is ever wrapped by internal/notify.Secret / ScrubSecret or
follows the encrypted-at-rest + masked-view shape internal/llm/cloud
(Store + Crypter, tenant_llm_routing.api_key_ciphertext) already
establishes elsewhere in this tree for an external provider credential. The
redaction guard has no attachment point to exist on.
MSG
  exit 1
fi

# --- Phase 2: assessor_destinations exists. --------------------------------

dest_cred_cols="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc "
  SELECT column_name FROM information_schema.columns
  WHERE table_schema = 'public' AND table_name = 'assessor_destinations'
    AND column_name ~* '$CRED_REGEX'
  ORDER BY column_name;
")"

if [[ -z "$dest_cred_cols" ]]; then
  echo "delivery-secret-redaction: CANNOT RUN -- assessor_destinations exists but no column name matches the credential-naming regex ($CRED_REGEX); cannot identify which column to check. Reconcile this probe's CRED_REGEX against the real migration." >&2
  exit 3
fi

plaintext_shaped=""
while IFS= read -r col; do
  [[ -z "$col" ]] && continue
  if [[ ! "$col" =~ (ciphertext|encrypted|_ref$|reference) ]]; then
    plaintext_shaped+="$col"$'\n'
  fi
done <<<"$dest_cred_cols"

if [[ -n "$plaintext_shaped" ]]; then
  echo "delivery-secret-redaction: FALSE (ISC-6) — assessor_destinations carries credential-shaped column(s) with no ciphertext/encrypted/ref naming to indicate at-rest protection:" >&2
  printf '%s' "$plaintext_shaped" >&2
  echo "A direct SELECT against this table (a ledger row) would return the credential in plaintext." >&2
  exit 1
fi

deliveries_cred_cols="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -Atqc "
  SELECT column_name FROM information_schema.columns
  WHERE table_schema = 'public' AND table_name = 'assessor_deliveries'
    AND column_name ~* '$CRED_REGEX'
  ORDER BY column_name;
")"

if [[ -n "$deliveries_cred_cols" ]]; then
  echo "delivery-secret-redaction: FALSE (ISC-6) — assessor_deliveries (the departure ledger ISC-2 reads) carries credential-shaped column(s); the ledger of what left has no reason to carry destination credential material at all:" >&2
  printf '%s\n' "$deliveries_cred_cols" >&2
  exit 1
fi

# --- Phase 2c: any Go struct field on the assessor surface that looks like
# a credential must be typed as a Secret or be unexported. Two passes on
# purpose: a single case-insensitive regex can't require BOTH "field name
# starts uppercase" (case-SENSITIVE — that's what "exported" means in Go)
# and "json tag matches the credential regex" (case-INsensitive) at once,
# so the field-shape/export check runs as a second, case-sensitive filter
# over the case-insensitive tag-match candidates rather than folding both
# into one -i regex, which would let a lowercase (unexported, already-safe)
# field name pass the [A-Z] class too and defeat the exported-only check.

if command -v rg >/dev/null 2>&1; then
  tag_candidates="$(rg -n -i --glob '*.go' --glob '!*_test.go' \
    "\`json:\"[a-z_]*($CRED_REGEX)" \
    "$REPO_ROOT/internal" "$REPO_ROOT/cmd" 2>/dev/null || true)"

  unguarded=""
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    file="${line%%:*}"
    content="${line#*:*:}"
    if [[ "$file" =~ [Aa][Ss][Ss][Ee][Ss][Ss][Oo][Rr] ]] \
      && [[ "$content" =~ ^[[:space:]]*[A-Z][A-Za-z0-9]*[[:space:]]+(string|\*string)[[:space:]] ]]; then
      unguarded+="$line"$'\n'
    fi
  done <<<"$tag_candidates"

  if [[ -n "$unguarded" ]]; then
    echo "delivery-secret-redaction: FALSE (ISC-6) — exported struct field(s) on an assessor surface carry a credential-shaped JSON tag typed as a plain string (not notify.Secret), which json.Marshal would render in plaintext to an API response or CLI print:" >&2
    printf '%s' "$unguarded" >&2
    exit 1
  fi
fi

echo "delivery-secret-redaction: ok — assessor_destinations' credential column(s) are named as protected-at-rest ($dest_cred_cols), assessor_deliveries carries no credential-shaped column, and no unguarded plaintext-typed credential field was found on an assessor surface"
exit 0
