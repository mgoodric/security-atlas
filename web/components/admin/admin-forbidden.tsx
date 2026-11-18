// The /admin 403 panel. A 403 explanation, NOT a 404, so a signed-in user
// can tell "this exists, I just can't reach it" (slice 060 AC-7).
//
// #2503: two variants. `section` is the whole /admin section, for a caller
// who is neither a super-admin nor a tenant admin. `platform` is a
// platform page (API keys, features, tenants, super admins, demo) reached
// by a tenant admin, who manages only the tenant-scoped pages (SSO, Users,
// Audit). See docs/audit-log/2503-tenant-admin-pages-decisions.md.

import Link from "next/link";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export function AdminForbiddenPanel({
  variant,
}: {
  variant: "section" | "platform";
}) {
  return (
    <div className="mx-auto max-w-2xl space-y-6 py-12">
      {variant === "section" ? (
        <Alert variant="destructive">
          <AlertTitle>This section is admin-only</AlertTitle>
          <AlertDescription>
            Your account is signed in but does not hold the{" "}
            <code className="rounded bg-muted px-1 py-0.5 text-xs">admin</code>{" "}
            role in this organisation, which this section requires. Ask an
            organisation admin to grant it. The admin section covers user role
            assignment and the platform settings a super-admin manages.
          </AlertDescription>
        </Alert>
      ) : (
        <Alert variant="destructive" data-testid="admin-platform-forbidden">
          <AlertTitle>This page is for platform super-admins</AlertTitle>
          <AlertDescription>
            You administer this organisation, which covers its sign-in, its
            users and their roles, and its audit log. This page changes platform
            settings and needs a platform super-admin. Contact your platform
            operator.
          </AlertDescription>
        </Alert>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Where to go from here</CardTitle>
          <CardDescription>
            Most day-to-day program work lives outside this section.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {variant === "platform" ? (
            <p>
              <Link href="/admin/users" className="font-medium underline">
                Users
              </Link>{" "}
              — your organisation&apos;s members and their roles.
            </p>
          ) : null}
          <p>
            <Link href="/dashboard" className="font-medium underline">
              Dashboard
            </Link>{" "}
            — program overview, controls, evidence freshness.
          </p>
          <p>
            <Link href="/controls" className="font-medium underline">
              Controls
            </Link>{" "}
            — the day-to-day surface for control owners.
          </p>
          <p>
            <Link href="/catalog/scf" className="font-medium underline">
              Catalog
            </Link>{" "}
            — SCF anchor browser (open to all signed-in users).
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
