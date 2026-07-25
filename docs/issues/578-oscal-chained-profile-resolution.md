# 578 — OSCAL chained profile-over-profile resolution

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** M (1-2d)
**Type:** JUDGMENT (chain-depth bound + cycle detection are subjective calls)
**Status:** `blocked` (depends on #511 — OSCAL profile import)
**Parent:** #511 (OSCAL profile import) — filed as a documented deferral
(slice-511 decisions-log D5).

## Narrative

Slice #511 resolves an OSCAL profile against a SUPPLIED **catalog**, bounding
the import chain to a single level (profile → catalog) by construction: a
supplied document that is itself a profile fails #511's catalog parse, and an
`import.href` is only ever matched against supplied catalogs. This is the
correct conservative v1 bound — the FedRAMP Low/Moderate/High baselines are
all profile-over-catalog, the dominant real-world shape.

OSCAL profiles can, however, import OTHER profiles (a profile-over-profile
chain). This slice lifts the single-level bound to support a bounded chain:
a profile that imports a profile that ultimately resolves to a catalog.

## Scope

- Accept supplied **profiles** (not only catalogs) as resolution inputs.
- Resolve a bounded chain: profile → profile → … → catalog.
- Enforce a **max import-chain depth** (proposed default 8) and **cycle
  detection** (a profile that imports itself, directly or transitively, is a
  structured error — never an infinite loop / fetch).
- Preserve the #511 no-external-dereference invariant unchanged: every href
  in every link of the chain is rewritten to a sandboxed `trestle://` path
  pointing at a supplied document, and an href that maps to no supplied
  document is a structured error with no fetch (P0-511-1 carries forward).
- Reuse #511's persistence + provenance shape (the resolved baseline still
  lands as `kind = 'profile'`).

## Acceptance criteria (outline)

- **AC-1.** `ImportProfileRequest` accepts supplied profiles alongside
  catalogs (or a single `supplied_documents` list discriminated by top-level
  key).
- **AC-2.** A profile-over-profile chain resolves end-to-end against supplied
  documents only — no external dereference.
- **AC-3.** Max chain depth + cycle detection enforced; a too-deep or cyclic
  chain is a structured error, nothing persisted.
- **AC-4.** Integration test: a two-level chain resolves; a cyclic chain
  errors WITHOUT fetching or looping.

## Anti-criteria (P0)

- **P0-578-1.** Does NOT dereference any external `href` at any chain link
  (carries forward P0-511-1).
- **P0-578-2.** Does NOT loop / hang on a cyclic chain.
- **P0-578-3.** Does NOT persist partially on a resolution failure.

## Dependencies

- **#511** (OSCAL profile import) — provides the single-level resolver, the
  sandbox, the no-deref guard, and the persistence shape this slice extends.

## Skill mix

`tdd` (cycle + depth tests are load-bearing) · `security-review` (the chain
multiplies the untrusted-reference surface) · `simplify`.
