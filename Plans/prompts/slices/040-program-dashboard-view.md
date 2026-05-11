# Slice 040 — Program dashboard view

Copy-paste prompt for executing slice 040. See `Plans/prompts/04-per-slice-template.md` for the template that generated this file.

---

```
Build docs/issues/040-program-dashboard-view.md.

Branch: frontend/040-program-dashboard-view (from main).

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

Open PR titled "feat(frontend): Program dashboard view (#040)" with body: AC pass/fail · files changed · CI URL · open questions surfaced.

Use Algorithm mode. Initialize a PRD (id: 040-program-dashboard-view).
```

---

**Quick reference for this slice:**

- Issue file: [`docs/issues/040-program-dashboard-view.md`](../../../docs/issues/040-program-dashboard-view.md)
- Cluster: `frontend`
- Branch: `frontend/040-program-dashboard-view`
- PRD id: `040-program-dashboard-view`
