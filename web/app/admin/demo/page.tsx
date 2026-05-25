// Slice 278 — /admin/demo page.
//
// Surfaces the slice-205 demo-seed + teardown CLI verbs as one-click
// admin buttons. The page is a server component that fetches the
// `/api/admin/demo/status` BFF to decide between two render paths:
//
//   - enabled=false -> "Demo tools are not enabled" banner. The page
//     is reachable (the operator hit it intentionally) but the
//     buttons render disabled with an explanation.
//   - enabled=true  -> two action cards (Reseed / Teardown) with
//     confirmation dialogs. The actual button logic lives in the
//     `<DemoControls>` client component because the dialog + toast
//     flow needs useState.
//
// Authority gate runs in admin/layout.tsx — non-admins never reach
// this page. P0-278-3 is enforced upstream via OPA.
//
// P0-278-1 honored: the page renders the disabled banner even when
// reachable by an admin if the env var is unset. The buttons are
// hidden in that branch; the operator cannot trigger an action.

import { cookies, headers } from "next/headers";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { SESSION_COOKIE } from "@/lib/auth";

import { DemoControls } from "./demo-controls";

async function fetchEnabled(): Promise<boolean> {
  // Self-referential fetch to the BFF — same pattern as
  // app/admin/layout.tsx's isAdmin probe. Fail-closed: any error
  // collapses to enabled=false so the banner renders rather than
  // false-positive buttons.
  try {
    const jar = await cookies();
    const bearer = jar.get(SESSION_COOKIE)?.value;
    if (!bearer) return false;
    const h = await headers();
    const host = h.get("host") ?? "localhost:3000";
    const proto = h.get("x-forwarded-proto") ?? "http";
    const res = await fetch(`${proto}://${host}/api/admin/demo/status`, {
      headers: { Cookie: `${SESSION_COOKIE}=${bearer}` },
      cache: "no-store",
    });
    if (!res.ok) return false;
    const body = (await res.json()) as { enabled?: boolean };
    return body.enabled === true;
  } catch {
    return false;
  }
}

export default async function AdminDemoPage() {
  const enabled = await fetchEnabled();

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

      {!enabled ? (
        <Alert>
          <AlertTitle>Demo tools are not enabled on this deployment</AlertTitle>
          <AlertDescription>
            <p>
              Set{" "}
              <code className="rounded bg-muted px-1 py-0.5 text-xs">
                ATLAS_ENABLE_DEMO_SEED=true
              </code>{" "}
              in the docker-compose env (or the equivalent secrets surface) and
              restart the atlas server to expose these actions.
            </p>
            <p className="mt-2 text-xs text-muted-foreground">
              This page is intentionally always reachable for admins on every
              deployment; the buttons are gated on the env var so production
              deployments cannot accidentally trigger a demo seed.
            </p>
          </AlertDescription>
        </Alert>
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle>What these actions do</CardTitle>
              <CardDescription>
                Two operator-driven actions; each writes one audit-log row
                before invoking the seeder.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-3 text-sm">
              <div>
                <strong>Reseed demo dataset</strong> — creates (or no-ops on)
                the <code className="rounded bg-muted px-1">demo</code> tenant
                and populates it with ~50 controls, ~20 risks, ~200 evidence
                records, 3 audit periods, and ~12 samples. Idempotent on the
                slug.
              </div>
              <div>
                <strong>Tear down demo tenant</strong> — deletes the demo tenant
                and every row anchored to it. Refuses to operate on a tenant
                that does not carry the slice-205 forensic mark (typo-safety).
              </div>
              <div className="text-xs text-muted-foreground">
                Both actions are rate-limited to one invocation per 60 seconds
                per IP. Every click writes a{" "}
                <code className="rounded bg-muted px-1">demo_seed</code> or{" "}
                <code className="rounded bg-muted px-1">demo_teardown</code> row
                to the audit log before the seeder runs.
              </div>
            </CardContent>
          </Card>

          <DemoControls />
        </>
      )}
    </div>
  );
}
