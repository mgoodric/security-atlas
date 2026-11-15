# ADR 0025 — Module Access Is Separate From Roles; Switches Never Filter the Ledger or Audit-Binding Output; 404 Means Switched Off, 403 Means No Access

**Status:** Accepted

**Date:** 2026-11-14

**Decides:** how a member's module access relates to their role, what module switches and module access are allowed to hide, and how the API tells a switched-off module from one the caller has no access to.

**Spec:** [#2166 — module switches per tenant and module access per member](https://github.com/mgoodric/security-atlas/issues/2166). Ticket: [#2169](https://github.com/mgoodric/security-atlas/issues/2169). Vocabulary (module, core module, optional module, feature, switched off, module access, no access) is in `GLOSSARY.md` (PR #2123).

**Numbering:** #2166 and #2169 name this record "ADR 0024". That number was taken by [ADR 0024](0024-approvals-require-a-second-person.md) (#2101) before this landed, so this is ADR 0025.

**Claims:** [`isa/modules.md`](../../isa/modules.md).

**Shipped in this change (docs only):** this ADR and the modules epic. The behaviour lands in the build tickets under #2166.

---

## Context

Each tenant chooses which **optional modules** are switched on (Risk, Third-party risk, Policies, Questionnaires, Personnel security, Exceptions, Board reporting). **Core modules** (Controls, Evidence, Frameworks and the SCF catalog, Scopes and framework scopes, Audit periods, Dashboard, Settings & admin) are always on. Admins can also narrow a member's **module access** to a subset of the switched-on modules.

Three questions shape every build ticket under #2166, and each has an obvious answer that is wrong:

- Roles already exist, so module access could be expressed as more roles.
- Hiding a module could mean hiding its rows everywhere, the evidence ledger included.
- A caller who cannot see a module could get the same response whichever reason applies.

The existing feature gate (`internal/featureflag/enabled.go`) already answers a switched-off flag with `404`, an `X-Feature-Disabled: <key>` header and the body `{"error":"feature disabled"}`, pinned by `enabled_test.go` and reused by #2092 and #2267.

## Decision

1. **Module access is kept separate from roles.** The role says _what_ a member can do; module access says _where_. Module access is stored per tenant membership as either "all" (a wildcard that includes modules switched on later) or an explicit set of optional-module keys. It applies only to `grc_engineer`, `control_owner` and `viewer`: `admin` always has every switched-on module, and `auditor` is scoped by audit periods instead. The authorization middleware resolves the route's module first and falls through to the unchanged OPA role decision only when the module is on and in the caller's access.
2. **Module switches and module access never filter the evidence ledger, control evaluation, audit-binding output or saved snapshots.** They control what people see on screen and in the API. Evidence written by an optional module (policy acknowledgments, personnel-security completions) stays visible under Evidence and still counts toward control results (invariant 2, C2). OSCAL SSP and POA&M output and audit-period evidence summaries include all the data that exists, whatever is switched on. A board pack already generated keeps every section it had; only new packs leave out sections for switched-off modules. Switching a module off keeps its data, and switching it back on restores it.
3. **404 with the switched-off marker means the module is switched off; 403 naming the module means no access.** Switched off reuses the existing feature-gate marker unchanged: `404`, `X-Feature-Disabled: <module key>`, body `{"error":"feature disabled"}`. The module key is the optional module's flag key (for example `vendor.enabled`), so a client that already handles the feature gate handles a switched-off module with no change. No access is a `403` whose body names the module. Switched off is checked first: a module that is off for the tenant is off for every caller, whatever their access.

## Alternatives considered

| Alternative                                                                       | Why rejected                                                                                                                                                                                                                                                                                                |
| --------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Express module access as roles (for example a "policy editor" or "vendor viewer") | Multiplies roles by modules, and every new module would add roles to every policy and IdP group mapping. The OPA role decision would have to learn about modules, and an admin narrowing access would have to change a member's role, which also changes what they can do. Two independent axes stay two.   |
| Filter the evidence ledger, evaluation and audit-binding output by module         | A display setting would change control results and what an auditor receives. Evidence from a switched-off module would stop counting, past dates would no longer replay (C2, ISA ISC-3), and a board pack or OSCAL export would differ depending on when someone flipped a switch. Snapshots must not move. |
| One status code for both cases (404 for both, or 403 for both)                    | A client, the web app and an MCP agent could not tell "ask an administrator for access" from "this isn't turned on for your organisation", and #2166 shows them different messages and different next steps. A new 404 shape for modules would also fork the marker the feature gate already ships.         |

## Consequences

- One module registry on the server owns which routes, pages and features belong to each module, and every server route belongs to exactly one module, guarded by a test that fails on a route with none. The per-route `Gate` wrapping becomes redundant once every route is in the registry.
- The web app reads the caller's effective modules from one `/v1/me` endpoint, and its hand-kept gating map is deleted.
- Combined views (Upcoming, Calendar and ICS, Activity, search, dashboard export, metrics) filter by the caller's effective modules on the server. Evidence is a core module, so its listing is never filtered by optional-module switches.
- Background notification jobs skip switched-off modules and recipients without access; data upkeep keeps running; MCP proposals aimed at a switched-off module stay pending and cannot be applied.
- Losing access never reassigns records; the owner-can't-access flag surfaces it instead.
