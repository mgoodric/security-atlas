// #2081 — the count badge a nav row carries, by href (slice 214).
//
// Not a client module, so the desktop sidebar (a server component) and
// the mobile drawer (a client component) both call it; the badges it
// returns are client components either way.

import {
  ApprovalsCountBadge,
  ControlsCountBadge,
  RisksCountBadge,
} from "@/components/shell/sidebar-counts";

export function badgeForHref(href: string) {
  switch (href) {
    case "/approvals":
      return <ApprovalsCountBadge />;
    case "/controls":
      return <ControlsCountBadge />;
    case "/risks":
      return <RisksCountBadge />;
    default:
      return null;
  }
}
