#!/usr/bin/env bash
#
# delivery-secret-redaction.sh -- falsifier for isa/assessor-delivery.md
# ISC-6: "No destination credential is readable back." Falsifier: a plaintext
# credential in an API response, CLI output, log line, error string or ledger
# row.
#
# The check covers the ledger-row half, which is the half that exists before
# any delivery code does:
#   1. No column of `assessor_destinations` or `assessor_deliveries` is shaped
#      to hold a credential in plaintext. On the destination a credential
#      column is allowed only as a reference or ciphertext (`*_ref`,
#      `*_ciphertext`, the shape internal/llm/cloud uses for
#      tenant_llm_routing.api_key_ciphertext). The delivery ledger carries no
#      credential-named column at all: it records what left, and has no reason
#      to hold destination credentials in any form.
#   2. Every `credential_ref` value is a reference (`scheme://...`), not a
#      pasted secret.
#
# What this does not reach: API responses, CLI output, log lines and error
# strings. Those are produced by delivery code that does not exist. When it
# lands this probe needs a behavioral half that registers a destination with a
# known sentinel credential, drives a delivery that fails, and searches every
# response, the CLI output and the captured logs for the sentinel. The epic
# commits that path to internal/notify's redacting Secret and ScrubSecret.
#
# Adopted from the engine draft (#1630). Its source-tree grep needed ripgrep,
# which CI does not install, and matched on file names containing "assessor";
# that half is dropped in favour of the behavioral half above.
#
# Today: no `assessor_destinations` table, so there is no stored credential
# and no redaction contract for one. FALSE.
#
# Exit 0  no plaintext-shaped credential column or value in either table.
# Exit 1  there is one, or the tables do not exist.
# Exit 3  cannot run (see isa/probes/_delivery-lib.sh).
set -uo pipefail

name=delivery-secret-redaction
claim=ISC-6
# shellcheck source=isa/probes/_delivery-lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/_delivery-lib.sh"

# to_regclass resolves through search_path, so the same query reads the
# self-test's temporary tables and, outside it, the real ones.
CHECK="
SELECT c.rel, a.attname, 'credential-named column ' || format_type(a.atttypid, a.atttypmod)
FROM (VALUES ('assessor_destinations'), ('assessor_deliveries')) c(rel)
JOIN pg_attribute a
  ON a.attrelid = to_regclass(c.rel) AND a.attnum > 0 AND NOT a.attisdropped
WHERE a.attname ~* '(credential|secret|token|api_?key|passw|bearer|passphrase|private_key)'
  AND (c.rel = 'assessor_deliveries' OR a.attname !~* '(_ref|_ciphertext)\$')
UNION ALL
SELECT 'assessor_destinations', 'credential_ref of ' || id::text, 'value is not a scheme:// reference'
FROM assessor_destinations
WHERE credential_ref IS NOT NULL AND credential_ref !~ '^[a-z][a-z0-9+.-]*://'
ORDER BY 1, 2;"

# A destination that keeps the vendor token itself, in plaintext.
VIOLATION="
ALTER TABLE assessor_destinations ADD COLUMN api_token text;"

dl_preflight "$name"
dl_selftest "$name" "$CHECK" "$VIOLATION"
dl_require_tables "$name" "$claim" \
  "There is nowhere a destination credential is stored, so there is no redaction contract for one: nothing yet holds it as a reference or ciphertext, or keeps it out of the delivery ledger." \
  assessor_destinations assessor_deliveries
dl_check "$name" "$claim" "$CHECK" "table	column_or_row	problem"

echo "$name: ok -- no plaintext-shaped credential column or value in assessor_destinations or assessor_deliveries (ledger rows only; see header)"
exit 0
