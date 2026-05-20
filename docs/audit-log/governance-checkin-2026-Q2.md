# Governance checkin — 2026-Q2 (baseline)

**Period covered:** project inception through 2026-05-20.
**Filed by:** [@mgoodric](https://github.com/mgoodric)
**Filed at slice:** 181 (`docs/181-open-governance-pre-commitments.md`)

This is the **baseline quarterly governance checkin**, established at the
same moment as [`GOVERNANCE.md`](../../GOVERNANCE.md) itself. Future
quarters compare against the numbers below. Format defined in
GOVERNANCE.md "Re-evaluation trigger" → "Quarterly checkin".

---

## 1. GitHub release-download count

The pure-community-OSS posture has a **100 deployed self-hosts**
adoption trigger, proxied by cumulative GitHub release-download stats
across all release artifacts (binaries, docker images via GHCR, source
tarballs).

- **Cumulative at 2026-05-20:** baseline N = TBD at first measurement.
  At slice-181 commit time the repository is private and has not yet
  shipped a tagged public release; release-download stats are
  effectively zero. The metric becomes meaningful once the first
  public release tag lands.
- **Delta this quarter:** N/A (baseline).
- **Next measurement:** Q3 2026 checkin (filed by end of Q3) will be
  the first meaningful delta.

**Measurement mechanic (for the next maintainer reading this in a
year):**

```sh
gh api repos/mgoodric/security-atlas/releases --paginate \
  | jq '[.[].assets[].download_count] | add'
```

Plus GHCR pull counts (separate API surface; see
`scripts/governance-checkin.sh` if/when that helper is added) for
container-image deployments.

---

## 2. GitHub stars delta

Social signal — less load-bearing than downloads, but recorded for
trend visibility.

- **At 2026-05-20:** baseline. Repository is private at slice-181 time;
  stars are not yet observable.
- **Delta this quarter:** N/A (baseline).
- **Next measurement:** Q3 2026.

---

## 3. New contributor count

Distinct GitHub accounts that landed their first merged PR this quarter.
Counts toward the "≥ 3 active outside contributors with ≥ 6 months
involvement" bar for forming an advisory council (GOVERNANCE.md
"Maintainership").

- **This quarter:** 0 (zero outside contributors as of 2026-05-20).
- **Cumulative outside contributors:** 0.
- **Sustained-6-month outside contributors:** 0.
- **Distance from advisory-council formation:** 3 contributors short
  AND no contributor has 6 months of sustained involvement yet. BDFL
  remains the model.

---

## 4. Maintainer assessment — trigger fired?

**No.** Neither the date trigger (2028-05-20) nor the adoption trigger
(100 deployed self-hosts) is anywhere near firing. The maintainer
affirms the pure-community-OSS posture for the coming quarter.

**Re-evaluation trigger countdown:**

- Date trigger: **2028-05-20** — 2 years, 0 days from this checkin.
- Adoption trigger: **0 / 100** deployed self-hosts (baseline; not yet
  measurable while repo is private).

---

## 5. Anything else worth flagging

- **Bus-factor status.** Single primary maintainer
  ([@mgoodric](https://github.com/mgoodric)). The 2027-05-20
  recruitment target (one co-maintainer by Year 1) is **on the clock**
  starting this checkin. No candidate identified yet — the contributor
  flywheel needs to produce one organically.
- **Funding signals.** [`.github/FUNDING.yml`](../../.github/FUNDING.yml)
  ships with this slice. GitHub Sponsors button becomes visible on the
  GitHub UI when the repository goes public. No corporate sponsorship
  inquiries to date.
- **Governance documentation.** This checkin lands together with
  [`GOVERNANCE.md`](../../GOVERNANCE.md) authored at repo root and the
  `funding-discussion` GitHub issue label (configured per AC-12 of slice
  181 at PR-merge time by the maintainer).
- **OQ #5 resolution traceability.** This baseline checkin closes the
  loop on the OQ #5 resolution recorded in
  [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md)
  #5 (resolved 2026-05-20). The pre-commitments shipped:
  - (A1) Governance model: BDFL → advisory council path documented in
    GOVERNANCE.md.
  - (A2) Funding posture: GitHub Sponsors set up + not marketed.
  - (A3) Re-evaluation trigger: 2028-05-20 OR 100 self-hosts; this
    checkin establishes the proxy-measurement baseline.
  - (A4) DCO-only; no CLA — already in place via CONTRIBUTING.md;
    re-affirmed in GOVERNANCE.md.
  - (A5) GOVERNANCE.md authored — done in slice 181.

---

**Next checkin:** 2026-Q3 (file by end of September 2026 as
`docs/audit-log/governance-checkin-2026-Q3.md`).
