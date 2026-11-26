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
// for `static`. Every known event type returns the link shape; the
// static shape is the runtime fallback for a type the frontend does
// not recognise. The default branch takes a `never` argument for
// exhaustiveness, so adding an event type to the union without a case
// is a compile error rather than a silent dead anchor. At runtime an
// unrecognised type returns the static shape (#2215: it used to throw,
// which crashed the page).
//
// AC mapping:
//   AC-1 — default branch never returns "#"; an unrecognised runtime
//          type renders as plain text.
//   AC-6 — covered by `link-for.test.ts` (pure logic, node-env vitest).
//
// Slice 183 rendered exception and policy events unlinked because
// their detail pages did not exist yet. #2772: `/exceptions/[id]` and
// `/policies/[id]` both ship now, so those branches link.

import type { CalendarEvent } from "@/lib/api/calendar";

export type LinkForResult =
  | { kind: "link"; href: string }
  | { kind: "static"; reason: string };

const UNKNOWN_REASON =
  "has no page in this version of the app, so it is shown without a link.";

// unknownType keeps the switch exhaustive at compile time (a new member of
// the union that linkFor does not handle is a type error) without throwing
// at runtime. #2215: the API emitted `offboarding` before the union knew
// it, and the old assertNever throw took down the whole /calendar page.
// A type the frontend does not recognise renders as a plain, unlinked
// event instead.
function unknownType(type: never): LinkForResult {
  return {
    kind: "static",
    reason: `Calendar event type ${JSON.stringify(type)} ${UNKNOWN_REASON}`,
  };
}

export function linkFor(ev: CalendarEvent): LinkForResult {
  switch (ev.type) {
    case "audit":
      return { kind: "link", href: `/audits/${ev.related_entity_id}` };
    case "exception":
      // #2772: links to web/app/(authed)/exceptions/[id]/page.tsx.
      return { kind: "link", href: `/exceptions/${ev.related_entity_id}` };
    case "policy":
    case "policy_ack":
      // #2772: links to web/app/(authed)/policies/[id]/page.tsx. A
      // policy_ack event's related_entity_id is the acknowledged
      // policy_version_id, which is a `policies` row id (each publish
      // writes a fresh row), so it resolves on the same page.
      return { kind: "link", href: `/policies/${ev.related_entity_id}` };
    case "vendor":
      // Slice 675: vendor review events link to the real per-vendor
      // detail page (web/app/(authed)/vendors/[id]/page.tsx).
      return { kind: "link", href: `/vendors/${ev.related_entity_id}` };
    case "control":
      return { kind: "link", href: `/controls/${ev.related_entity_id}` };
    case "risk":
      // #2130: risk review events link to the risk detail page.
      return { kind: "link", href: `/risks/${ev.related_entity_id}` };
    case "action_plan":
      // #2130: action-plan due dates link to the plan's detail page.
      return { kind: "link", href: `/action-plans/${ev.related_entity_id}` };
    case "offboarding":
      // #2215: an open offboarding checklist links to its detail page.
      return {
        kind: "link",
        href: `/personnel-security/${ev.related_entity_id}`,
      };
    default:
      return unknownType(ev.type);
  }
}
