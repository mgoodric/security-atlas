// Slice 278 — /admin/demo page.
//
// Surfaces the slice-205 demo-seed + teardown CLI verbs as one-click
// admin buttons. The page is a server-rendered shell; the dynamic
// status fetch + button rendering live in <DemoControls/> so that
// `page.route` mocking in the Playwright e2e can intercept the
// status call (server-side fetches are invisible to the browser).
//
// Authority gate runs in admin/layout.tsx — non-admins never reach
// this page. P0-278-3 is enforced upstream via OPA.
//
// P0-278-1 honored: <DemoControls/> renders the disabled banner
// when the env var is unset; buttons only render when the BFF
// reports enabled=true.

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

import { DemoControls } from "./demo-controls";

export default function AdminDemoPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 py-2">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold tracking-tight">Demo tools</h1>
        <p className="text-sm text-muted-foreground">
          Reseed and tear down the demo tenant on this deployment. Backed by
          slice 205&apos;s <code className="rounded bg-muted px-1">demo</code>{" "}
          CLI; surfaced here for one-click operator access.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>What these actions do</CardTitle>
          <CardDescription>
            Two operator-driven actions; each writes one audit-log row before
            invoking the seeder.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div>
            <strong>Reseed demo dataset</strong> — creates (or no-ops on) the{" "}
            <code className="rounded bg-muted px-1">demo</code> tenant and
            populates it with ~50 controls, ~20 risks, ~200 evidence records, 3
            audit periods, and ~12 samples. Idempotent on the slug.
          </div>
          <div>
            <strong>Tear down demo tenant</strong> — deletes the demo tenant and
            every row anchored to it. Refuses to operate on a tenant that does
            not carry the slice-205 forensic mark (typo-safety).
          </div>
          <div className="text-xs text-muted-foreground">
            Both actions are rate-limited to one invocation per 60 seconds per
            IP. Every click writes a{" "}
            <code className="rounded bg-muted px-1">demo_seed</code> or{" "}
            <code className="rounded bg-muted px-1">demo_teardown</code> row to
            the audit log before the seeder runs.
          </div>
        </CardContent>
      </Card>

      <DemoControls />
    </div>
  );
}
