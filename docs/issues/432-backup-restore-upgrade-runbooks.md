# 432 — Operator backup/restore + upgrade runbooks, surfaced in the docs site

**Cluster:** Docs
**Estimate:** M
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

**Why.** There are two unrelated documents that look like they cover continuity but do not give an operator a runnable backup/restore procedure:

- Slice 373 shipped the **BCP/DR plan** (`docs/governance/business-continuity.md`) — a governance artifact (RTO/RPO tiers, role devolution, restore scenarios) aimed at diligence reviewers, not a hands-on operator runbook.
- The user-facing `docs/SELF_HOSTING.md` "Backups" section is **one `pg_dump` line + a MinIO note** with **no restore procedure** and **no verify-the-restore drill**. An operator who runs the `pg_dump` has a backup file and no documented way to prove it restores.

Worse, `docs/SELF_HOSTING.md` — the canonical self-host guide — is **not in `docs-site/mkdocs.yml` nav**, so the published documentation site never surfaces it. There are effectively two homes for operator docs (the in-repo `docs/SELF_HOSTING.md` and the published `docs-site/`), and they have drifted.

**What.** Three deliverables, all surfaced in the published nav:

1. `docs-site/docs/backup-restore.md` — a runbook covering Postgres dump **and restore**, MinIO `mc mirror` backup **and restore**, handling of the bootstrap token + signing keys, and a **tested restore drill** (restore into a scratch stack, confirm `/health` + a known evidence record + an OSCAL export verify). Backup **encryption** and **key handling** are first-class (threat model below).
2. `docs-site/docs/upgrade.md` — pin a version, take a pre-upgrade backup checkpoint, run `migrate up`, verify, and roll back. Reconciles the migration guidance already scattered in `SELF_HOSTING.md` (the `ATLAS_MIGRATE_ON_START` / manual-migrate discussion).
3. Resolve the **two-homes drift**: surface `SELF_HOSTING.md` content into the published nav (either add it to `mkdocs.yml` nav, or port/redirect its operator content into `docs-site/` and make `SELF_HOSTING.md` point at the canonical home). Pick one and document the choice.

**Scope discipline.** Documentation + the restore-drill procedure (which is runbook content, not new code). It does NOT add a backup tool, a scheduler, or an automated backup feature (that would be a code slice). It does NOT modify the BCP/DR governance plan (slice 373 owns that; this runbook **operationalizes** it and cross-links). It resolves the docs-home drift for operator content only, not the whole `docs/` tree.

## Threat model

Docs slice STRIDE pass — load-bearing, because **backups contain the crown jewels**: tenant evidence (confidential customer data) and signing keys (`OSCAL_SIGNING_KEY`, the OAuth keystore). A backup runbook that addresses only the mechanics and ignores encryption/key-handling teaches operators to create an unencrypted copy of the most sensitive data in the system.

**S — Spoofing.** N/A (docs). The runbook notes that a restored deployment must re-establish its bootstrap credential posture (rotate `ATLAS_BOOTSTRAP_TOKEN` after a restore, as on first boot).

**T — Tampering (load-bearing).** A restore is only trustworthy if the backup is intact. The drill MUST include an integrity confirmation: after restore, the evidence ledger's sha256 per-record property holds (`atlas evidence verify`) and a signed OSCAL export still verifies. _Threat:_ a silently-corrupt backup that restores "successfully" but with damaged evidence. _Mitigation:_ the drill verifies, not just restores.

**R — Repudiation.** The runbook notes that restore events should be recorded (operationally) so the audit trail reflects a continuity event.

**I — Information disclosure (load-bearing).** _Threat:_ an unencrypted `pg_dump` / MinIO mirror sitting in an S3 bucket or on disk is a full copy of tenant evidence — a breach waiting to happen. _Mitigation:_ the runbook MUST document backup **encryption at rest** (encrypt the dump before it leaves the host / use SSE on the destination bucket) and access-control on the backup destination. The signing keys (`OSCAL_SIGNING_KEY`, OAuth keystore) MUST be backed up **separately** with stricter handling than the data dump — losing them breaks export verification, leaking them breaks signing integrity. _Anti-criterion:_ the runbook MUST NOT show a plaintext backup landing in a shared/public location.

**D — Denial of service.** N/A for the docs themselves. The runbook notes Postgres is deliberately NOT auto-updated by Watchtower (major version upgrades need manual dump+restore — already in `SELF_HOSTING.md`), preventing an auto-update from bricking the DB.

**E — Elevation of privilege.** The restore drill uses the `atlas_migrate` role for schema restore and `atlas_app` for the running server — the runbook keeps that role separation (it must not tell operators to restore as a superuser into the running app's connection).

## Acceptance criteria

- [ ] **AC-1.** New file `docs-site/docs/backup-restore.md` exists with a Postgres backup **and** restore procedure (dump + restore commands, role-correct).
- [ ] **AC-2.** The page documents MinIO / S3 artifact-store backup **and** restore via `mc mirror` (both directions).
- [ ] **AC-3.** The page documents handling of the bootstrap token and the signing keys (`OSCAL_SIGNING_KEY`, OAuth keystore): backed up separately, stricter access control, rotate-after-restore for the bootstrap token.
- [ ] **AC-4.** The page documents backup **encryption at rest** (encrypt the dump and/or SSE the destination) and access-control on the backup destination — not just the dump mechanics (threat-model I).
- [ ] **AC-5.** The page includes a **tested restore drill**: restore into a scratch stack, confirm `/health`, confirm a known evidence record is present, and confirm a signed OSCAL export still verifies (`atlas evidence verify` + `atlas oscal verify`) — integrity, not just presence (threat-model T).
- [ ] **AC-6.** New file `docs-site/docs/upgrade.md` exists: pin a version, pre-upgrade backup checkpoint, `migrate up`, verify, rollback.
- [ ] **AC-7.** The upgrade page reconciles the migration guidance (`ATLAS_MIGRATE_ON_START` off for production + manual `migrate up`) currently in `SELF_HOSTING.md`, without contradicting it.
- [ ] **AC-8.** The two-homes drift is resolved: either `SELF_HOSTING.md` is added to `docs-site/mkdocs.yml` nav, OR its operator content is ported into `docs-site/` with `SELF_HOSTING.md` pointing at the canonical home. The chosen approach is documented and applied consistently.
- [ ] **AC-9.** Both new pages have nav entries in `docs-site/mkdocs.yml`.
- [ ] **AC-10.** Both new pages cross-link to the BCP/DR governance plan (`docs/governance/business-continuity.md`) — the runbook operationalizes the plan; it does not restate it.
- [ ] **AC-11.** The restore drill uses role-correct connections (`atlas_migrate` for schema restore; not a superuser into the app connection) (threat-model E).
- [ ] **AC-12.** All example bucket names, hosts, and credentials are placeholders.
- [ ] **AC-13.** `mkdocs build --strict` passes from `docs-site/` (new pages wired in, links resolve, drift-resolution does not break the build).
- [ ] **AC-14.** The restore drill is actually run once against a scratch stack (dump → wipe → restore → verify) and the outcome is recorded in the decisions log — the drill is tested, not just written.

## Constitutional invariants honored

- **Append-only evidence ledger + point-in-time replay** (#2) — the restore drill leans on the ledger's sha256-per-record integrity property to confirm a restore is faithful.
- **Evidence integrity** (tech stack) — the drill verifies a signed OSCAL export post-restore, exercising the cosign/ed25519 verification path.
- **Tenant isolation via RLS** (#6) — the runbook keeps the `atlas_migrate` / `atlas_app` role separation through the restore.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Postgres, S3-compatible object storage, evidence integrity.
- `Plans/canvas/04-evidence-engine.md` §4.3 — append-only ledger / replay (what the drill verifies).
- `docs/governance/business-continuity.md` (slice 373) — the BCP/DR plan this runbook operationalizes.

## Dependencies

- **#037** (docker-compose self-host bundle) — `merged`. The stack the drill restores into.
- **#373** (BCP/DR plan) — `merged`. The governance plan the runbook operationalizes + cross-links.
- **#413** (OSCAL signing) — `merged`. The signing-key handling + post-restore export-verify the drill exercises.

## Anti-criteria (P0 — block merge)

- **P0-432-1.** Does NOT document a backup procedure without restore + a tested restore drill — restore and verify are the load-bearing half (AC-1/AC-2/AC-5).
- **P0-432-2.** Does NOT show a plaintext backup landing in a shared/public location; encryption-at-rest + access control are documented (threat-model I).
- **P0-432-3.** Does NOT treat signing keys as ordinary data — they are backed up separately with stricter handling (AC-3).
- **P0-432-4.** Does NOT add a backup tool / scheduler / automated feature — runbook content only (that would be a separate code slice).
- **P0-432-5.** Does NOT restate or fork the slice-373 BCP/DR plan — it operationalizes and cross-links it.
- **P0-432-6.** Does NOT leave the two-homes drift unresolved — AC-8 requires one canonical operator-docs home.
- **P0-432-7.** Does NOT include real bucket names / hosts / credentials — placeholders only.

## Skill mix (3-5)

- `grill-with-docs` — align the runbook against `SELF_HOSTING.md`, the slice-373 plan, and the docker-compose bundle.
- `Security` — verify the encryption / key-handling guidance is sound (backups = crown jewels).
- `verify` — run the restore drill against a scratch stack (AC-14).
- `runbook-generator` — structure the restore + upgrade procedures as runnable, ordered steps.
- `simplify` — keep each runbook to a single linear procedure.

## Notes for the implementing agent

- The existing `SELF_HOSTING.md` "Backups" section (the `pg_dump` line + MinIO note + the existing "Bootstrap credential — rotate it" callout) and the "Database migrations across upgrades" section are the seed content — the runbook **completes** the restore half they omit, not duplicates them.
- The drill (AC-5/AC-14) maps cleanly onto the existing `just` recipes: `self-host-up` → seed → `pg_dump` → `self-host-wipe` → bring up fresh → restore → `curl /health` → `atlas evidence verify` → `atlas oscal verify`. Use the slice-037 bundle so the drill is reproducible by any operator.
- Two-homes resolution recommendation: the lighter touch is to **add `SELF_HOSTING.md` to `mkdocs.yml` nav** (or symlink/include) so the canonical guide reaches the published site, and put the _new_ backup-restore/upgrade depth in `docs-site/`. Avoid a full content migration in this slice unless the build forces it — record the decision either way. Watch for the mkdocs `docs_dir: docs` constraint: `docs-site/mkdocs.yml` uses `docs_dir: docs` (i.e. `docs-site/docs/`), so a top-level `docs/SELF_HOSTING.md` is outside the docs root — surfacing it may require a copy/include step rather than a bare nav path. Resolve this concretely (it affects AC-8) and note it.
- Signing-key handling cross-references the OSCAL signing runbook (`docs/runbooks/oscal-signing.md`): `OSCAL_SIGNING_KEY` (embedded mode) is a secret whose loss makes prior `embedded-ed25519` exports unverifiable against a stable identity; for `cosign-kms` the key lives in the KMS (back up the KMS, not a local key). Document both cases.
- Detection-tier: a restore-drill that does not actually work is `target=manual_review` / `actual=manual_review` (AC-14 catches it before merge). Note in decisions log.
