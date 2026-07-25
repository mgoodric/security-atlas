# Slice 357a — Chaos Experiment 4 execution: OIDC IdP unavailable · decisions log

**Type:** JUDGMENT · **Approach:** execute the slice-335 design as written (no redesign) · **Date:** 2026-07-24

- detection_tier_actual: `manual_review` (chaos run)
- detection_tier_target: `integration`

> The headline gap (G-1) is reachable from the integration tier and is not
> reached today: nothing in the suite asserts that the docker-compose wiring
> of the OIDC RP can serve a login at all. `internal/auth/oidc` has good unit
> coverage — `TestBeginLoginPropagatesResolverError` even pins the exact
> error path that fires here — but it exercises the package against a stub
> resolver, never against the resolver `cmd/atlas/main.go` actually wires.
> That gap between "the package works" and "the deployment works" is what a
> chaos run walked into, and it is the argument for the follow-up filed below.

**Design contract:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 4.
**Slice narrative:** [`docs/issues/357-auth-substrate-chaos-round-1.md`](../issues/357-auth-substrate-chaos-round-1.md).
**Scope of THIS log:** Experiment 4 only. Slice 357 bundles Experiments 4, 6
and 8; this log is filed at `357a-` so the bundled path named by slice 357
AC-17 is not created half-populated — the same reason 356a/356b split. Exp 6
(cosign) and Exp 8 (OPA, high-risk, additional-reviewer-gated) are untouched
here and remain slice 357's to execute.

**Tooling:** [`scripts/chaos/run-exp4-oidc-idp.sh`](../../scripts/chaos/run-exp4-oidc-idp.sh),
[`scripts/chaos/exp4-idp-overlay.yml`](../../scripts/chaos/exp4-idp-overlay.yml),
[`scripts/chaos/exp4-dex-config.yaml`](../../scripts/chaos/exp4-dex-config.yaml).
**Run of record:** tag `oe386`, 2026-07-25T01:51:44Z → 02:15:48Z UTC.

---

## Headline

**Two of the hypothesis's three clauses HOLD, and hold strongly. The third
could not be tested at all — and finding out why is the experiment's most
valuable result.**

The design's central claim is that atlas-as-issuer is independent of
atlas-as-relying-party: knock out the IdP and existing sessions should not
notice, because JWT verification is local. That claim survived three
different injection mechanisms plus a process restart, with no measurable
degradation — not a slower path, not a single non-200, no latency shift.

The third clause — new logins degrade gracefully — is **unexercisable on this
deployment**, because the docker-compose bundle has no working OIDC relying
party to degrade. `cmd/atlas/main.go:667` wires the authenticator as
`oidc.New(localModeIdpResolver{})`, and that resolver returns `ErrUnknownIdp`
unconditionally (`main.go:1310-1312`). `BeginLogin` resolves before it touches
the provider cache, so `/auth/oidc/login` returns **400 on every request,
whether the IdP is up, down, or never existed**. The new-login surface returned
a byte-identical body in steady state and under every injection.

That is not a null result to shrug at. Three things follow from it, and each is
a finding in its own right:

1. **`PATCH /v1/admin/sso` returns 200 and persists the IdP config row that the
   running binary then never reads.** An operator configures SSO, is told it
   worked, and SSO does not work. The experiment's own preflight did exactly
   this and got a 200 back (gate C-1c).
2. **The design's specified degradation contract does not exist in the
   codebase.** `auth_provider_unavailable` appears nowhere outside the design
   document. There is no 503 path, and no code path distinguishes
   "IdP unreachable" from "IdP not configured" — both are the same 400.
3. **The design's recovery expectation rests on a refresh that is not
   implemented.** "New logins resume within 30s (OIDC discovery refresh
   interval)" presumes a discovery refresh. `Authenticator.provider()` caches
   `*coreos.Provider` per issuer in a plain map with no TTL and no
   invalidation. Even on a deployment whose resolver works, a warm cache means
   `BeginLogin` never re-contacts the IdP — the outage would be invisible going
   in, and there would be nothing to "resume" coming out.

So: the resilience claim the experiment set out to test is intact, and the
adjacent claim it could not test turns out to rest on a surface that is not
wired, not specified, and not refreshed. Per the design's own framing, this is
a successful experiment. It is recorded plainly here and carried into
follow-ups.

---

## Pre-execution checklist — sign-off (slice 357 AC-2, design §Experiment 4)

The design's checklist has two items. Both were executed by `preflight()` in
`scripts/chaos/run-exp4-oidc-idp.sh` against the running stack and signed off
against real output, not asserted. Seven gates were added because the design's
two items are not sufficient on their own to make the run meaningful; the
additions are part of the sign-off and are marked as such. **Injection did not
start until every gate read PASS** — the script hard-refuses otherwise
(`preflight()` final line), which is how slice 357's "do NOT skip the
checklist" is enforced in tooling rather than left to operator care.

| ID   | Item                                                              | Source               | Result on the run of record                                                                                                |
| ---- | ----------------------------------------------------------------- | -------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| S-1  | Stack up and atlas healthy before injecting                       | added (operational)  | PASS — 6 services running, atlas health=healthy                                                                            |
| C-1  | Containerized IdP, NOT a real external IdP                        | **design**           | PASS — `ghcr.io/dexidp/dex:v2.44.0`, issuer `http://dex:5556/dex` (in-compose service name; resolves nowhere else)         |
| C-1b | IdP actually serves OIDC discovery, from atlas's own netns        | added (see D1)       | PASS — `GET /.well-known/openid-configuration` → 200                                                                       |
| C-1c | An IdP config row exists for the tenant, pointing at the fixture  | added                | PASS — `oidc_idp_configs(primary).issuer_url=http://dex:5556/dex`, written via `PATCH /v1/admin/sso` → 200                 |
| C-2  | Active JWT minted BEFORE injection; `exp` claim recorded          | **design**           | PASS — `exp=1784947907` (2026-07-25T02:51:47Z), `iat=1784944307`                                                           |
| C-2b | That JWT actually authenticates on a protected endpoint           | added                | PASS — `GET /v1/me` → 200                                                                                                  |
| C-2c | JWT TTL outlives the whole run window                             | added                | PASS — 3599s remaining vs 900s needed; expiry cannot confound a late failure                                               |
| C-3  | `/oauth/authorize` reaches the login path, not param-validation   | added                | PASS — no-session GET → 302 to `/auth/oidc/login?return_to=…`                                                              |
| C-5  | **New-login path actually contacts the IdP (design step 5 live)** | added (LOAD-BEARING) | **VACUOUS** — `GET /auth/oidc/login` → 400 `{"error":"OIDC begin: oidc: unknown IdP"}` while the IdP is provably up (C-1b) |
| C-4  | Key-rotation cron observed ticking BEFORE injection               | added (LOAD-BEARING) | PASS — 1 rotation event inside a 60s cadence window                                                                        |
| H-1  | Harness floor recorded                                            | added (measurement)  | INFO — a loopback `/health` round trip costs 6ms through the same construct                                                |

Two of the added gates carry most of the weight, and both exist because a
chaos run can pass vacuously in ways the design's two items do not catch:

- **C-4** — "key-rotation continues without errors" is worth nothing if no
  rotation ever fires. The shipped default cadence is **annual**
  (`cmd/atlas/main.go` `defaultKeyRotationInterval`), so on an unmodified
  stack the cron would not tick once in a 25-minute run and the design's third
  check would pass by never happening. See D2.
- **C-5** — every other gate can pass while the experiment still measures
  nothing. C-1b proves the IdP serves discovery; C-1c proves a config row
  exists; C-3 proves `/oauth/authorize` reaches the login path — and the run is
  _still_ vacuous if the RP never contacts the IdP. C-5 is the gate that
  caught it. See D3 for why it records VACUOUS rather than FAIL.

---

## What was run

Local docker-compose only. `deploy/docker/docker-compose.yml` plus an
experiment overlay that adds the Dex fixture and shortens the key-rotation
cadence; nothing else about the stack changed, and the overlay is removed
after the run. Every action is scoped to one named local compose project by
three guards in the script: the atlas endpoint must be a loopback literal, the
IdP issuer must be the in-compose service name, and the container to detach is
resolved via `docker compose ps -q dex` against the compose files passed in.
No hosted or edge endpoint appears anywhere in `scripts/chaos/` (slice 335
P0-335-2, slice 357 P0-1 / P0-2).

**Timeline:** 300s steady state captured **before any injection**, then three
arms, each holding **300s** at a 5s probe cadence, each followed by a measured
recovery and a 60s post-recovery observation.

| Arm   | Mechanism                                                     | Source                           | Cache state |
| ----- | ------------------------------------------------------------- | -------------------------------- | ----------- |
| **A** | `docker network disconnect` the IdP from the project network  | design Variable, parenthetical   | warm        |
| **B** | `iptables -A OUTPUT -d <idp-ip> -j DROP` inside atlas's netns | design Variable, **first-named** | warm        |
| **C** | detach the IdP, _then_ restart atlas                          | added — see D4                   | **cold**    |

Five probes per tick: the existing JWT on `/v1/me`; a new `/oauth/authorize`
attempt with no session; `/auth/oidc/login` (the surface that would actually
talk to the IdP); the IdP's discovery document **from inside atlas's own
network namespace**; and `/health`. Key-rotation events and error lines are
counted per phase from the atlas container log.

---

## Steady state vs injection — the measured comparison (slice 357 AC-3, AC-5)

Status-code histograms, `code × ticks`. The tick counts differ between arms
because each probe is timeout-bounded — arm B's discovery probe blocks for the
full 3s connect timeout on every tick, so fewer ticks fit in the same 300s.
That difference is not noise; it _is_ arm B's result (see D5).

| Probe                          | Steady state (300s) | Arm A injection | Arm B injection | Arm C injection |
| ------------------------------ | ------------------- | --------------- | --------------- | --------------- |
| Existing JWT on `/v1/me`       | **200 × 54**        | **200 × 53**    | **200 × 34**    | **200 × 52**    |
| `/oauth/authorize` (new login) | 302 × 54            | 302 × 53        | 302 × 34        | 302 × 52        |
| `/auth/oidc/login` (RP entry)  | 400 × 54            | 400 × 53        | 400 × 34        | 400 × 52        |
| IdP discovery (atlas netns)    | **200 × 54**        | **000 × 53**    | **000 × 34**    | **000 × 52**    |
| atlas `/health`                | 200 × 54            | 200 × 53        | 200 × 34        | 200 × 52        |
| Key rotations / error lines    | **5 / 0**           | **5 / 0**       | **5 / 0**       | **5 / 0**       |

Latency, mean / max ms:

| Probe                       | Steady state | Arm A       | Arm B             | Arm C       |
| --------------------------- | ------------ | ----------- | ----------------- | ----------- |
| Existing JWT on `/v1/me`    | 8.3 / 18     | 7.8 / 15    | 7.1 / 14          | 6.9 / 16    |
| `/oauth/authorize`          | 3.3 / 7      | 2.8 / 6     | 2.9 / 6           | 3.1 / 6     |
| `/auth/oidc/login`          | 2.6 / 6      | 2.5 / 7     | 2.3 / 7           | 2.0 / 4     |
| IdP discovery (atlas netns) | 0.4 / 1      | **2.2 / 5** | **3004.8 / 3011** | **2.0 / 7** |
| atlas `/health`             | 2.1 / 5      | 2.0 / 5     | 2.1 / 5           | 1.7 / 4     |

**The injection reached the variable in every arm.** The discovery probe —
taken from inside atlas's own network namespace, i.e. the exact egress path
the RP's discovery call would take — flipped 200 → 000 at every injection
boundary and back on every recovery. A run whose witness does not flip is not
evidence of anything; this one's did, three times.

**The existing-JWT path did not move.** Not in status code (193 injected
ticks, all 200), and not in latency: 8.3ms steady state against 7.8 / 7.1 /
6.9ms under injection, all of it sitting on a 6ms harness floor (gate H-1) and
comfortably inside the ~1–2ms ES256-verify baseline slice 332 carries forward
from slice 188 D-Argon-1. The verification path never touched the IdP, so
removing the IdP cost it nothing. See D6 on the baseline.

---

## Falsification verdicts

### The design's hypothesis, clause by clause

> **Hypothesis.** When the external OIDC IdP becomes unreachable, **existing**
> JWT sessions continue to authenticate successfully for the remainder of
> their TTL. **New** logins fail with a user-friendly error (not a stack
> trace). The atlas-issued JWT key-rotation is unaffected (the AS-as-issuer
> surface is independent of the AS-as-RP surface).

| Clause                                           | Verdict                      | Evidence                                                                                     |
| ------------------------------------------------ | ---------------------------- | -------------------------------------------------------------------------------------------- |
| Existing JWT sessions continue for TTL remainder | **HOLDS**                    | 200 on all 193 injected ticks across three mechanisms; no latency shift; `exp` never reached |
| New logins fail with a user-friendly error       | **NOT TESTABLE — see below** | Byte-identical 400 before and during every injection; the surface never observed the outage  |
| Key-rotation unaffected (issuer ⟂ RP)            | **HOLDS**                    | 5 rotations / 0 error lines in steady state and in every injection arm — identical           |

### The middle clause, stated precisely

This is the part worth not blurring. The experiment **cannot** falsify "new
logins degrade gracefully when the IdP goes down", because on this deployment
new OIDC logins do not work when the IdP is _up_. There is no causal path from
the injection to that surface, so no injection could have moved it.

What the experiment **does** establish, by direct measurement plus code
reading:

- The design's expected outcome — `503 {"error": "auth_provider_unavailable",
"retry_after": 30}` — **is not implemented anywhere in the codebase.** The
  string appears in the design document and nowhere else. That is a
  falsification of the design's expectation as a description of the system,
  even though it is not a falsification of the resilience claim.
- The observed error _is_ structured JSON and _is not_ a stack trace —
  `{"error":"OIDC begin: oidc: unknown IdP"}` — so the "not a stack trace"
  half of the clause is satisfied incidentally. It is the wrong status code
  (400, not 503), carries no `retry_after`, and does not distinguish
  IdP-unreachable from IdP-not-configured.

### The design's abort criteria

Both were evaluated on every tick of every injection phase. Neither tripped.

| Criterion                                                    | Arm A | Arm B | Arm C | Result      |
| ------------------------------------------------------------ | ----- | ----- | ----- | ----------- |
| Existing JWT verification fails                              | 0     | 0     | 0     | not tripped |
| atlas crashes on IdP-unreachable (missing discovery timeout) | 0     | 0     | 0     | not tripped |

`RestartCount` was 0 → 0 across every arm. Arm C's restart was an explicit
`docker compose restart` and correctly does not increment that counter, which
is what keeps it usable as a crash detector rather than a restart counter.

Arm B is what makes the second criterion a real check rather than a formality.
That criterion is about a _missing timeout_, and only a blackhole produces the
failure shape that would expose one — a detach-only run fails fast at name
resolution and can never hang. Under a genuine 3s-per-call connect timeout,
atlas stayed healthy on all 34 ticks and never restarted. See D5.

### Slice 357's Experiment-4 acceptance criteria

| AC   | Status                | Note                                                                                                            |
| ---- | --------------------- | --------------------------------------------------------------------------------------------------------------- |
| AC-1 | MET                   | Dex `v2.44.0`, in-compose issuer, enforced by a script guard that refuses any non-in-compose issuer             |
| AC-2 | MET                   | Checklist executed and signed off against real output; see table above                                          |
| AC-3 | MET                   | IdP detached (arm A / C) and blackholed (arm B); existing JWT verified 200 on every injected tick               |
| AC-4 | **FAILED — reported** | `/oauth/authorize` returned **302**, not 503. No structured 503 body exists to return. Recorded as G-2          |
| AC-5 | MET                   | 5 rotations / 0 error lines per arm, identical to steady state; cadence gate C-4 made the check non-vacuous     |
| AC-6 | **VACUOUS PASS**      | IdP reachable again in 0–1s and both login surfaces at their steady-state code in 0–1s — but they never left it |

AC-4 is an assertion about the platform, not about this slice's execution, and
it did not hold. AC-6 is recorded as a vacuous pass rather than a pass: a
surface that did not change under the outage "recovers" at t+0s, which is not
a recovery measurement. The tooling keeps three recovery clocks apart
specifically so that this stays legible instead of reading as a fast recovery
(D7).

---

## D1 — Measuring the injection, not just its effect

A chaos run that only watches the application can pass for the wrong reason:
if the application never touched the dependency, removing the dependency
changes nothing and the hypothesis "holds" vacuously.

So every tick also probes the IdP's discovery document from **inside the atlas
container's network namespace**, via a sidecar started with
`--network container:<atlas>`. That sidecar shares atlas's DNS resolver, its
routes, and its network attachments, so the probe's view of the IdP _is_
atlas's view of the IdP. (atlas's own image is distroless — no shell, no curl —
which is why the measurement is taken from a sidecar rather than from inside
the process's own container.)

This turned out to be the difference between a real result and a plausible
one. Given that the new-login surface never moved, the discovery witness is
the only thing standing between "the IdP outage was survived" and "the IdP
outage never happened". It flipped 200 → 000 in all three arms and back on
recovery. Without it, this run would be an anecdote.

## D2 — Shortening the key-rotation cadence, and why that is not tuning

The design's third check is "key-rotation cron: continues, no error log". The
shipped default rotation interval is **annual**. A 25-minute run against an
unmodified stack would observe zero rotations and report zero errors, and the
check would pass without ever having fired.

The overlay sets `ATLAS_KEY_ROTATION_INTERVAL=60s`. This is a shipped operator
knob read by `keyRotationInterval()`, not a code change and not a test hook —
the same knob a self-hoster would use. Gate C-4 then _observes_ a rotation
before injection and refuses to proceed if none fires within 150s.

The distinction that matters: this makes the check **harder**, not easier. It
converts "no errors observed in a window where nothing ran" into "five
rotations ran under a network outage and none errored". Tuning a parameter to
make a run look better would be a design violation; tuning one to make a
vacuous check real is the opposite.

## D3 — Why gate C-5 records VACUOUS rather than FAIL

C-5 discovered, before injection, that `/auth/oidc/login` 400s while the IdP is
demonstrably up. The obvious response is to FAIL the checklist and refuse to
inject into a stack whose RP is not wired.

That would have been the wrong call, and the gate is deliberately not a FAIL:

- **A refusal produces no measurements at all.** Two of the design's three
  checks — existing JWT survives, key rotation continues — are fully live on
  this deployment and are the clauses carrying the design's actual resilience
  claim. Refusing to run would have discarded both to protest the third.
- **"The RP never contacts the IdP" is not an unverified stack; it is a
  finding about the stack.** It is the most useful thing this run produced.
  A FAIL would have filed it as an operator problem to fix before re-running,
  when it is in fact a platform gap to report.

So the gate records VACUOUS, the verdict carries that state through to every
arm's report, and the null result is the headline rather than a silent zero
buried in a table. The distinction between "not satisfied", "violated", and
"unexercisable" is load-bearing in a chaos result, and the tooling is built to
keep the third from being mistaken for either of the first two.

## D4 — Why a third arm, and what it actually settled

Arm C was designed to defeat the RP's provider cache. `Authenticator.provider()`
caches `*coreos.Provider` per issuer with no TTL and no invalidation, so the
leading hypothesis for the unchanged 400s was a warm cache: discovery happened
once at some earlier point, and `BeginLogin` never re-contacted the IdP
afterwards. Detaching the IdP first and _then_ restarting atlas produces a cold
cache, which would distinguish the two explanations.

Reading further up the call chain showed the cache is not even reached —
`BeginLogin` calls `resolver.ResolveIdp` _before_ `a.provider()`, and the
stub resolver returns `ErrUnknownIdp` first. So the answer was available from
the source.

Arm C was kept anyway, for two things a code reading cannot do:

1. **It settles the question empirically rather than by assertion.** If the
   400s were a warm-cache artifact they would change under a cold cache; if
   they are the resolver stub they will not. They did not. A run that reads
   the source and asserts the answer is weaker evidence than one that tests
   it — and the cache finding survives on its own merits as G-3, applying to
   any deployment whose resolver _does_ work.
2. **It puts the design's central claim under a harder perturbation than the
   design asked for.** Arm C's JWT outlived the process that minted it, and
   still returned 200 on `/v1/me` after the restart (`jwt_after_prepare=200`).
   That was worth measuring rather than assuming: slice 356b found an
   in-memory credential store that did **not** survive a restart, so "the
   keystore is on a volume" is not an assumption available for free in this
   codebase. Here it held — the keystore volume behaves as the design's blast
   radius says it does.

## D5 — Two mechanisms, two failure shapes, and why both were run

The design's Variable field names two mechanisms and treats them as
alternatives:

> Network egress from atlas to IdP URL, perturbed via
> `iptables -A OUTPUT -d <idp-ip> -j DROP` (or via docker-compose network
> isolation: detach atlas from the network that reaches the simulated IdP
> container).

They do not fail the same way, and the run measured the difference:

| Mechanism         | Discovery probe latency    | Failure shape                                         |
| ----------------- | -------------------------- | ----------------------------------------------------- |
| Arm A — detach    | 2.2ms mean / 5ms max       | fast **name-resolution** error                        |
| Arm B — blackhole | 3004.8ms mean / 3011ms max | **connect timeout**, pinned at the probe's 3s ceiling |

Detaching the container removes its docker-DNS entry, so the call dies at
resolution in ~2ms. Dropping egress leaves DNS intact, so the call hangs until
something times out. The design's second abort criterion — "atlas crashes on
IdP-unreachable (signals a **missing timeout** on the OIDC discovery refresh)"
— is specifically about the hang, and a detach-only run cannot speak to it at
all. Running only the parenthetical mechanism would have left the design's own
abort criterion untested while appearing to satisfy it.

Neither arm is a redesign: both are the design's own named mechanisms, and no
field of the experiment was altered by running both. Slice 335 owns the
design; the arms are initial conditions.

## D6 — On the slice-332 baseline

Slice 357's instruction is to use slice 332's performance audit for the
steady-state baseline, and to re-derive rather than assume if the baseline has
visibly shifted.

Slice 332 publishes no end-to-end latency figure for `/v1/me`. What it carries
that is relevant is the auth-path cost from slice 188 D-Argon-1: **ES256
verify ~1–2ms**, with the note that signature verification is the slower half
and that Argon2id's ~150ms is a password-verification cost on the login path,
not on the bearer-token path this experiment probes.

Measured here: `/v1/me` at 8.3ms mean in steady state, against a 6ms harness
floor for a bare loopback `/health` round trip through the identical construct
(gate H-1). That leaves ~2ms for verification plus handler work, which sits on
the published 1–2ms ES256 figure. **No visible shift, so no re-derivation was
warranted** — and the check that actually matters for this experiment is not
the absolute number but the delta, which was zero: 8.3ms steady against
7.8 / 7.1 / 6.9ms under injection. The verify path does not touch the IdP, and
the measurement says so.

The steady state is captured in-run rather than inherited regardless. A
baseline from a different machine on a different date is a sanity check, not a
control; the 300s window captured immediately before injection, on the same
stack, through the same probes, is the control.

## D7 — Three recovery clocks, kept apart on purpose

`measure_recovery()` tracks three separate times rather than one:

| Clock               | Question it answers                                      | Arm A | Arm B | Arm C |
| ------------------- | -------------------------------------------------------- | ----- | ----- | ----- |
| `idp_recovered_s`   | when is the IdP reachable from atlas's netns again       | 1s    | 0s    | 1s    |
| `authz_recovered_s` | when is `/oauth/authorize` back to its steady-state code | 1s    | 0s    | 1s    |
| `login_recovered_s` | when is `/auth/oidc/login` back to its steady-state code | 1s    | 0s    | 1s    |

Collapsing these into one "recovery time" would have produced the single most
misleading number this run could have emitted: **"new logins recovered in
under 1 second, well inside the design's 30s expectation."** That reads as an
excellent result. It is actually a surface that never degraded, "recovering"
instantly to a state it never left.

Keeping the IdP clock separate from the two application clocks is what makes
the vacuity visible: the IdP genuinely went away and came back (the discovery
witness proves it), while the login surfaces held a constant 400 throughout.
Same numbers, opposite meanings, and only the split reporting distinguishes
them.

This generalizes past this experiment. Any chaos run that reports one recovery
time for a dependency and its dependents can hide a dependent that never
depended.

---

## Resilience gaps and follow-ups filed (slice 357 AC-19)

| ID  | Gap                                                                                                                                                       | Severity | Follow-up      |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | -------------- |
| G-1 | The docker-compose bundle wires a stub OIDC resolver that fails every login, while `PATCH /v1/admin/sso` returns 200 and persists the config              | high     | OPENENGINE-438 |
| G-2 | No IdP-unavailable degradation path exists: unreachable and unconfigured are the same 400; the design's `auth_provider_unavailable` 503 is unimplemented  | medium   | OPENENGINE-439 |
| G-3 | The OIDC provider cache has no TTL and no invalidation, so the "30s discovery refresh interval" the design's recovery expectation rests on does not exist | medium   | OPENENGINE-440 |

**G-1** is the significant finding, and its severity is about the lie rather
than the outage. An operator configures SSO through the shipped admin surface,
receives a 200, sees the row persisted — and every login attempt returns 400
with an error naming an "unknown IdP" they just successfully configured. There
is no signal anywhere in that loop that the deployment cannot serve OIDC at
all. For a product whose customers will diligence the diligence tool, an auth
surface that reports success and does nothing is worse than one that reports
failure honestly.

**G-2** is filed separately and lower, because it is a real contract
divergence on its own merits — a deployment with a working resolver still
could not tell an operator "the IdP is down, retry in 30s" — but closing it
alone would leave G-1's login path dead. The OE body says so explicitly.

**G-3** matters only once G-1 is fixed, which is exactly why it is filed now
rather than rediscovered later: a resolver fix would make the login path live
and would _not_, on its own, make an IdP outage observable to it. The provider
is cached at first discovery and never re-fetched, so the first successful
discovery pins the deployment's view of the IdP until restart. Whoever closes
G-1 should read G-3 before declaring the RP resilient.

All three are filed as children of OPENENGINE-386.

None of the three is a falsification of the design's resilience claim. The
claim — atlas-as-issuer is independent of atlas-as-RP — held under everything
this experiment could throw at it. The gaps are all on the RP side, which is
the side the design predicted would degrade, and which turns out not to be
wired to degrade from.

---

## Cross-references

- **Slice 335** (`docs/audits/335-chaos-experiment-design.md` §Experiment 4) —
  the design contract this slice executes without redesign.
- **Slice 357** (`docs/issues/357-auth-substrate-chaos-round-1.md`) — the
  bundled execution slice. Experiments 6 (cosign) and 8 (OPA, high-risk,
  additional-reviewer-gated) remain unexecuted and are not touched here.
- **Slice 356b** (`docs/audit-log/356b-atlas-restart-mid-push-chaos-decisions.md`)
  — Experiment 5. Its in-memory-credstore finding is why arm C measured the
  post-restart JWT rather than assuming the keystore volume behaved.
- **Slice 187+ / ADR-0003** (`docs/adr/0003-oauth-authorization-server.md`) —
  the auth substrate under test; the AS-as-issuer vs AS-as-RP split whose
  independence this experiment confirms.
- **Slice 332** (`docs/audits/332-performance-audit-report.md`) — the
  steady-state baseline; see D6.
- **Slice 365** (`docs/audit-log/365-oidc-nonce-validation-decisions.md`) and
  **slice 366** (`docs/audit-log/366-jwt-key-rotation-decisions.md`) — the
  nonce and rotation surfaces this run exercised.
- **Slice 062** — `PATCH /v1/admin/sso`, the operator surface G-1 is about.
