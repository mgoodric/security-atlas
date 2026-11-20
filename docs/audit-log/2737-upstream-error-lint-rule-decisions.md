# 2737 — an ESLint rule replaces the upstream-error regex sentinel: decisions log

Issue: mgoodric/security-atlas#2737, a follow-up from the review of #2726
(#2645).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The review of #2726 saw that the
  regex could not see an uncast `res.json()` read. The new rule found no
  existing site in `app/`, `components/` or `lib/`.
- **detection_tier_target:** unit (lint rule, with RuleTester fixtures in
  vitest).

## Decisions

### D1 — the rule traces values; it does not scope by "files that call fetch"

The ticket suggests flagging `.error` reads on `res.json()` results in
files that call `fetch`. The rule instead runs on every non-test source
under `app/`, `components/` and `lib/`, and decides by tracing the object:
an `error` read is flagged when the object is `x.json()` with no arguments
or `JSON.parse(…)`, seen through `await`, TS casts, `.catch(…)`, `||` /
`??` defaults and local variables bound to one. A file-level `fetch` gate
would miss a helper that takes `res: Response` from its caller. The tracing
is what keeps false positives out: `NextResponse.json({…})` has arguments,
and a prop, an `Error` or a `useQuery` result named `error` is not a parsed
body. Running it over the tree reported nothing.

### D2 — the regex's three syntactic shapes are kept as AST checks

A body that arrives as a parameter (`(raw as { error?: string }).error` in
the AI-assist parsers' style) cannot be traced to a `.json()` call. So the
rule keeps the regex's shapes as AST matches: a cast to a type with an
`error` member, an `& { error?: … }` intersection, and `.error ??` a
template literal that reads `.status`. Every regex self-test case is a
RuleTester invalid case.

### D3 — the regex sentinel test is deleted

The rule covers everything it scanned, under the same roots and with the
same single exemption (`lib/api/_shared.ts`), and `just lint` gates it.
Keeping both would be two mechanisms for one check.
