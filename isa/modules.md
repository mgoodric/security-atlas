---
phase: specified
progress: 0
---

# Modules

Epic ISA. Inherits the Constraints in `isa/ISA.md` and cannot violate them.

## Problem

A security leader who runs third-party risk, questionnaires or policies in
another tool still sees those areas of security-atlas in the sidebar, on the
dashboard and on the calendar. The flags that exist today do not help: most
module flags are seeded and checked by nothing, Questionnaires and Personnel
security have no flag, and the sidebar hides two entries from a hand-kept list
that has drifted from the server.

A team also cannot limit where a member works. Roles say what someone may do,
but every member sees every area.

Spec: security-atlas#2166. Vocabulary: `GLOSSARY.md` (module, core module,
optional module, feature, switched off, module access, no access).

## Vision

Each tenant switches its optional modules on or off, and admins narrow a
member's module access. One module registry on the server decides what belongs
to each module, and the API, MCP, search and the web app all follow it. What a
person sees changes; the record does not.

## Out of Scope

- Granting module access from identity-provider groups.
- Deleting or exporting a module's data when it is switched off.
- Module access for admins or auditors.
- Record-level permissions inside a module.
- New Incidents or Metrics modules.

## Constraints

- Switches and module access never filter the evidence ledger, control
  evaluation, audit-binding output or saved snapshots (C2, C10, C11;
  ADR 0025 decision 2).
- A switched-off module answers with the existing feature-gate marker
  (`404`, `X-Feature-Disabled`, `{"error":"feature disabled"}`); a second
  switched-off shape is a defect (ADR 0025 decision 3).
- Module access is a separate axis from roles; the OPA role decision is
  unchanged (ADR 0025 decision 1).

## Goal

A tenant switches off Third-party risk and a member is narrowed to Policies
and Questionnaires; afterwards neither sees vendors anywhere in the app or the
API, while Evidence, control results, OSCAL output and every saved board pack
read exactly as they did before.

## Claims

### M1 · The boundary

- [ ] ISC-1: Core modules cannot be switched off. Falsifier: any request, by
      any role including super-admin, that leaves a core module (Controls,
      Evidence, Frameworks and the SCF catalog, Scopes and framework scopes,
      Audit periods, Dashboard, Settings & admin) answering its routes with the
      switched-off marker.
- [ ] ISC-2: Every server route belongs to exactly one module, core or
      optional. Falsifier: a route registered on the server that the module
      registry assigns to no module, or to more than one.

### M2 · Data is kept

- [ ] ISC-3: Switching a module off keeps its data, and switching it back on
      restores it. Falsifier: a row of the module's data, or a feature's stored
      switch value, that differs after an off-then-on cycle from before it.

### M3 · The record does not move

- [ ] ISC-4: Evidence and control evaluation are unaffected by module switches
      and module access. Falsifier: an evidence record, or a control result,
      that differs between a tenant with an optional module switched on and the
      same tenant with it switched off.
- [ ] ISC-5: OSCAL output is unaffected by module switches. Falsifier: an OSCAL
      SSP or POA&M export whose content differs between a module switched on
      and switched off.
- [ ] ISC-6: A saved board pack never changes. Falsifier: a board pack
      generated before a module was switched off whose stored content or
      sections differ afterwards.

### M4 · The two answers

- [ ] ISC-7: A switched-off module answers 404 with the switched-off marker,
      and a module outside the caller's access answers 403 naming the module.
      Falsifier: a route in a switched-off module answering anything other than
      `404` with `X-Feature-Disabled` and `{"error":"feature disabled"}`, or a
      route in a switched-on module outside the caller's access answering
      anything other than `403` with the module named.

## Test Strategy

Every probe below is named before it is written. `isa/check-probes.sh`
reports each as MISSING, which is not a pass: the claim stays open until its
probe exists and has been observed failing and then passing.

| isc   | tier    | type | check                                                                                | threshold | tool                                             |
| ----- | ------- | ---- | ------------------------------------------------------------------------------------ | --------- | ------------------------------------------------ |
| ISC-1 | service | bash | switching off each core module is refused and its routes never return the marker     | 0         | `bash isa/probes/modules-core-always-on.sh`      |
| ISC-2 | pure    | bash | registered routes with zero or several registry modules                              | 0         | `bash isa/probes/modules-route-coverage.sh`      |
| ISC-3 | service | sql  | module rows and feature switch values differ after an off-then-on cycle              | 0         | `bash isa/probes/modules-data-kept.sh`           |
| ISC-4 | service | bash | evidence listings and control results differ with an optional module off             | 0         | `bash isa/probes/modules-evidence-unfiltered.sh` |
| ISC-5 | service | bash | OSCAL SSP and POA&M exports differ with an optional module off                       | 0         | `bash isa/probes/modules-oscal-unfiltered.sh`    |
| ISC-6 | service | sql  | stored board packs whose content changes after a module is switched off              | 0         | `bash isa/probes/modules-board-pack-frozen.sh`   |
| ISC-7 | service | bash | switched-off routes not answering the 404 marker, no-access routes not answering 403 | 0         | `bash isa/probes/modules-status-codes.sh`        |

ISC-4 and ISC-5 are the load-bearing pair: if a display switch moves the
evidence or an audit-binding export, the initiative's ISC-3 (any past date is
reconstructible) and ISC-4 (no audit-binding artifact without approval) are
answered by a setting rather than by the record.

## Anti-claims

- A1: Switching a module off does not delete, archive or export its data. The
  rows stay in the database under RLS, and evidence they produced stays
  visible under Evidence.
- A2: Module access does not narrow what a role can do inside a module. It
  says only where the member can work.

## Not yet specified

- What the probes drive once the registry exists: ISC-2's probe reads the
  registry and the router, and its exact entry point is set by the ticket that
  builds the registry.

## Decisions

Ratified decisions live in `docs/adr/`. The ones that bound these claims:

| ADR  | Bears on                                                                  |
| ---- | ------------------------------------------------------------------------- |
| 0025 | module access vs roles, what switches may hide, 404 vs 403 (ISC-4..ISC-7) |
| 0012 | append-only evidence ledger, never filtered by module (ISC-4)             |
| 0011 | RLS tenant isolation, unchanged by switches and access (ISC-3)            |

## Changelog

### 2026-11-14 · specified

Filed from spec security-atlas#2166 (security-atlas#2169). The claims come from
its user stories 8, 9, 35-37, 44 and 46. All seven probes are named and none
exists yet. The ADR the spec calls "0024" landed as ADR 0025, because 0024
was taken by the approvals ADR (#2101) first.
