// Slice 183 — shared linkFor helper for the compliance calendar.
//
// Previously `linkFor` lived as a private function inside both
// `agenda-view.tsx` and `month-grid-view.tsx` and returned a string.
// Two of the four event-type branches pointed at routes that DO NOT
// exist in the production tree (`/admin/exceptions/<id>`,
// `/policies/<id>`) — every exception / policy event rendered an
// `<a>` that hit Next.js's default 404. The default branch returned
// `"#"`, which the slice 178 UI-honesty harness flags as a
// dead-anchor (AC-5a) even though the four-type CalendarEventType
// union makes the branch unreachable at compile time.
//
// This module replaces the string-return with a tagged-union return:
//
//   { kind: "link";  href: string }
//   { kind: "static"; reason: string }
//
// Consumers render a `<Link>` for `link` and a `<span title={...}>`
// for `static`. The exception / policy branches return the static
// shape (no link, tooltip explains the placeholder). The audit /
// control branches return the link shape. The default branch calls
// `assertNever` for exhaustiveness — TypeScript widens to `never`
// once all four cases are handled, so adding a fifth event type to
// the union becomes a compile error rather than a silent dead anchor.
//
// AC mapping:
//   AC-1 — default branch no longer returns "#"; assertNever throws if
//          a divergent event type ever appears at runtime.
//   AC-2 — exception events return { kind: "static", reason: <copy> }.
//   AC-3 — policy events return { kind: "static", reason: <copy> }.
//   AC-6 — covered by `link-for.test.ts` (pure logic, node-env vitest).
//
// Anti-criterion P0-183-1: this slice does NOT introduce
// `/admin/exceptions/:id` or `/policies/:id` detail pages. Future
// slices that ship those pages flip the static branch back to a link
// branch HERE (one place to edit, both views update).

import type { CalendarEvent } from "@/lib/api/calendar";

export type LinkForResult =
  | { kind: "link"; href: string }
  | { kind: "static"; reason: string };

const EXCEPTION_REASON =
  "Per-exception detail page is a future slice — view the exception register at /exceptions.";
const POLICY_REASON =
  "Per-policy detail page is a future slice — view the policy register at /policies.";

function assertNever(x: never): never {
  throw new Error(
    `unreachable: linkFor received an unexpected CalendarEvent type: ${JSON.stringify(
      x,
    )}`,
  );
}

export function linkFor(ev: CalendarEvent): LinkForResult {
  switch (ev.type) {
    case "audit":
      return { kind: "link", href: `/audits/${ev.related_entity_id}` };
    case "exception":
      return { kind: "static", reason: EXCEPTION_REASON };
    case "policy":
      return { kind: "static", reason: POLICY_REASON };
    case "control":
      return { kind: "link", href: `/controls/${ev.related_entity_id}` };
    default:
      return assertNever(ev.type);
  }
}
