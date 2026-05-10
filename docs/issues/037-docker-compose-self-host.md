# 037 — docker-compose self-host bundle

**Cluster:** Infra / deploy
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Build the docker-compose self-host bundle that meets the 4-hour-to-first-evidence acceptance criterion. One `docker-compose.yml` brings up: Postgres 16, NATS JetStream, MinIO (S3-compatible local), the `atlas` server, the AWS connector example, and the Next.js frontend. Environment variables documented in `.env.example`. The bundle includes seeded defaults: a default tenant, a default single-cell scope, the SCF catalog imported, the 50 stock controls. The slice delivers value because acceptance criterion #7 ("installable, seeded, producing first evidence within 4 hours") becomes testable.

## Acceptance criteria

- [ ] AC-1: `docker compose up -d` on a fresh checkout brings the platform online in under 5 minutes on a mid-size VM
- [ ] AC-2: Health check at `http://localhost:8080/health` returns 200 within 3 minutes of startup
- [ ] AC-3: Web UI accessible at `http://localhost:3000`; default user can sign in (local mode)
- [ ] AC-4: SCF catalog already seeded; 50 SOC 2 controls visible in catalog
- [ ] AC-5: AWS connector example shows how to add credentials and trigger first pull — produces first evidence record
- [ ] AC-6: `docker compose down -v` cleanly removes everything (volumes included)
- [ ] AC-7: 4-hour to first evidence walkthrough documented in `docs/getting-started/first-evidence.md`

## Constitutional invariants honored

- **Replacement-grade criterion 7 ("installable + first evidence in 4h"):** this slice is the integration test
- **Tech-stack lock:** single VM target, no required Kafka, no required ClickHouse

## Canvas references

- `Plans/canvas/09-tech-stack.md` (deployment unit)
- `Plans/canvas/10-roadmap.md` §10.1 (Self-host row)
- `Plans/canvas/01-vision.md` §1.5 (acceptance criteria #7)

## Dependencies

- #002, #013, #034

## Anti-criteria (P0)

- Does NOT require an external IdP for first sign-in (local mode must work)
- Does NOT require external S3 (MinIO bundled)
- Does NOT exceed 5 minutes for initial bring-up

## Skill mix (3–5)

- Docker + docker-compose
- MinIO local config
- Health-check orchestration
- Seeded migrations
- Documentation (getting-started)
