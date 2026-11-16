// #2092 — FeatureGate: a gated route checks its module on the server.
//
// Wraps an optional module's route (questionnaires, board packs, vendor
// claims) in its layout. The module's state is read before anything
// renders, with the caller's own bearer, through the same
// `GET /v1/me/modules` read the sidebar uses, plus `GET /v1/me` for
// `is_admin`. The page names itself; which module owns it comes from the
// server's registry (#2170), so the web app keeps no map of its own. No
// privileged read: a caller learns only their own tenant's modules and
// their own admin bit.
//
// Module on: the route renders as normal. Module off: a calm off state, and
// the client page never mounts, so there is no skeleton, no flash of the
// form, and no "Could not load…" error from the gated API's 404.
//
// Only an admin is pointed at Admin › Features; a non-admin is told to
// ask one and never sees the link. Neither message leaks internal detail
// (slice 367).
//
// Both reads fail closed (unknown modules and not-admin), so a transport
// error shows the non-admin off state rather than a module the API would
// refuse.

import { cookies } from "next/headers";
import Link from "next/link";
import type { ReactNode } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Card, CardContent } from "@/components/ui/card";
import { fetchAdminMe, fetchEffectiveModules } from "@/lib/api/self.server";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { owningModuleName, resolveFeatureGate } from "@/lib/feature-nav";

export async function FeatureGate({
  page,
  moduleName,
  children,
}: {
  /** The gated page's path, as the module registry lists it. */
  page: string;
  /** The page title shown above the off state. */
  moduleName: string;
  children: ReactNode;
}) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  const [modules, me] = bearer
    ? await Promise.all([fetchEffectiveModules(bearer), fetchAdminMe(bearer)])
    : [null, { is_admin: false }];

  const state = resolveFeatureGate(modules, page, me.is_admin);
  if (state === "on") {
    return children;
  }

  if (state === "no-access") {
    // #2172: the module is on, but outside the caller's module access.
    const name = owningModuleName(modules, page) ?? moduleName;
    return (
      <PageShell title={moduleName}>
        <Card data-testid="feature-no-access-state">
          <CardContent className="py-6 text-sm text-muted-foreground">
            <p>You don&apos;t have access to {name}. Ask an administrator.</p>
          </CardContent>
        </Card>
      </PageShell>
    );
  }

  return (
    <PageShell title={moduleName}>
      <Card data-testid="feature-off-state" data-audience={state}>
        <CardContent className="py-6 text-sm text-muted-foreground">
          {state === "off-admin" ? (
            <p>
              This feature is off.{" "}
              <Link
                href="/admin/features"
                className="font-medium text-foreground underline"
                data-testid="feature-off-admin-link"
              >
                Enable in Admin › Features
              </Link>
            </p>
          ) : (
            <p>
              This feature isn&apos;t turned on for your organisation. Ask an
              administrator.
            </p>
          )}
        </CardContent>
      </Card>
    </PageShell>
  );
}
