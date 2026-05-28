# Slice 350 — decisions log

Security-critical advisory tier for the auth-substrate-v2 + tenancy
spine. Each D-decision is a JUDGMENT trade-off recorded inline per
the per-slice template's JUDGMENT-slice discipline.

## Context

Slice 333 (QA strategy audit) finding **Q-4**: a 75 %
line-coverage floor on `internal/api/oauth` is satisfied by happy-path
tests while dangerous error branches sit untested. The remediation is a
**security-critical tier** as a named subset of the existing slice 069
ratchet — with an ADVISORY 90 % target (not a hard floor) layered on
top of the existing per-package hard floors in `thresholds`.

The mechanical work is small. The judgement is in (a) what "branch"
honestly means under Go's coverage tooling, (b) whether the tier
enforces a SECOND hard floor or only an advisory warning, and (c) how
the tier is encoded in the JSON config + the gate code.

### Design tensions in the slice doc as filed

The slice doc (`docs/issues/350-branch-coverage-floor-security-critical.md`)
had two internal contradictions that the dispatch brief resolved
before this build:

1. **"Branch coverage" in Go is not a native primitive.** Go's
   `-cover` measures per-basic-block, not McCabe branch (see D1).
   Resolved by redefining the tier as "enhanced statement-coverage"
   with honest naming.
2. **"Start at truth" vs "lift to 90 % in the same PR".** The slice
   doc said both. Slice 069's monotonic ratchet contract says one
   (start at truth; lifts are separate slices). Resolved by following
   slice 069 — no new tests in 350; the 90 % target is advisory only
   (warning, not failure).

D3 and D4 below were both reshaped by these resolutions. The slice
ships the **tier-definition mechanism** + an **advisory-only check**;
per-package lifts toward 90 % are downstream slice work, each one
writing tests AND raising the hard floor in the same PR per the slice
069 contract.

---

## D1 — What "branch coverage" means under Go's coverage tooling

**Question.** The slice title says "branch coverage". Does Go's
`-coverprofile=` actually measure branch coverage in the McCabe
sense (each predicate's true and false outcome tracked
independently)?

**Decision.** **No — and the tier is named honestly.**

**Empirical verification (Go 1.22 toolchain).** A two-conditional probe
under all three covermodes (`set`, `count`, `atomic`) emits an
identical 5-block profile shape for this function:

```go
func Classify(x int) string {
    if x < 0  { return "neg"  }
    if x == 0 { return "zero" }
    return "pos"
}
```

The profile blocks are:

| Block range  | numStmt | Meaning                          |
| ------------ | ------- | -------------------------------- |
| `3.29,4.11`  | 1       | function entry → first `if` test |
| `4.11,6.3`   | 1       | body of `if x < 0`               |
| `7.2,7.12`   | 1       | fall-through to second `if` test |
| `7.12,9.3`   | 1       | body of `if x == 0`              |
| `10.2,10.14` | 1       | final `return "pos"`             |

A test that exercises only the negative path hits blocks 1 and 2 and
leaves blocks 3, 4, 5 unhit. **For simple conditionals, each arm
DOES show up as a separate block** — block coverage is therefore an
approximation of branch coverage.

**But:** a probe with a compound predicate

```go
func Compound(a, b bool) string {
    if a && b { return "both" }
    return "not-both"
}
```

produces only 3 blocks: entry+predicate, body, fall-through. The
short-circuit cases (`a=true, b=false` vs `a=false, b=anything`)
**cannot be distinguished** by Go's block coverage. JaCoCo / gcov-style
branch coverage would track each predicate independently.

**Conclusion.**

- Go's `-coverprofile=` reports per-basic-block coverage in all three
  modes (`set`, `count`, `atomic`).
- For SIMPLE predicates, block coverage ≈ branch coverage. Each arm of
  an `if`/`else if`/`else` chain or `switch`/`case` is its own block.
- For COMPOUND predicates (`&&`, `||`), block coverage is **coarser**
  than McCabe branch coverage.

The slice 350 tier is therefore named `$security_critical_packages`
(not `$branch_floor`) in `cmd/scripts/coverage-thresholds.json` —
**an enhanced statement-coverage advisory on a named subset of
packages**, not literal branch coverage. The advisory bar is the
single `advisory_target_pct: 90` and the tier membership is a flat
`packages: [string]` list. See D3 for the advisory-vs-hard-floor
decision and D4 for the as-shipped config shape. The measurement
caveat is documented inline in the JSON's `$measurement_caveat`
field, in the gate's package doc-comment, and here.

**Alternative considered + rejected.** Drive `go test -cover` through
a tool that adds true branch-coverage instrumentation (e.g. `gobco`).
Rejected because: (a) it would diverge from the slice 069 / 279
ratchet's measurement basis, (b) it would require a separate CI
artifact + parser, (c) the existing block-level resolution is already
sufficient for the named threat (untested error paths in simple-
conditional auth-substrate code), and (d) it would add a third-party
dependency on a single-maintainer tool.

**Revisit-trigger.** When a security incident traces to a compound-
predicate-only failure mode in an auth-substrate-v2 package, file a
follow-on slice to evaluate true branch-coverage instrumentation.

**Confidence.** High. The Go toolchain behaviour was verified by
direct probe before this log was written.

---

## D2 — Roster choice (10 packages: auth-substrate-v2 + tenancy)

**Question.** Which packages enter the security-critical tier?

**Decision.** Exactly 10 packages — the auth-substrate-v2 spine plus
tenancy:

| Package                     | Why it qualifies                                |
| --------------------------- | ----------------------------------------------- |
| `internal/auth/jwt`         | JWT signing + verification primitive (RFC 9068) |
| `internal/auth/keystore`    | Key material at rest                            |
| `internal/auth/tokensign`   | Token-signing wrapper                           |
| `internal/auth/oauthclient` | OAuth client registry                           |
| `internal/auth/oauthcode`   | OAuth code-grant primitive (PKCE)               |
| `internal/auth/revocation`  | Token revocation (RFC 7009)                     |
| `internal/auth/userprefs`   | User-preference primitive (read in auth path)   |
| `internal/api/oauth`        | OAuth 2.0 AS HTTP handler surface (slice 187+)  |
| `internal/api/authzmw`      | RBAC + super_admin enforcement middleware       |
| `internal/tenancy`          | RLS context-propagation primitive               |

**Rationale.** Two filters apply:

1. **The slice doc's published roster** (the canonical author intent).
   The slice doc lists 10 packages under "Proposed initial roster"
   with the user-task spec's `P0-2` reinforcing it: "Do NOT extend the
   roster beyond auth-substrate-v2 + tenancy."
2. **Path-resolution correction.** The slice doc's
   `internal/api/oauth/{oauthcode|oauthclient|revocation|userprefs}`
   are misremembered paths — those subpackages actually live under
   `internal/auth/`. The actual codebase layout, verified by
   `ls internal/auth/ && ls internal/api/oauth/`, has the listed
   primitives at `internal/auth/`. Roster uses canonical paths.

**Special note on `internal/api/oauth`.** This package was previously
absent from `coverage-thresholds.json` line-tier — neither in
`thresholds` nor in `excludes`. The slice 350 roster adds it to the
security-critical advisory tier only. It is NOT being added to the
`thresholds` line-tier in this slice (scope discipline — the slice
ships the tier mechanism, not new line-tier enrolments). A follow-on
slice should (a) enrol `internal/api/oauth` in the CI integration
test list (it has `integration_test.go` files but the integration
target list in `.github/workflows/ci.yml` does not include it),
(b) write the missing error-branch tests, and (c) add the package to
`thresholds` with a floor derived from the post-enrolment measured
coverage.

**Packages explicitly excluded from the roster.**

- `internal/auth/jwtmw` — JWT MIDDLEWARE, not the JWT lib itself.
  In the line tier already (floor 82); not in the slice doc's roster.
- `internal/auth/bearer` — Pre-auth-substrate-v2 bearer-token
  primitive being retired; not in the slice doc's roster.
- `internal/auth/password` — Password storage; tangential to the
  Q-4 threat (OAuth error paths) and not in the slice doc's roster.
- `internal/auth/{sessions,oidc,users,apikeystore}` — currently in
  `excludes[]` (integration-tested elsewhere or in transition). The
  P0-2 anti-criterion forbids extending the roster speculatively.

**Revisit-trigger.** Round-2 may add packages once the tier's value
is demonstrated by a caught regression. Specifically, evaluate
`internal/auth/jwtmw`, `internal/auth/oidc` (when un-excluded), and
`internal/api/auth` (when un-excluded).

**Confidence.** High — the roster matches the slice doc + the
auth-substrate-v2 spine 1:1 after path correction.

---

## D3 — Floor methodology: ADVISORY-only, hard floors stay in `thresholds`

**Question.** Does the tier introduce a SECOND hard floor per
roster package (alongside the existing line-tier hard floor in
`thresholds`), or is the tier ADVISORY-only?

**Decision.** **Advisory-only.** The tier is a named MARKER plus a
single global `advisory_target_pct: 90`. Hard per-package floors stay
exclusively in `thresholds` (slice 069 contract preserved). The gate
emits a warning to stderr when a tier package's measured coverage
falls below the 90 % advisory target — but does NOT fail.

**Rationale.**

- The user-task spec's `P0-4` forbids writing new tests in this
  slice. A hard floor at 90 % across the roster would fail the gate
  on day 1 for 7 of 10 packages — unshippable.
- The user-task spec's `P0-1` forbids lowering any existing line-
  coverage floor. A SECOND per-package hard floor at
  `max(0, floor(measured - 2pp))` would be approximately equal to
  the existing line floor for 9 of 10 packages — ceremony without
  signal. For `internal/api/oauth` (measured 15.74 %), a hard floor
  of 13 % would be a public commitment to a low bar in a
  security-critical context.
- The slice doc's AC-4 ("ship missing tests in SAME PR; lift to
  90 % in SAME PR") is explicitly overridden by the user-task
  spec's `P0-4`. Advisory-only preserves the spirit of AC-4
  (visibility into the 90 % target) without violating `P0-4` (no
  new tests in this slice).
- Slice 069's ratchet contract — "Raise a per-package floor in a
  follow-up slice that (a) writes the additional tests, (b) lifts
  the number in the same PR" — fits the advisory pattern cleanly.
  Each follow-on slice lifts ONE package's hard floor in
  `thresholds` toward the 90 % advisory target.

**Alternatives considered + rejected.**

- _Per-package hard floors at `max(0, floor(measured - 2pp))` in
  a `floors: {pkg: pct}` sub-map._ Rejected per the redundancy +
  embarrassing-13%-floor concerns above. Adds wire surface, no
  enforcement signal beyond the line tier.
- _Tier-wide hard floor at 90 % failing the gate._ Rejected — would
  block the slice from merging given `P0-4`.

**Revisit-trigger.** If a regression escapes through the advisory
(measured coverage falls AND no follow-on lift slice catches it),
promote the advisory to a tier-wide hard floor that matches each
package's current measured coverage.

**Confidence.** High.

---

## D4 — Config shape: `$security_critical_packages` block with `packages` list + `advisory_target_pct`

**Question.** How is the tier encoded in
`cmd/scripts/coverage-thresholds.json`?

**Decision.** A separate top-level block `$security_critical_packages`
with TWO data fields (`advisory_target_pct: 90` and
`packages: [pkg, …]`) plus `$`-prefixed comment fields documenting
rationale. `$schema_version` is bumped from 2 to 3.

```json
"$security_critical_packages": {
  "$comment": "...",
  "$trigger": "...",
  "$measurement_caveat": "...",
  "$advisory_vs_hard": "...",
  "$how_to_lift_toward_advisory": "...",
  "$how_to_extend_roster": "...",
  "$roster_rationale": "...",
  "advisory_target_pct": 90,
  "packages": [
    "internal/api/authzmw",
    "internal/api/oauth",
    "internal/auth/jwt",
    ...
  ]
}
```

**Rationale.**

- The existing `thresholds` map MUST stay untouched (P0-1: no
  existing line-coverage floor lowered, and by extension no shape
  change that would risk it).
- A separate top-level block keeps the tier visible at the JSON
  document level — a maintainer scanning the file sees the tier
  exists.
- `packages: [string]` flat list is the simplest possible shape: the
  tier is a SET of package names; per-package floors are NOT carried
  here (they live in `thresholds`). Set-of-strings is the minimum
  data the advisory check needs.
- One global `advisory_target_pct: 90` is sufficient because the
  90 % target is the slice doc's tier-wide aspiration (slice 333
  Q-4). Per-package per-target customization adds configuration
  surface without need.
- `$`-prefixed comment fields are the file's existing convention
  for inline documentation (`$comment`, `$methodology`,
  `$how_to_raise`, `$how_to_extend`, `$tier_recommendations`).
- The gate's struct-tag matching
  (`json:"$security_critical_packages,omitempty"`) is straightforward
  and backward-compatible (older configs without the block continue
  to parse).

**Alternatives considered + rejected.**

- _Per-package hard floors map (`floors: {pkg: pct}`)._ Rejected per
  D3 — the tier is advisory, not hard, so per-package floors would
  imply more enforcement than the design provides.
- _Embed the tier flag in `thresholds`_ (e.g.
  `{"internal/api/oauth": {"floor": 13, "tier": "security-critical"}}`).
  Rejected — it would change the wire shape of every existing entry,
  risking an off-by-one rename break, and the slice 069 ratchet's
  git-blame history is more valuable preserved than refactored.

**Revisit-trigger.** If the tier grows to >25 packages OR if
per-package targets become useful (e.g. JWT signing demands 100 %
but authorization middleware accepts 85 %), evaluate adding
`overrides: {pkg: pct}` alongside `advisory_target_pct`.

**Confidence.** High.

---

## D5 — Initial measurement table

**Question.** Where does each of the 10 roster packages stand against
the 90 % advisory target on the as-shipped CI artifact?

**Decision.** Measurements taken from the CI merged unit +
integration coverage profile (`go-merged-coverage` artifact from CI
run `26558927897`, the most recent successful run on `main` that
exercised the full test surface, captured 2026-05-28 at the start of
this slice).

| Package                     | Measured % | Δ vs 90 % advisory | Current line floor |
| --------------------------- | ---------: | -----------------: | -----------------: |
| `internal/api/authzmw`      |      71.88 |             −18.12 |                 69 |
| `internal/api/oauth`        |      15.74 |             −74.26 |      (not in tier) |
| `internal/auth/jwt`         |      95.45 |              +5.45 |                 93 |
| `internal/auth/keystore`    |     100.00 |             +10.00 |                 98 |
| `internal/auth/oauthclient` |      86.79 |              −3.21 |                 84 |
| `internal/auth/oauthcode`   |      87.64 |              −2.36 |                 85 |
| `internal/auth/revocation`  |      79.55 |             −10.45 |                 77 |
| `internal/auth/tokensign`   |      74.51 |             −15.49 |                 72 |
| `internal/auth/userprefs`   |      85.19 |              −4.81 |                 83 |
| `internal/tenancy`          |      92.31 |              +2.31 |                 90 |

**Observations.**

1. **3 of 10 packages already meet the 90 % advisory target.**
   `internal/auth/jwt` (95.45 %), `internal/auth/keystore`
   (100 %), and `internal/tenancy` (92.31 %). These packages
   already satisfy the discipline; the advisory does not fire for
   them.
2. **7 of 10 packages fall below the 90 % advisory target.** Each
   is a candidate for a follow-on lift slice. Sorted by gap:
   - `internal/api/oauth` (−74.26 pp) — the largest gap and the
     direct subject of slice 333 Q-4.
   - `internal/api/authzmw` (−18.12 pp)
   - `internal/auth/tokensign` (−15.49 pp)
   - `internal/auth/revocation` (−10.45 pp)
   - `internal/auth/userprefs` (−4.81 pp)
   - `internal/auth/oauthclient` (−3.21 pp)
   - `internal/auth/oauthcode` (−2.36 pp)
3. **`internal/api/oauth` is the headline gap.** 145 of 921
   statements covered. The CI integration target list in
   `.github/workflows/ci.yml` does NOT include
   `./internal/api/oauth/...`, so the merged profile only sees
   unit-test coverage. A follow-on slice should (a) enrol
   `internal/api/oauth` in the CI integration test list, (b) write
   the missing error-branch tests (invalid grant, expired code,
   revoked token, tenant-switch denied, super_admin escalation
   refused), (c) add the package to `thresholds` with a floor
   derived from the post-enrolment measured coverage. This is
   exactly the slice 333 Q-4 remediation pattern.

**Gate output on this measurement basis (verified locally before
commit):**

```
coverage-gate: checked 107 packages, 0 failed, 0 warnings (no profile data)
coverage-gate (security-critical advisory, target 90%): 10 tier package(s), 7 advisory, 0 no-data

coverage-gate ADVISORY (security-critical tier, slice 350 — non-blocking):
  internal/api/authzmw: got 71.9% < advisory target 90.0%
  internal/api/oauth: got 15.7% < advisory target 90.0%
  internal/auth/oauthclient: got 86.8% < advisory target 90.0%
  internal/auth/oauthcode: got 87.6% < advisory target 90.0%
  internal/auth/revocation: got 79.5% < advisory target 90.0%
  internal/auth/tokensign: got 74.5% < advisory target 90.0%
  internal/auth/userprefs: got 85.2% < advisory target 90.0%
HARD FLOORS PASS · security-critical advisory not yet met (non-blocking)
```

Exit code 0 — the advisory does NOT fail CI. Slice 069 line-tier
hard floors remain the only failure surface.

**Revisit-trigger.** Each follow-on lift slice marks one row of this
table as resolved by lifting that package's hard floor in
`thresholds` AND writing the necessary tests. The advisory line for
that package disappears as the work lands.

**Confidence.** High — measurements taken from the canonical CI
artifact, gate behaviour verified locally before merge.

---

## Cross-references

- Slice 069 — original line-coverage ratchet (`cmd/scripts/coverage-gate`).
- Slice 279 — merged unit + integration profile measurement basis.
- Slice 312 — round-3 audit; precedent for floor methodology.
- Slice 315 — auth-substrate-v2 small-packages line-floor enrolment
  (sister slice; covers the small packages now joining the new tier).
- Slice 333 — QA strategy gap analysis; Q-4 is this slice's trigger.
- Slice 334 — test-framework review (audit context).
