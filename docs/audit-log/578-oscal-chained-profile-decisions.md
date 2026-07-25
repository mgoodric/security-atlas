# Slice 578 — OSCAL chained profile-over-profile resolution: JUDGMENT decisions log

**Slice:** 578 — chained OSCAL profile resolution (profile -> profile -> ... -> catalog)
**Type:** JUDGMENT (chain-depth bound + cycle-detection policy are subjective calls)
**Parent:** 511 (OSCAL profile import — the single-level resolver this extends)
**Author:** Claude (Engineer)
**Date:** 2026-06-07

This log records the subjective build-time calls per the JUDGMENT-slice
convention. The product-runtime AI-assist boundary is untouched: the bridge
imports no LLM; chained resolution is deterministic compliance-trestle logic
plus a pure-Go graph validator.

---

## Detection-tier classification (slice 353 / Q-13)

- `detection_tier_actual`: `none` — no latent bug surfaced during the slice.
  The chained resolve + cycle/depth rejection were proven against real
  compliance-trestle and a real Postgres before this log was written (the
  integration suite ran green end-to-end with the live bridge — see Verify).
- `detection_tier_target`: `unit` + `integration`. The load-bearing safety
  properties (cycle detection, depth bound, no-external-deref at every chain
  link) are covered at BOTH tiers: pure-Go table tests in
  `chain_test.go` prove them WITHOUT the bridge (fast, deterministic), and the
  bridge-gated integration tests prove the end-to-end resolve + the cyclic
  rejection through trestle. The pure-Go tier is the primary safety net so the
  properties hold even if the bridge is unavailable in CI.

---

## D1 — The chain validator is Go-side and pure; resolution stays delegated to trestle

Slice 511 D1 delegated the full import/merge/modify resolution semantics to
compliance-trestle's `ProfileResolver` and kept the sandbox + no-deref guard on
our side. 578 keeps that split and adds ONE Go-side responsibility: the
**import-graph validation** (cycle detection, depth bound, no-external-deref,
termination-at-catalog). This lives in `internal/oscal/profileimport/chain.go`
as a pure function over the supplied documents — no bridge, no Postgres.

Rationale: the brief requires the cycle-detection + depth-bound logic to be
testable Go-side without the bridge, and these are pure graph properties that do
not need trestle to evaluate. trestle still performs the actual chained
resolution: once the graph is proven safe, the bridge lays every supplied
profile + catalog into its sandbox, rewrites every `import.href` to a
`trestle://` path, and `ProfileResolver.get_resolved_profile_catalog` descends
the chain natively (a profile-over-profile import is a first-class trestle
shape). We did NOT fork a parallel resolver — trestle remains the only thing
that merges/modifies.

**What is delegated to compliance-trestle (Python bridge):** the recursive
descent of the validated chain (profile importing profile importing catalog),
plus all import/merge/modify semantics and OSCAL v1.1.x validation.

**What is done Go-side (pure, bridge-free):** external-href reject, cycle
detection, depth bound, unresolvable-href reject, termination-at-catalog, and
the resolved-chain provenance.

The bridge ALSO runs a cycle + depth check (`_check_chain`) as defense-in-depth
in case it is ever driven outside the Go pipeline; the two implementations share
the same constant (`MAX_CHAIN_DEPTH == MaxChainDepth == 8`) and the same
external-href prefix list.

## D2 — Chain-depth bound: N = 8 (matches the spec's proposed default)

The maximum import-chain depth is **8 profile links**. Depth counts the PROFILE
links from the entry profile down to (and including) the profile whose import
names a catalog: a profile that imports a catalog directly is depth 1; A -> B ->
catalog is depth 2.

Justification:

- Real-world OSCAL tailoring chains are shallow. The FedRAMP Low/Moderate/High
  baselines are profile-over-catalog (depth 1). An agency overlay tailoring a
  baseline is depth 2. A layered org -> business-unit -> system tailoring is
  depth 3. A handful of levels covers every legitimate shape.
- 8 is far above any depth a non-malicious author would plausibly author, so
  the bound bites only on a pathological or adversarial chain — its purpose is
  resource-exhaustion defense (threat-model D), not a feature limit.
- 8 is conservative and trivially liftable by one constant if a real-world
  deeper chain ever surfaces (the constant lives in exactly two places, both
  named).

The spec proposed 8 as the default; we adopt it.

## D3 — Cycle detection: DFS path-set, reject on revisit

A profile that imports itself directly (A -> A) or transitively (A -> B -> A,
A -> B -> C -> A) is rejected with a structured error — never an infinite loop
or fetch (P0-578-2). The validator does a depth-first walk of the import graph
tracking the set of profile keys on the CURRENT path; revisiting a key already
on the path is a cycle. The path-set is popped on exit, so a legal DIAMOND (A
imports B and C, both importing the same catalog) is NOT a false cycle — proven
by `TestChain_Diamond_NotACycle`.

Document identity for cycle tracking + href matching is the same precedence the
slice-511 bridge already used: OSCAL `uuid` first, then the metadata-title slug,
then a supplied-ordinal fallback, deduplicated so two same-titled documents
still get distinct keys.

## D4 — Wire shape: extend the existing ImportProfile RPC, no new RPC

Per the orchestrator's preference (and AC-1's "a single supplied_documents list
discriminated by top-level key" leeway), 578 adds a `repeated SuppliedProfile
profiles = 4` field to the EXISTING `ImportProfileRequest` rather than adding a
new RPC. The entry profile still travels in `profile_json`; intermediate
profiles travel in the new `profiles` field; catalogs stay in `catalogs`. This
is purely additive and backward-compatible — a slice-511 single-level caller
sends an empty `profiles` and gets identical behavior.

Stubs regenerated with the pinned toolchain:
`buf generate` for Go bindings, and
`uv run --with 'grpcio-tools==1.80.0' bash oscal-bridge/scripts/gen_proto.sh`
for the Python stubs — verified `GRPC_GENERATED_VERSION = '1.80.0'` in
`oscal_pb2_grpc.py` (a newer grpcio-tools breaks the CI bridge job — slice 512).

## D5 — No external dereference carries forward unchanged, at EVERY chain link

P0-511-1 becomes P0-578-1: the no-external-deref guard now applies to every
import.href at every link of the chain. The Go validator rejects any external
scheme (`https`, `http`, `sftp`, `ftp`, `file:`, `//`) before matching, and the
bridge rewrites every link's href to a `trestle://` sandbox path before
resolution. A deep external href (B importing `https://...`) is rejected with no
fetch — proven both Go-side (`TestChain_ExternalHref_DeepLink`) and in the
bridge (`test_import_profile_chained_external_href_deep_is_rejected_without_fetch`,
which monkeypatches `socket.connect` to assert no network call).

## D6 — Persistence + provenance: reuse 511's shape, record the resolved chain

The resolved baseline still lands via 511's exact persistence shape
(`imported_catalogs.kind = 'profile'`, source `oscal-profile-import`,
requirement -> SCF anchor reconciliation, transactional all-or-nothing). No new
table, no new migration — chained resolution produces the same flat resolved
control set as single-level resolution; only the inputs differ.

The resolved-chain PROVENANCE is recorded in the success-audit-row `detail`
JSON (slice 578 — "record the resolved chain, their hashes"): a `chain` array
of `{role, sha256, bytes}` entries for the entry profile + every intermediate
profile + every catalog, plus a `chain_depth` count. This is provenance over the
supplied material so an auditor can later prove the exact chain documents that
resolved the baseline. The single-level (511) case records a two-element chain
(`entry-profile`, `catalog`), so 511 imports keep a richer-but-compatible audit
detail.

## D7 — Supplied-profile cap: 32 (mirrors the catalog working-set bound)

`MaxSuppliedProfiles = 32` caps intermediate profiles per resolution
(defense-in-depth with the depth bound; mirrors `MaxSuppliedCatalogs = 16`'s
working-set rationale — threat-model D). The entry profile is counted
separately (it travels in `ProfileJSON`).

## Open follow-ons (spillover)

- Resolved-baseline -> FrameworkScope activation remains the slice-511 D5
  follow-on (a resolved baseline lands as rows only; activation is a separate
  operator action). Not touched here.
- See `docs/issues/599-*.md` (filed by this slice — chain-provenance read
  surface).
