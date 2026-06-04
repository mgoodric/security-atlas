# 429 — README parity for the AWS connector and the Go/TypeScript SDKs

**Cluster:** Docs
**Estimate:** S
**Type:** JUDGMENT
**Status:** `merged` (`74ca43be`, #979 — AWS/Go/TS SDK READMEs)

## Narrative

**Why.** Three of the highest-traffic OSS extension surfaces have conspicuous README gaps:

- **`connectors/aws/`** is the flagship connector — slice 004, the first connector built, and the canonical worked example the connector-authoring guide links to repeatedly. Yet every other connector (`okta`, `github`, `jira`, `1password`, `osquery`, `manual`) ships a `README.md` and AWS is the **only** one without. A contributor who opens the connector-authoring guide, follows the link to "see how AWS does it", and lands on a directory with no README gets the worst first impression of exactly the surface the project most wants people to extend.
- **`pkg/sdk-go/`** (the Go push SDK) and **`sdk/typescript/`** (`@security-atlas/sdk`) have no README, while `sdk/python/` and `sdk/java/` both do. The Go SDK is the one the platform itself dogfoods; the TypeScript SDK is the one the frontend/Node ecosystem reaches for first. Both are missing the push-record quickstart that the Python README already provides.

**What.** Three new READMEs:

1. `connectors/aws/README.md` — match the shape of `connectors/okta/README.md`: the evidence_kinds emitted (with profile + source), the least-privilege IAM read-only role + STS AssumeRole posture, `register` / `run` subcommands, scope minimums, and a P0 anti-criteria block mirroring okta's.
2. `pkg/sdk-go/README.md` — a push-record quickstart for the Go SDK (construct client, build an evidence record, `Push`, handle the `Receipt`), mirroring `sdk/python/README.md`.
3. `sdk/typescript/README.md` — the equivalent push-record quickstart for `@security-atlas/sdk`.

**Scope discipline.** Documentation-only. No code changes to the connector or either SDK. The READMEs describe the **shipped** surface — the actual evidence_kinds, the actual subcommands, the actual SDK API — verified against the source, not aspirational. This slice closes the README-parity gap; it does not add connector features or SDK methods.

## Threat model

Docs slice STRIDE pass. The load-bearing threat for a connector/SDK README is **recommending an insecure default** — the canonical example being an over-broad IAM role. The AWS connector README, if it tells contributors to attach an admin or write-capable policy, would propagate a real security misconfiguration into every deployment that follows it.

**S — Spoofing.** N/A (docs). The AWS README documents how the connector authenticates (vendor-native AWS credential chain / AssumeRole); it must not suggest embedding long-lived keys in source.

**T — Tampering.** N/A (read-only docs).

**R — Repudiation.** N/A.

**I — Information disclosure (load-bearing).** The AWS README MUST NOT contain a real AWS account ID, ARN, access key, or secret. All identifiers are placeholders. _Anti-criterion enforces this._ The SDK READMEs MUST NOT show a real platform endpoint or a real bearer/API token — placeholders only.

**D — Denial of service.** N/A.

**E — Elevation of privilege (load-bearing).** The AWS README MUST document a **read-only, least-privilege** IAM posture — a read-only role assumed via STS, scoped to exactly the services whose evidence the connector emits, mirroring the okta README's "banned admin roles" discipline. _Threat:_ a README that says "attach `AdministratorAccess`" or "use root credentials" would be a real elevation-of-privilege recommendation. _Mitigation:_ the IAM section enumerates only the read-only actions the connector actually calls; an anti-criterion rejects any write/delete/admin action in the documented policy.

## Acceptance criteria

- [ ] **AC-1.** `connectors/aws/README.md` exists and matches the structural shape of `connectors/okta/README.md` (evidence-kinds table · Auth/least-privilege section · Subcommands · scope/rate notes · Anti-criteria · Tests).
- [ ] **AC-2.** The AWS README's evidence-kinds table lists the **actual** kinds the connector emits, each with its profile (`pull`/`subscribe`/`push`) and source API — verified against `connectors/aws/` source, not invented.
- [ ] **AC-3.** The AWS README documents a least-privilege **read-only** IAM role assumed via STS AssumeRole, enumerating only read actions for the services the connector calls.
- [ ] **AC-4.** The AWS README's IAM section contains no write/delete/admin action and no real account ID / ARN / access key (placeholders only).
- [ ] **AC-5.** The AWS README documents the `register` and `run` subcommands with their flags, matching the okta README's subcommand pattern, verified against the connector's CLI.
- [ ] **AC-6.** The AWS README documents scope minimums (the minimum scope fields the connector sets on emitted records), consistent with the connector-pattern conventions slice 004 established.
- [ ] **AC-7.** `pkg/sdk-go/README.md` exists with a push-record quickstart: construct a client, build an evidence record, call `Push`, handle the `Receipt` — using the actual public API of `pkg/sdk-go/client.go`.
- [ ] **AC-8.** `sdk/typescript/README.md` exists with the equivalent push-record quickstart for `@security-atlas/sdk`, using the actual published TS API.
- [ ] **AC-9.** All three READMEs use placeholder endpoints/tokens/identifiers — no real platform URL, bearer token, or AWS identifier.
- [ ] **AC-10.** The connector-authoring guide's reference to AWS as the canonical example resolves correctly (the AWS README is the target a contributor lands on); if the guide links a specific path, that path now exists.
- [ ] **AC-11.** The Go SDK README's quickstart code compiles against the current `pkg/sdk-go` API (the code sample is checked, not aspirational — e.g. by pasting it into a scratch `_test` or confirming the symbols exist).
- [ ] **AC-12.** `pre-commit run --files` passes on all three new READMEs.

## Constitutional invariants honored

- **Single canonical inbound API** (#3) — the SDK READMEs document `Push` → `Receipt` as the one canonical wire surface; the AWS README's `profiles_supported` framing matches the "platform-side wire is always push" rule.
- **Closed proprietary connectors are rejected** (anti-pattern) — documenting the AWS connector + SDKs well is in direct service of the OSS-connector thesis.
- **No proprietary collector agents** — the AWS README documents read-only API access (no endpoint agent), consistent with the anti-pattern stance.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — Evidence SDK, connectors, profiles.
- `Plans/EVIDENCE_SDK.md` — the full SDK contract (`Push` / `Receipt`, push profile) the SDK READMEs document.

## Dependencies

- **#003** (Evidence SDK proto + push client + CLI) — `merged`. The `Push`/`Receipt` surface the SDK READMEs document.
- **#004** (AWS connector) — `merged`. The connector the AWS README documents.
- The Python/Java SDK READMEs (`sdk/python/README.md`, `sdk/java/README.md`) and the okta connector README exist on `main` as the shape templates.

## Anti-criteria (P0 — block merge)

- **P0-429-1.** Does NOT document an over-broad IAM policy — read-only, least-privilege only; no write/delete/admin action; no `AdministratorAccess` (threat-model E).
- **P0-429-2.** Does NOT include a real AWS account ID, ARN, access key, secret, platform endpoint, or bearer token — placeholders only (threat-model I).
- **P0-429-3.** Does NOT change any connector or SDK code — README files only.
- **P0-429-4.** Does NOT document evidence_kinds, subcommands, or SDK methods that do not exist in the shipped source (every documented surface is verified against code).
- **P0-429-5.** Does NOT recommend embedding long-lived AWS keys in source; documents the vendor-native credential chain / AssumeRole posture.

## Skill mix (3-5)

- `grill-with-docs` — align the READMEs against the actual connector/SDK source + the okta/python README shapes.
- `Security` — verify the IAM section is least-privilege and leaks no real identifiers.
- `simplify` — keep each README scannable; the okta README is the length target.
- `verify` — confirm the documented evidence_kinds/subcommands/SDK symbols exist (AC-2/AC-5/AC-11).

## Notes for the implementing agent

- Read `connectors/aws/` source to enumerate the **actual** emitted evidence_kinds + their source APIs before writing AC-2's table — do not copy okta's kinds. Slice 004's connector-pattern memory (actor_id format, stable optional fields, observed_at granularity, register-per-run, scope minimums, vendor-native auth) is the convention to reflect.
- The okta README (`connectors/okta/README.md`) is the canonical shape template: evidence-kinds table → Auth/least-privilege table with a "banned roles" subsection → Subcommands → Rate limiting → Anti-criteria → Tests. Mirror it; the AWS analog of "banned admin roles" is "no write/delete IAM actions; no `AdministratorAccess`".
- For the Go SDK README, read `pkg/sdk-go/client.go` + `client_test.go` to get the exact constructor + `Push` signature + `Receipt` shape. The Python README (`sdk/python/README.md`) is the content template — mirror its quickstart structure in Go idiom.
- For the TS SDK README, confirm the published package name (`@security-atlas/sdk`) and the actual exported client/method names from `sdk/typescript/` before writing the quickstart.
- Detection-tier: a documented-symbol-that-does-not-exist bug would be `target=manual_review` (AC-11 hand-check); there is no doc-code-drift CI tier in v1. Note in decisions log.
