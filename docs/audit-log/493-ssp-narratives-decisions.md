# 493 — SSP control-implementation narratives — decisions log

Slice 493 is `Type: JUDGMENT`. "What text faithfully represents a control's
implementation in an SSP" is an OSCAL-conformance judgment, and the exact
wording of the description-less fallback is operator-facing copy an auditor
reads. This log records the build-time judgment calls so the maintainer can
re-evaluate them once a real auditor exercises the export.

- detection_tier_actual: integration
- detection_tier_target: integration

(One bug surfaced during the slice: the description-bearing seed used an
invalid `control_implementation_type` enum value `'manual'`; the controls enum
admits `automated | semi_automated | manual_attested | manual_periodic`. Caught
by the integration test on first run against real Postgres — the right tier.
Corrected to `'manual_attested'`. No fix-forward; caught pre-merge.)

## Decisions made

### D-query — companion query, not a widened shared projection

**Options considered:**

- (A) Extend the existing `ListActiveControls` projection to add `description`.
  One query, fewer lines.
- (B) Add a purpose-built companion query `ListActiveControlsWithDescription`
  with the description-bearing column set; leave `ListActiveControls` untouched.

**Chosen: (B).** The project convention is unambiguous on this point — slice 137
(`ListActiveControlsForExport`, D2) and slice 175 (`ListControlsHistoryForExport`,
D2) both rejected widening a shared projection in favor of a dedicated export
query, precisely so a non-export caller of the shared row is never reshaped to
carry export-only columns. `ListActiveControls` has other consumers; widening it
would change their generated row type for no benefit. The companion query is the
pattern-matched choice. Cost: ~25 SQL lines + the sqlc-regenerated row. Benefit:
the shared projection stays stable; the SSP exporter gets exactly the columns it
needs.

**Revisit once in use:** if a future slice needs the description on several
read paths, consider whether a single description-bearing projection should
become the default — but that is a deliberate convention change, not this slice.

**Confidence: high.** Directly pattern-matched to two prior export-query slices.

### D-fallback — wording of the description-less synthesized statement

**The call:** P0-493-1 forbids an empty statement; AC-3 requires the fallback be
clearly labeled as auto-generated so an auditor is not misled into thinking the
boilerplate is the operator's authored narrative. The chosen text:

```
[Auto-generated summary — no authored implementation narrative on file.] Control
"<Title>" (family: <family>); implementation owned by role "<owner_role>".
Provide an authored control-implementation narrative to replace this placeholder.
```

**Rationale:**

- The honesty marker is **front-loaded** — an auditor skims the first words of
  each implementation statement, so the "[Auto-generated summary …]" label must
  lead, not trail. A trailing disclaimer is easy to miss.
- It states the absence plainly ("no authored implementation narrative on file")
  rather than dressing the boilerplate up as content. This is the anti-pattern
  the slice exists to kill — cookie-cutter statements that _look_ like a
  narrative but say nothing.
- It still carries the title + family + owner so the auditor can identify the
  control and knows who to ask for the real narrative.
- It closes with an explicit call to action ("Provide an authored … narrative to
  replace this placeholder") so the operator reading their own SSP knows the
  remediation.
- No LLM is involved — this is a deterministic `text/template`-style format, so
  it honors the constitutional AI-assist boundary (the SSP statement is never
  AI-generated).

**Revisit once in use:** if a real auditor finds the label too prominent
(distracting) or not prominent enough, tune the wording. Consider whether a
description-less control should instead be _omitted_ from the SSP's
control-implementation list entirely (vs. a labeled placeholder) once there is
operator feedback on which an auditor prefers; the current choice (labeled
placeholder, never omit) errs toward completeness because a missing control in an
SSP reads worse to an auditor than a labeled gap.

**Confidence: high** on "never empty + clearly labeled" (P0-493-1 / AC-3
satisfied unambiguously); **medium** on the exact wording, which is genuine
operator-facing copy worth tuning against real auditor reaction.

### D-test — load-bearing assertions run WITHOUT the Python bridge

**The call:** AC-6 / AC-7 / AC-8 assert OSCAL-JSON content. The real serializer
is the Python compliance-trestle bridge, which is absent on the CI integration
shard (and locally), so a bridge-gated test would _skip_ there — leaving the
load-bearing tenant-isolation guarantee (P0-493-2) unproven on every normal run.

**Chosen:** a capturing fake `BridgeClient` (`captureBridge`) that serializes the
SSP proto input with `protojson` and records the bytes. The content + isolation
assertions run through the **real DB read path** (the only place a cross-tenant
leak could originate) with the fake bridge, so they execute on every CI run. A
separate `TestSSP_RealBridgeRoundTrip` exercises the real bridge for wire
fidelity and skips when the bridge is unavailable (decision D2 from slice 030;
the bridge-skip precedent — slice 492 D8 / slice 030 D2).

**Rationale:** the leak surface is the tenant-scoped SQL read, not the trestle
serialization. Proving isolation on the read path is what matters for P0-493-2,
and it must not be gated on an optional toolchain. The real-bridge test still
exists to catch a serializer that drops the field.

**Revisit once in use:** when the CI integration shard gains the Python bridge,
`TestSSP_RealBridgeRoundTrip` will stop skipping and add wire-fidelity coverage
on every run — no code change needed.

**Confidence: high.** Mirrors the established bridge-skip precedent and moves the
load-bearing guarantee from "skipped in CI" to "runs in CI."

## Revisit once in use (consolidated)

1. **Fallback wording (D-fallback)** — tune against real auditor reaction;
   decide labeled-placeholder vs. omit-from-list for description-less controls.
2. **Per-statement decomposition** — v1 ships a single implementation statement
   per control (no OSCAL `by-component` structure); a follow-on can decompose.
3. **Authored-description provenance** — the description is the slice-009 bundle
   text; if a distinct "implementation narrative" field is ever added to
   `controls` (slice 030 D-narrative revisit), re-point `controlStatement`.

## Confidence summary

| Decision                                   | Confidence |
| ------------------------------------------ | ---------- |
| D-query (companion query)                  | high       |
| D-fallback (never empty + clearly labeled) | high       |
| D-fallback (exact wording)                 | medium     |
| D-test (bridge-independent load-bearing)   | high       |

No constitutional-invariant conflicts surfaced. Invariants #6 (RLS tenant
isolation), #8 (OSCAL wire format), and #9 (manual evidence first-class — a
manual/minimal control still produces a complete labeled statement) are honored
and, for #6, proven by the tenant-isolation integration test.
