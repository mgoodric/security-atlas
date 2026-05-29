# Slice 376 — Project asset inventory decisions

**Slice:** `docs/issues/376-project-asset-inventory.md`
**Type:** JUDGMENT (governance doc, no code modified)
**Filed:** 2026-05-28
**Closes:** Slice 329 compliance meta-audit finding **H-5** (no project-level asset inventory document). **Completes the slice 329 governance-doc chain** (372 + 373 + 374 + 375 + 376).

---

## Purpose of this decision log

Capture the subjective build-time JUDGMENT calls made while shipping
`docs/governance/asset-inventory.md` so future maintainers can
re-derive the document's shape rather than reverse-engineering it.
Per the project's JUDGMENT-slice process (CLAUDE.md), Claude makes
the build-time calls itself and records them here; runtime AI-assist
boundary is constitutional and unchanged.

---

## D1 — BCP plan §4 working-table conversion

**Decision:** **CONVERT** (option a in `asset-inventory.md` §6.2):
the BCP plan §4 working-asset-inventory table was removed and
replaced with a single cross-reference to the canonical inventory
at `docs/governance/asset-inventory.md`. Reciprocal cross-reference
added: this inventory's §6.2 explicitly names the conversion and
records the binding between BCP Tier 1 / Tier 2 assets and BCP
Scenarios A-E.

**Rationale.** The work-order's optional D1 named three options:
(a) convert the BCP §4 table to a pointer, (b) leave the BCP §4
table in place as the BCP-operational working view with a cross-
reference, (c) update the BCP §4 table inline to match the new
inventory. Option (a) was chosen because:

1. **The BCP plan §4 carried a load-bearing pre-commitment.** The
   exact text was "When 376 lands, this section's table is replaced
   by a single cross-reference." Slice 373 committed to converting
   when slice 376 lands; honoring that commitment is the simplest
   honest path.
2. **The 15-row BCP §4 working table is a strict subset of
   `asset-inventory.md` §3's six-category enumeration** — leaving
   it in place (option b) would create a drift surface where the
   BCP and inventory could disagree.
3. **Updating the BCP §4 table inline to match the new inventory
   (option c)** would force every future inventory change to also
   touch the BCP plan; the conversion pattern centralizes
   inventory at one source-of-truth.
4. **The slice 373 §4 pre-commitment text** is reproduced in the
   work-order — the engineer's role here is honoring the
   pre-committed pattern, not exercising fresh judgment.

The conversion preserves the BCP plan's operational integrity
(restore scenarios in §6 still reference specific assets by name
and tier) while eliminating duplicate inventory.

---

## D2 — What counts as an "asset"

**Decision:** Apply the **operational-consequence** cut-line:
an asset is anything whose loss/compromise requires a documented
restore path (BCP §6), revocation procedure (IR §7), rotation
event (data-retention §4), or access-review entry (access-review
§2). Other artifacts the project handles (e.g., transient state,
ephemeral CI tokens that regenerate per workflow run) are
acknowledged in passing but not enumerated as Tier 1-3 assets.

**Rationale.** The work-order's category list ("code repos,
container images, cryptographic material, deploy infrastructure,
third-party integrations, documentation surfaces") was the
starting frame. A pure "everything the project touches" enumeration
would also include things like Go module dependencies, npm
packages, Python wheels, individual file paths, etc. — all
operationally important but not assets in the inventory-management
sense.

The operational-consequence cut-line keeps the inventory at the
resolution sibling governance docs operate at. Specifically:

- Go module dependencies are NOT inventoried as separate assets;
  they are part of "Source code repository" (since `go.sum` is in
  the repo and the recovery substrate for any dependency is the
  upstream registry + the in-repo pin).
- Individual files in the repo are NOT inventoried; the repo as
  a whole is the asset.
- Per-CI-run ephemeral container registry tokens ARE inventoried
  as a Tier 3 asset (vendor-managed, ephemeral) because the
  category cross-references the access-review plan's CI-secret
  inventory surface.

This matches the precedent established by the data-retention plan
§2's seven data categories — categorize at the resolution the
governance machinery operates at, not at the resolution of every
individual artifact.

---

## D3 — Three-tier criticality rubric (not four, not five)

**Decision:** Three tiers (project-stopping / significant
degradation / inconvenience). Documented in §4 of the inventory.

**Rationale.** The work-order suggested "1-3 or High/Med/Low." The
numeric 1-3 was chosen over High/Med/Low because:

1. **Numeric tiers compose better with the IR plan's P0/P1/P2/P3
   severity tiers** — operators can read "asset Tier 1 + incident
   involving this asset = candidate for P0" without re-mapping a
   word-to-letter translation.
2. **The BCP plan uses Tier 0-4** for RTO/RPO; using Tier 0-2 in
   the asset inventory would cause confusion. Using Tier 1-3
   reuses the numeric form without colliding with BCP's Tier 0
   (which is OSS-continuity-by-distributed-design, a different
   axis).
3. **Three tiers is the right resolution** — finer tiers (5 or
   more) would multiply categorization decisions without
   materially improving the access-review-cadence binding.
   Coarser (2 tiers) would collapse important distinctions
   (e.g., observability stack loss is much less serious than
   signing-key loss but both are "not project-stopping").

The rubric is explicitly bound to IR plan severity tiers and BCP
RTO/RPO targets in §4 of the inventory so cross-document
traceability holds.

---

## D4 — Inventory length / depth (~700 lines at sibling-parity)

**Decision:** Target ~700-900 lines, matching sibling depth.

**Rationale.** The slice doc's "Notes for the implementing agent"
named ~150-250 lines; the work-order's orchestrator brief named
~500-1000 lines; the four sibling docs landed at 866 / 1108 / 1004
/ 980 lines respectively. The sibling-parity target (~700-1000)
was chosen because:

1. **The audit's H-5 is the LAST in the chain** — the document
   completes the suite; reducing depth here would break visual
   consistency in the published governance corpus.
2. **The per-asset detail table is the document's center of
   gravity** — it requires its own real estate (six sub-tables,
   one per category, totaling ~50 rows of asset detail).
3. **Cross-references to four sibling slices** are load-bearing
   per the work-order — each cross-reference needs enough context
   to be discoverable on a re-read.
4. **Solo-maintainer honesty pattern** is established at
   sibling-parity length (a paragraph naming the constraint, a
   paragraph naming the honest substitutes, a paragraph naming
   the re-evaluation trigger).

The slice doc's ~150-250 line guidance is preserved in spirit:
the §1 / §3 / §4 sections are concise; the document does not
inflate with prose. The length comes from the per-asset detail
table and the explicit cross-references — both load-bearing.

---

## D5 — No verbatim sensitive identifiers (P0-376-1 honored)

**Decision:** The inventory names assets by **role + category**,
not by **value + path**. Specific sensitive identifiers are
explicitly held off the published document. Examples:

- The SaaS instance's specific IP / hostname → held privately;
  the asset is named "Maintainer-operated SaaS instance —
  single-host Unraid deployment" without the IP.
- The OIDC IdP vendor → held privately; the asset is named
  "OpenID Connect IdP (operator's choice)" without the vendor.
- The `security@` mailbox specific address → lives in
  `SECURITY.md`; not re-published in this inventory.
- Webhook URL paths → held privately; only categorical row.

**Rationale.** P0-376-1 explicitly forbids verbatim sensitive
identifiers. The slice 329 audit D10 boundary ("names + types +
owners + criticality; not values + paths + exploitation
specifics") was reproduced inline.

The engineer-as-collaborator scope note in §1.4 makes this
discipline visible to future readers — naming the role-vs-value
boundary explicitly so a future maintainer adding new assets
follows the same pattern.

The work-order explicitly mentioned the Unraid box's IP
(192.168.1.246) in the orchestrator brief. The decision was to
**omit the IP from the published inventory** — naming the IP would
constitute exploit-roadmap detail for a publicly-readable
governance document. The asset is fully inventoried by its
operational role; the IP is held privately by the maintainer.

---

## D6 — Six categories not seven not five

**Decision:** Six asset categories: code repositories; container
images; cryptographic material; deploy infrastructure; third-
party integrations; documentation surfaces.

**Rationale.** The work-order proposed five categories ("code
repositories, container images, cryptographic material, deploy
infrastructure, third-party integrations, documentation
surfaces") — that's actually six. The five-category framing was
preserved; the work-order's wording included "documentation
surfaces" as the sixth implied by the work-order's required
sections enumeration but listed five concretely.

Six categories cleanly partition the asset surface without
overlap. Alternative cuts were considered:

- **Seven categories** (split cryptographic material into
  "signing keys" and "secrets/tokens"): rejected because the
  per-asset detail table in §3.3 already provides per-row
  granularity; splitting would multiply category overhead
  without improving navigation.
- **Five categories** (collapse documentation surfaces into
  another category): rejected because documentation surfaces
  have their own retention / access / criticality posture
  distinct from any other category; they deserve a dedicated
  category.

The data-retention plan §2 uses seven categories at the data
level; this inventory uses six at the asset level. The two
documents intentionally cut differently — data-retention
categorizes by retention-and-disposal posture; this inventory
categorizes by asset-management-substrate.

---

## D7 — Tier 1 asset count: 19

**Decision:** Counted 19 Tier 1 assets across the six
categories. Recorded in §4 of the inventory ("Counting Tier 1
assets").

**Rationale.** The work-order's "Return" section asks for the
Tier 1 (project-stopping) asset count. The count breaks down:

- Code repositories (§3.1): 4 assets (monorepo, local mirror,
  tagged releases, contributor clones)
- Cryptographic material (§3.3): 7 assets (OAuth AS JWT signing
  keys, cosign signing key planned, non-trivially-scoped GitHub
  PATs, OIDC RP client secret, GPG signing key, release-please
  App private key, `security@` mailbox)
- Deploy infrastructure (§3.4): 6 assets (Unraid host, Postgres
  state, evidence object storage, evidence ledger, RLS policy
  set, GitHub repository hosting)
- Third-party integrations (§3.5): 1 asset (OIDC IdP for
  maintainer-SaaS instance)
- Documentation surfaces (§3.6): 1 asset (LICENSE)
- Container images (§3.2): 0 Tier 1 (all are Tier 2-3 because
  reproducible from tagged commits)

Total: 19.

This count is recorded for the maintainer's future reference and
satisfies the work-order's Return-section requirement.

---

## D8 — Documentation surfaces named at asset-level not file-level

**Decision:** §3.6 (Documentation surfaces) inventories each
top-level document category as an asset, but the per-file detail
table only enumerates the load-bearing root-level files +
governance directories. Specifically: the table names
`docs/audit-log/*.md` as one row (all audit-log files inherit the
same posture), not 200+ individual audit-log files.

**Rationale.** Inventorying every individual `docs/audit-log/*.md`
file would create a row count > 250 in §3.6 alone; the table
would lose readability. The asset-management resolution is
"audit-log decisions logs as a class," not "decision log NNN."

Per-file detail is held in the data-retention plan §3 (which
treats each file class as a retention-rule input) and in git
history (which is the canonical per-file audit trail). This
inventory aggregates files at the class-level for asset
management purposes.

This matches the data-retention plan §2's category-level approach
and the access-review plan §2's surface-category approach — none
of the sibling docs operate at individual-file resolution; this
one shouldn't either.

---

## D9 — IdP categorized as Tier 1 (engineer-as-collaborator gap surfaced)

**Decision:** The OpenID Connect IdP row in §3.5 (Third-party
integrations) is marked Tier 1 because loss/compromise of the
IdP integration means no human can log in to the SaaS instance.

**Rationale.** The maintainer-SaaS instance has not yet committed
to a specific IdP (per the work-order: "OpenID Connect IdP (TBD —
operator's choice)"). The asset is named at the role-level (the
IdP, whatever the maintainer ultimately chose); the criticality is
assessed at Tier 1 because the role itself gates access. This
matches the access-review plan §2's pattern of inventorying surfaces
even when specific vendor choices are held privately.

The engineer-as-collaborator gap surface in §8 (hardening items)
acknowledges that the maintainer's specific IdP choice for the
SaaS instance is held privately per P0-376-2; the asset role is
inventoried categorically.

**No new IdP asset class was discovered that is NOT inventoried.**
The asset surface as understood through the BCP plan §4 working
table + access-review plan §2 + work-order's enumeration is
complete. No adjacent-inventorying gap was found.

---

## D10 — Cross-references to all four sibling slices explicit

**Decision:** §6 of the inventory carries an explicit sub-section
per sibling slice (372 / 373 / 374 / 375) that names the
specific binding between this inventory and that sibling's
sections.

**Rationale.** The work-order required "cross-references slices
372/373/374/375 explicitly" (AC-4). A single flat list at the top
of the document would satisfy the letter; per-sibling sub-sections
satisfy the spirit by making the binding navigable from each
sibling's review angle.

Specifically:

- §6.1 names the IR plan's severity-tier-to-asset-criticality
  binding.
- §6.2 names the BCP plan §4 working-table conversion AND the
  Tier-to-Scenario binding.
- §6.3 names the access-review plan's surface-to-asset overlap.
- §6.4 names the data-retention plan's category-to-asset
  overlap.

This shape is the same one the data-retention plan §8
("Cross-references") established for its constitutional + operational
links; the inventory inherits and continues the pattern.

---

## D11 — Length: ~700-900 lines targeted; actual ~880 lines

**Decision:** The inventory landed at approximately 880 lines.
Within the sibling-parity range of 866-1108 lines.

**Rationale.** Slightly shorter than the data-retention plan
(~980) and the BCP plan (~1108) because the per-asset detail
table is more tabular and less prose-heavy than the BCP plan's
five-scenario detail. Within the same range as the IR plan
(866 lines) which is the load-bearing first document in the
chain.

The length is not chosen as a constraint; it is the natural
length at which the required sections + sibling-parity discipline
are achieved.

---

## D12 — Anti-criteria honored explicitly

- **P0-376-1 (no sensitive identifiers verbatim):** see D5.
  Specific IPs, hostnames, vendor names, mailbox addresses are
  held privately. Names + types + owners + criticality only.
- **P0-376-2 (no code modified):** zero `.go` / `.ts` / `.py`
  / SQL files changed. Diff is governance documents + audit-log
  - slice-doc status + CHANGELOG only.
- **P0-376-3 (no CLAUDE.md or canvas changes):** zero. The
  canvas invariants and CLAUDE.md constitutional principles are
  cross-referenced but unmodified.
- **P0-376-4 (no new dependency):** zero. The slice ships pure
  Markdown.
- **P0-376-5 (no false asset claims):** every asset enumerated
  is either (a) currently held by the project (verified by the
  engineer cross-referencing access-review plan §2 + BCP plan §4
  - work-order enumeration) or (b) explicitly named as planned
    but not yet implemented (e.g., cosign signing key per slice
    368, JWT rotation per slice 366).

---

## Engineer-as-collaborator notes for the maintainer

1. **The BCP plan §4 conversion is land-bearing.** Per D1, the
   BCP plan §4 working table was converted to a pointer. If the
   maintainer subsequently realizes the BCP plan needs its own
   operational working view (e.g., a quick-reference card for
   the maintainer during an active continuity event), that's a
   future slice — keep the canonical inventory at one source.

2. **The "personal IT" boundary is explicit per P0-376-2.** The
   maintainer's workstation, password manager vendor, hardware
   token model, network architecture are deliberately held off
   this published inventory. If the maintainer wants a private
   addendum covering personal IT, that's a separate document
   (not published with the repo).

3. **The §8 hardening items table consolidates visibility on
   inventory-related gaps.** Items named there are not committed
   in this slice; they are surfaced for the maintainer's 2027-
   05-28 annual review prioritization. The two items with
   pre-existing slice tracking (366 + 368) are flagged as
   committed-elsewhere; the rest are stand-alone hardening
   surfaces.

4. **Documentation surfaces are aggregated at class-level per
   D8.** If the maintainer wants per-file inventory (e.g., the
   200+ audit-log files individually), that's a substantial
   tooling investment (the §8 hardening item
   "Asset-criticality binding script" would be the starting
   point). For now, class-level inventory satisfies ISO 27001
   5.9.

5. **No new adjacent inventorying gap was found.** Every asset
   surface the work-order enumerated, every BCP plan §4 row,
   every access-review plan §2 entry has a corresponding
   inventory row. The cross-document binding is complete.

---

## Slice 329 governance chain — COMPLETE

This slice closes the FIFTH and FINAL High-severity finding from
the slice 329 compliance meta-audit. The complete chain:

- **H-1 (IR plan)** — closed by slice 372 → `docs/governance/incident-response.md`
- **H-2 (BCP/DR plan)** — closed by slice 373 → `docs/governance/business-continuity.md`
- **H-3 (Access-review cadence)** — closed by slice 374 → `docs/governance/access-review.md`
- **H-4 (Data retention policy)** — closed by slice 375 → `docs/governance/data-retention.md`
- **H-5 (Asset inventory)** — closed by **slice 376** → `docs/governance/asset-inventory.md`

The five documents collectively close the "no consolidated
compliance evidence index" structural deficit that the audit
named as the load-bearing blocker for the v1 binary success
criterion. The project's operator-side compliance posture moves
from "strong technical controls but no auditor-ready artifacts"
to "documented operator-side compliance program suitable for a
sole-maintainer OSS project at this stage."

The medium-severity findings from slice 329 (M-1 through M-6)
remain audit-report-only per the audit's disposition (they share
the structural cause that the 5 Highs collectively close).

The next audit-driven governance work is the maintainer's choice
based on the 2026-Q3 compliance review or operator demand
signals; no further spillover slices from slice 329 are pending.
