# Slice 350 — decisions log

Branch-coverage floor for security-critical packages. Each D-decision
is a JUDGMENT trade-off recorded inline per the per-slice template's
JUDGMENT-slice discipline.

## Context

Slice 333 (QA strategy audit) finding **Q-4**: a 75 %
line-coverage floor on `internal/api/oauth` is satisfied by happy-path
tests while dangerous error branches sit untested. The remediation is a
**security-critical branch-coverage floor tier** as a named subset of
the existing slice 069 ratchet.

The mechanical work is small. The judgement is in (a) what "branch"
honestly means under Go's coverage tooling and (b) seeding the initial
floors per the user-task spec's `P0-4` constraint (floors only, no new
test files in this slice).

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

The slice 350 tier is therefore named `$security_critical_floor`
(not `$branch_floor`) in `cmd/scripts/coverage-thresholds.json` —
**a more-aggressive statement-coverage floor on a named subset of
packages**, not literal branch coverage. The measurement caveat is
documented inline in the JSON's `$measurement_caveat` field, in the
gate's package doc-comment, and here.

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
security-critical tier, which captures the slice 333 Q-4 finding head
on. It is NOT being added to the `thresholds` line-tier in this slice
(scope discipline — the slice ships the tier mechanism, not new
line-tier enrolments).

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

## D3 — Floor methodology (matches slice 069)

**Question.** How are initial floors computed?

**Decision.** `max(0, floor(measured - 2pp))` — identical to the
slice 069 ratchet methodology used by every prior coverage slice
(279, 312, 313, 315, 317, 318, 319, 320, 321).

**Rationale.**

- A new tier with a NEW methodology would fork the ratchet contract.
  One methodology, two tiers is cleaner.
- The 2pp band absorbs measurement noise (test-order-dependent
  conditional branches, flaky integration-tier coverage variation).
- The user-task spec's `P0-3` explicitly requires it ("Do NOT seed
  branch floors above measured").

**Alternative considered + rejected.** Seed at the slice-doc's
aspirational target of 90 % across the roster. Rejected because:
(a) the user-task spec's `P0-4` forbids writing new tests in this
slice, so any package below 90 % could not be lifted to the target
without violating the constraint, (b) `internal/api/oauth` is at
15.74 % measured — seeding it at 90 % would force the gate to fail
on day 1, (c) the slice doc's AC-4 ("ship tests in SAME PR if below
90 %") is explicitly overridden by the user-task spec's P0-4.

**Revisit-trigger.** A follow-on slice (round-2) may lift roster
floors toward 90 % alongside writing the missing tests, per the slice
069 ratchet contract: "Raise a floor in a follow-up slice that
(a) writes the additional tests to clear the new bar, (b) lifts the
number here in the same PR."

**Confidence.** High — methodology has 8 prior precedents.

---

## D4 — Config shape (Option A: separate top-level block)

**Question.** How is the new tier encoded in
`cmd/scripts/coverage-thresholds.json`?

**Decision.** **Option A — a separate top-level block
`$security_critical_floor` with a `floors` sub-map** keyed by
package path.

```json
"$security_critical_floor": {
  "$comment": "...",
  "$measurement_caveat": "...",
  "$methodology": "...",
  "$how_to_raise": "...",
  "$how_to_extend": "...",
  "$roster_rationale": "...",
  "floors": {
    "internal/api/oauth": 13,
    ...
  }
}
```

**Rationale.**

- The existing `thresholds` map MUST stay untouched (P0-1: no
  existing line-coverage floor lowered, and by extension no shape
  change that would risk it).
- A separate block keeps the tier visible at the JSON-document level
  (a maintainer scanning the file sees the tier exists).
- `$`-prefixed comment fields are already the convention for inline
  documentation in this file (see `$comment`, `$methodology`,
  `$how_to_raise`, `$how_to_extend`, `$tier_recommendations`).
- The gate's struct-tag matching (`json:"$security_critical_floor"`)
  is straightforward.

**Alternative considered + rejected.** Option B — replace the flat
`thresholds: {pkg: pct}` map with `thresholds: {pkg: {line: pct,
branch: pct}}`. Rejected because: (a) it would change the wire shape
of every existing entry, (b) it would risk an off-by-one rename
break, (c) the slice 069 ratchet's git-blame history is more valuable
preserved than refactored.

**Revisit-trigger.** If the security-critical tier grows to >25
packages, evaluate whether option B's denser representation is
worthwhile.

**Confidence.** High.

---

## D5 — Initial floor table

**Question.** What are the seeded floors for each of the 10 roster
packages?

**Decision.** Floors derived from the CI merged unit + integration
coverage profile (`go-merged-coverage` artifact from CI run
`26558927897`, the most recent successful run on `main` that
exercised the full test surface, captured on 2026-05-28 at the start
of this slice).

| Package                     | Measured % | floor(measured − 2pp) | Current line floor |
| --------------------------- | ---------: | --------------------: | -----------------: |
| `internal/api/authzmw`      |      71.88 |                    69 |                 69 |
| `internal/api/oauth`        |      15.74 |                    13 |      (not in tier) |
| `internal/auth/jwt`         |      95.45 |                    93 |                 93 |
| `internal/auth/keystore`    |     100.00 |                    98 |                 98 |
| `internal/auth/oauthclient` |      86.79 |                    84 |                 84 |
| `internal/auth/oauthcode`   |      87.64 |                    85 |                 85 |
| `internal/auth/revocation`  |      79.55 |                    77 |                 77 |
| `internal/auth/tokensign`   |      74.51 |                    72 |                 72 |
| `internal/auth/userprefs`   |      85.19 |                    83 |                 83 |
| `internal/tenancy`          |      92.31 |                    90 |                 90 |

**Observations.**

1. **For 9 of 10 packages, the security-critical floor exactly
   matches the current line floor.** This is by design: the slice
   ships the tier MECHANISM. The numerical bar is not yet more
   aggressive than the existing line bar. The value is the named
   subset + the gate's ability to enforce a HIGHER bar in a follow-on
   slice that also writes the tests.
2. **`internal/api/oauth` is the outlier at 15.74 %.** This package
   was never in the line tier, so its measurement was never floored.
   The 15.74 % reflects unit-test coverage only — the package has
   `integration_test.go` files but is NOT in the CI integration test
   list (the CI integration target list in `.github/workflows/ci.yml`
   does not include `./internal/api/oauth/...`). The honest seeded
   floor is therefore 13 (= floor(15.74 − 2)).
3. The slice 333 Q-4 finding is now empirically validated at the
   package level: `internal/api/oauth` has 145 of 921 statements
   covered. That gap is the "happy-path coverage hides dangerous error
   branches" pattern in raw form. The remediation is a follow-on
   slice (NOT this one — P0-4) that (a) enrols `internal/api/oauth`
   in the CI integration test list and (b) lifts the security-critical
   floor as the tests land.

**Revisit-trigger.** The slice 069 ratchet contract — monotonic
upward, never lower. A follow-on slice may lift any of these floors
in lockstep with writing the necessary tests.

**Confidence.** High — measurements taken from the canonical CI
artifact, methodology identical to slice 069.

---

## Cross-references

- Slice 069 — original line-coverage ratchet (`cmd/scripts/coverage-gate`).
- Slice 279 — merged unit + integration profile measurement basis.
- Slice 312 — round-3 audit; precedent for `floor(measured - 2pp)` seeding.
- Slice 315 — auth-substrate-v2 small-packages line-floor enrolment
  (sister slice; covers the small packages now joining the new tier).
- Slice 333 — QA strategy gap analysis; Q-4 is this slice's trigger.
- Slice 334 — test-framework review (audit context).
