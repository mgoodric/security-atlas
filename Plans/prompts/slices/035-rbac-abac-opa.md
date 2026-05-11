# Slice 035 — RBAC roles (5) + ABAC via OPA embedded library

Copy-paste prompt for executing slice 035. See `Plans/prompts/04-per-slice-template.md` for the template that generated this file.

---

```
Build docs/issues/035-rbac-abac-opa.md.

Branch: auth/035-rbac-abac-opa (from main).

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

Open PR titled "feat(auth): RBAC roles (5) + ABAC via OPA embedded library (#035)" with body: AC pass/fail · files changed · CI URL · open questions surfaced.

Use Algorithm mode. Initialize a PRD (id: 035-rbac-abac-opa).
```

---

**Quick reference for this slice:**

- Issue file: [`docs/issues/035-rbac-abac-opa.md`](../../../docs/issues/035-rbac-abac-opa.md)
- Cluster: `auth`
- Branch: `auth/035-rbac-abac-opa`
- PRD id: `035-rbac-abac-opa`
