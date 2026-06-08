# Slice 511 — OSCAL profile import: JUDGMENT decisions log

**Slice:** 511 — OSCAL profile import (resolve import / merge / modify directives)
**Type:** JUDGMENT
**Parent:** 492 (OSCAL catalog import)
**Author:** Claude (Engineer)
**Date:** 2026-06-07

This log records the subjective build-time calls per the JUDGMENT-slice
convention. The product-runtime AI-assist boundary is untouched (the bridge
imports no LLM; profile resolution is deterministic compliance-trestle logic).

---

## Detection-tier classification (slice 353)

- `detection_tier_actual`: `none` — no latent bug surfaced during the slice;
  the resolution + no-dereference behavior was proven against real
  compliance-trestle 4.0.2 before the Go code was written (probe scripts).
- `detection_tier_target`: `integration` + `unit` — the no-external-deref
  guard is the load-bearing assertion and is covered at BOTH tiers: a pure
  Python unit test in the bridge proves an `https://` / `sftp://` / bare-path
  `import.href` is rejected WITHOUT a fetch, and a Go integration test proves
  the end-to-end resolve against a supplied local catalog.

---

## D1 — Resolution is delegated to compliance-trestle; the sandbox is ours

compliance-trestle 4.0.2 ships `ProfileResolver.get_resolved_profile_catalog`,
which resolves `import` (control selection), `merge` (combine/dedup), and
`modify` (set-parameters + alters) into a flat resolved catalog. We **delegate
the full resolution semantics to trestle** rather than re-author them in Go —
trestle is the OSCAL reference SDK and re-implementing merge/modify would be
exactly the "fork a parallel path" anti-pattern the brief warns against.

**What is delegated to compliance-trestle (Python bridge):**

- `import.include-controls` / `exclude-controls` selection.
- `merge` (combine + dedup of controls drawn from multiple imports).
- `modify.set-parameters` (parameter value assignment).
- `modify.alters` (add/remove parts on selected controls).
- OSCAL v1.1.x structural validation of the profile AND the resolved output.

**What is done in Go:**

- Role gate, tenant scoping, byte cap (defense-in-depth, before the wire).
- Source SHA-256 provenance.
- requirement → SCF-anchor reconciliation (D3).
- Transactional persistence + append-only audit (mirrors 492).

## D2 — No external dereference: a sandboxed trestle workspace (PRIMARY threat)

`ProfileResolver` is **filesystem-based**: it takes a `trestle_root` workspace
dir + a profile path and resolves each `import.href` through trestle's
`FetcherFactory`. That factory has `HTTPSFetcher` and `SFTPFetcher` — it
**will** dereference an `https://` / `sftp://` href, and a bare relative path
(`foo.json`) is treated as a `LocalFetcher` read of an arbitrary host file.
This is the load-bearing threat (P0-511-1 / threat-model I).

**Mitigation (the core security design):** the bridge NEVER hands trestle a
profile with an unsanitized href. The flow:

1. Parse the profile JSON in the bridge (no trestle yet).
2. Build an **ephemeral, isolated** trestle workspace in a temp dir
   (`.trestle/` marker + `catalogs/<key>/catalog.json` per supplied catalog +
   `profiles/imported/profile.json`).
3. For each `import.href`, REWRITE it to a `trestle://catalogs/<key>/catalog.json`
   in-sandbox path, where `<key>` is the supplied catalog the href maps to.
   The href→catalog match is by an **identity key** derived from the supplied
   catalogs (their declared OSCAL `metadata.title` / catalog `uuid`, and the
   operator-supplied ordinal), NOT by fetching the href.
4. Any `import.href` that does NOT map to a supplied catalog is a **structured
   error** — the resolve never runs, nothing is fetched (P0-511-1).
5. Resolve against the sandbox. Because every href is now `trestle://`, trestle
   only ever uses `LocalFetcher` pointed inside our temp dir. No `HTTPSFetcher`
   / `SFTPFetcher` is ever constructed.

The bridge unit test asserts (a) an `https://` href is rejected with no network
call, (b) a bare-path href is rejected, (c) a `trestle://` / mapped href
resolves. The temp workspace is removed in a `finally`.

**Href→catalog matching rule:** when a single catalog is supplied and a single
import is present, they match positionally (the FedRAMP-baseline common case:
one profile over one catalog). When multiple catalogs are supplied, the match
is by the href's trailing path segment / fragment against each supplied
catalog's declared title-slug and uuid; an ambiguous or unmatched href errors.
This is intentionally conservative — a non-match is a clean error, never a
guess that triggers a fetch.

## D3 — SCF reconciliation reuses 492's deterministic anchor match

A resolved profile's controls reconcile to the SCF spine **exactly as 492's
imported catalog controls do**: a resolved control whose OSCAL `control-id`
matches a `scf_anchors.scf_id` in the current SCF version maps TO that anchor
(`scf_anchor_id` set); otherwise NULL = "needs operator mapping". This honors
invariant #7 (requirement → SCF anchor, never requirement → requirement) and
reuses the `loadSCFAnchorIDs` query. No new crosswalk logic is invented — a
resolved baseline is, structurally, an imported control set with a profile
provenance label.

## D4 — Persistence: reuse 492's tables, add a `kind` discriminator + profile provenance

Rather than fork a parallel `imported_profiles` table tree, the resolved
baseline persists into the **same** `imported_catalogs` / `imported_catalog_controls`
/ `imported_catalog_audit_log` tables 492 established, with:

- A new `imported_catalogs.kind` column (`'catalog'` default | `'profile'`) so a
  resolved profile baseline is queryable as a distinct set without a second
  table tree (the brief's "reuse 492's persistence shape" + "a sibling baseline
  table" — chosen the column over a sibling table for query-uniformity: the
  read models, RLS policies, and audit trail are shared, and a resolved profile
  IS a catalog projection).
- A new `imported_catalogs.profile_title` column carrying the profile's declared
  title (provenance / display), empty for a catalog import.
- The `source` CHECK extends to allow `'oscal-profile-import'`.
- The audit `action` CHECK extends to allow `'profile_imported'` +
  `'profile_import_rejected'`.

The migration is purely additive (ALTER ADD COLUMN with safe defaults +
CHECK-constraint replacement). It sorts after the latest existing migration.

## D5 — Caps: byte + control + import-chain depth + resolution timeout (AC-3)

- Byte cap: reuse `MaxCatalogBytes` (16 MiB) for the profile + a per-catalog
  byte cap (same 16 MiB) on each supplied catalog (defense-in-depth, Go-side
  before the wire + bridge-side).
- Resolved-control-count cap: 10,000 (reuse `MAX_CATALOG_CONTROLS`).
- Import-chain depth cap: profiles importing profiles are bounded; v1 supports
  a single import level (profile → catalog). A profile whose import references
  another _profile_ (chained) is rejected with a structured error in v1 (the
  FedRAMP baselines are profile-over-catalog; chained profile-over-profile is a
  documented follow-on). This caps chain depth at 1 by construction — the
  simplest correct bound (threat-model D).
- Resolution timeout: the bridge RPC inherits the Go-side 30s bridge timeout;
  the bridge additionally guards expansion via the control-count cap.

## D6 — CLI shape mirrors `import-catalog`

`atlas-oscal import-profile <profile-file> --catalog <file>... [flags]` with the
same dsn / tenant-id / bridge-addr / source-label / imported-by / role / json
flags as `import-catalog`. `--catalog` is repeatable (multi-catalog imports).

## Open follow-ons (spillover)

- Chained profile-over-profile resolution (deferred per D5).
- Resolved-baseline → FrameworkScope activation (a resolved baseline lands as
  rows only; activation is a separate operator action — threat-model E).
