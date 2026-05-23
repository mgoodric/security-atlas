// Slice 248 — page-specific `<title>` metadata for `/settings`.
//
// Why this file exists: `web/app/(authed)/settings/page.tsx` is a
// client component (`"use client"` at the top). Next.js App Router
// forbids exporting `metadata` from a client component. The canonical
// pattern is a sibling server-component `layout.tsx` whose only job
// is to declare the page-specific metadata and pass `children`
// through unchanged.
//
// This layout is intentionally thin:
//   - server component (no "use client" directive)
//   - no chrome: the topbar/sidebar live in `(authed)/layout.tsx`,
//     and the page itself owns the inner page chrome. Rendering any
//     wrapper here would double-render or shift the layout tree
//     (slice 248 P0-A2: no `<head>` shadow; slice 248 spec note that
//     this is "the smallest viable fix").
//   - merges into the root metadata (`web/app/layout.tsx`) via Next's
//     metadata cascade: the root sets `title: "security-atlas"` as a
//     plain string, which this child overrides for the `/settings`
//     route only. P0-248-3 (no global title change) is honored.
//
// See `docs/audit-log/248-decisions.md` for the layout-vs-page decision
// (D1) and the title-string choice (D2).

import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Settings · security-atlas",
};

export default function SettingsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return <>{children}</>;
}
