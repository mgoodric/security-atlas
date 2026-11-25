# Issue #3064 — Stock policy seed falls back to the current release by code and title

Follow-up to #3061 D5. On a bring-your-own SCF release install the stock
policies declare `test-2026.3` (#3033), the bootstrap loads it as legacy and
moves the stock controls onto the operator's release (#3061). The #1772
resolver linked a code only to a control bound in the declared release, so
`seed-stock` linked 0 of 15 codes. Matt chose the rule on 2026-11-24: fall
back by code and title.

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

The gap was found and pinned by `TestBYOBootstrap_FreshInstallBindsStockContentOnTheOperatorRelease`
(#3063), which this change flips from asserting 0 links to asserting 11.

## D1 — A scoped exception to #1774, for the stock policy seed only

#1774 says a write binds an SCF code only in the release it names, and never
falls back to the current one. This change adds one exception, and keeps it
narrow:

- **Only `SQLAnchorResolver`, the stock policy seed.** No other write path
  changes. `scfrelease.Lookup` and `scfrelease.Resolve` are unchanged, and
  the seed still refuses a missing or unloaded release (#1774).
- **Only when the declared release is loaded but not current.** A seed that
  declares the current release behaves as before (`TestSeed_DeclaredCurrentHasNoFallback`,
  and `TestBYOBootstrap_DefaultInstallIsUnchanged` asserts no fallback note
  on a default install).
- **Only on code AND title.** The SCF reuses codes between releases for a
  different control (#1770's reason for release-aware anchors). A matching
  title is the evidence that the code still names the same control, which
  is the same test the bootstrap's stock re-bind uses (#3061 D10). A code
  whose title differs, or that the current release lacks, stays unlinked.
- **Read-only.** The fallback reads anchors and controls. It writes nothing
  to the catalog and binds no control to a new anchor. The only write is the
  policy row the seed already inserts, whose `linked_control_ids` now
  include the fallback's controls.
- **Visible.** Every code the fallback links or refuses gets a line in
  `seed.Report.ReleaseFallbacks`, and `atlas-cli policy seed-stock` prints
  each as `warning: <policy>: <note>`. A linked note names the current
  release used. A refused note names the code and both titles.

The policy row stores no SCF release, so nothing records which release a
link came through except the seed's output. That is acceptable for a seed
of community drafts, which an operator reviews before approving.

## D2 — The declared release still wins

The fallback runs only for codes the declared release leaves unlinked: the
code has an anchor there, but no control is bound to it. A code with a
control bound in the declared release links that control, with no note,
even when the current release has a control for the same code
(`TestSeed_DeclaredLegacyKeepsItsOwnBinding`). This keeps #1772's rule
whole wherever it can still be met.

On a BYO install it matters for a stock control the bootstrap left on the
sample's anchor because its title differs in the operator's release (#3061
D10, `TestBYOBootstrap_DifferingAnchorsAreLeftForReview`). The policy links
that control in `test-2026.3`, as the policy declares. The title check then
applies only to a code whose control is already on the operator's anchor,
which on a BYO install means an operator re-anchored it.

## D3 — A code the declared release lacks gets no fallback

With no declared anchor there is no title to compare, so the code stays in
`MissingAnchors` with no fallback note, as before. The four codes the stock
policies link with no stock control bundle (`CFG-04`, `IRO-02`, `IRO-14`,
`TPM-14`) are in the declared release, match by title, and have no control
in either release. They stay missing with no note, on both a default and a
BYO install.

## D4 — The resolver interface gains a return value

`AnchorResolver.Resolve` returns the fallback notes as a third slice beside
`resolved` and `missing`. Its only implementations are `SQLAnchorResolver`
and `NoopAnchorResolver`, both in this package. A separate interface for the
notes would have hidden them from a resolver that falls back without
reporting it.

`Report.SCFReleaseWarnings` was removed by #1774. The new field is named
`ReleaseFallbacks` because it carries only fallback notes, not the old
"no release declared" warning.
