# Slice 003 — Evidence SDK: proto + Go push client + CLI

Copy-paste prompt for executing slice 003. See `Plans/prompts/04-per-slice-template.md` for the template that generated this file.

---

```
Build docs/issues/003-evidence-sdk-proto-push-client-cli.md.

Branch: spine/003-evidence-sdk-proto-push-client-cli (from main).

Workflow:
1. Read CLAUDE.md constitutional invariants + the issue's "Constitutional invariants honored" + "Canvas references" sections
2. grill-with-docs the issue against the cited canvas section(s) — flag drift before coding
3. tdd loop per acceptance criterion (integration > unit; never mock the DB; never test private methods)
4. database-designer for any migration (idempotent + reversible)
5. security-review on any PR touching auth, ingest, RLS queries, or external IO
6. simplify pass before opening PR
7. ship-gate must pass before claiming done
8. changelog-generator entry for the slice

Honor every anti-criterion (P0 — block merge).

Respect CLAUDE.md style (no emojis, Conventional Commits, Co-Authored-By trailer).

Open PR titled "feat(spine): Evidence SDK: proto + Go push client + CLI (#003)" with body: AC pass/fail · files changed · CI URL · open questions surfaced.

Use Algorithm mode. Initialize a PRD (id: 003-evidence-sdk-proto-push-client-cli).
```

---

**Quick reference for this slice:**

- Issue file: [`docs/issues/003-evidence-sdk-proto-push-client-cli.md`](../../../docs/issues/003-evidence-sdk-proto-push-client-cli.md)
- Cluster: `spine`
- Branch: `spine/003-evidence-sdk-proto-push-client-cli`
- PRD id: `003-evidence-sdk-proto-push-client-cli`
