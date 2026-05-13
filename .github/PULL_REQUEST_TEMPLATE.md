<!--
Thanks for the contribution! Fill in every section. Empty sections will delay review.

If this PR is AI-assisted, include a `Co-authored-by:` trailer naming the assistant.
-->

## Summary

<!-- 1–3 sentences: what does this PR do, and why? -->

## Slice / issue

<!-- Link the slice file under docs/issues/ and the corresponding GitHub issue (if any). -->

- Slice: `docs/issues/NNN-...md`
- Issue: #

## Changes

<!-- Bulleted list of the substantive changes. -->

-

## Acceptance criteria

<!-- Copy the AC list from the slice file. Tick what's done. -->

- [ ] AC-1:
- [ ] AC-2:

## Constitutional invariants check

<!-- See CLAUDE.md → "Architecture invariants". Tick what applies. -->

- [ ] No per-framework duplicated controls (invariant #1)
- [ ] Ingestion and evaluation stages remain separated (invariant #2)
- [ ] Evidence SDK contract unchanged OR additive-only (invariant #3)
- [ ] Tenant-scoped tables retain RLS (invariant #6)
- [ ] No CCM / CAIQ / SIG / OpenGRC content bundled (licensing constraint)
- [ ] AI-assist boundary in CLAUDE.md not broadened

## Tests

<!-- What did you add / change? Unit, integration, e2e? -->

-

## Risk + rollout

<!-- What could go wrong? Anything irreversible? Migration round-trip verified? -->

-

## Documentation

- [ ] `CHANGELOG.md` updated under `## [Unreleased]`
- [ ] `CONTEXT.md` updated if domain vocabulary changed
- [ ] `Plans/` updated if architecture changed
- [ ] Slice status flipped in `docs/issues/_STATUS.md` (when opening as `in-review`)

## DCO

- [ ] Every commit carries a `Signed-off-by:` trailer
