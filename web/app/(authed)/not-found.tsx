// Slice 672 — in-shell not-found boundary for the `(authed)` route group
// (AC-3, load-bearing).
//
// Before this file existed there was NO `not-found.tsx` anywhere under
// `web/app`, so any 404 inside the authed area (e.g. clicking a policy
// title that 404'd, or any `notFound()` call) rendered the framework's
// default shell-LESS 404 — no sidebar, no nav, only browser-back
// recovered, stranding the user (ATLAS-024 secondary finding).
//
// Because this boundary lives INSIDE the `(authed)` route group, Next's
// App Router wraps it in the group's `layout.tsx` (the app shell). A
// `notFound()` thrown from any authed page — including the policy detail
// page on a genuinely-missing id — renders HERE, with the full app shell
// present and every nav affordance reachable.
//
// #2080 — the body is the shared `<NotFoundPanel>`, also used by the root
// not-found for unmatched URLs.

import type { Metadata } from "next";

import {
  NOT_FOUND_TITLE,
  NotFoundPanel,
} from "@/components/shell/not-found-panel";

export const metadata: Metadata = { title: NOT_FOUND_TITLE };

export default function AuthedNotFound() {
  return <NotFoundPanel />;
}
