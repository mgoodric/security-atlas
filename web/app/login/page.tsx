import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { VersionFooter } from "@/components/version-footer";
import { FirstInstallCard } from "@/components/first-install-card";
import { ThemeAwareLogo } from "@/components/shell/theme-aware-logo";

import { signIn, signInLocal } from "./actions";
import { apiBaseURL } from "@/lib/api/base";
import { type InstallState, loginTenantMode } from "@/lib/auth/login-tenant";

type SearchParams = Promise<{ from?: string; error?: string }>;

// Server-side install-state fetch. #1756: it decides how the
// email/password form names the tenant (see loginTenantMode); the form
// itself always renders. null means install-state could not be read.
//
// Known limitation: this is a server-component fetch, so Playwright's
// page.route() (which only sees browser-originated requests) can't mock
// it. The slice-073 BFF-relay pattern (web/app/api/install-state/route.ts +
// FirstInstallCard) exists for the test-mockable surface; this card's
// Playwright e2e (AC-14) will need a similar client-island promotion when
// the test commit lands.
async function fetchInstallState(): Promise<InstallState | null> {
  try {
    const response = await fetch(`${apiBaseURL()}/v1/install-state`, {
      // #1756: no-store, like the /api/install-state BFF. A cached
      // first-install answer could post a stale tenant id, or hide the
      // tenant field after a second tenant was added.
      cache: "no-store",
      // Bound the wait so a wedged backend doesn't stall /login forever.
      signal: AbortSignal.timeout(3000),
    });
    if (!response.ok) return null;
    return (await response.json()) as InstallState;
  } catch {
    return null;
  }
}

// Slice 123 — the first-install guidance is rendered by the
// FirstInstallCard client island, which fetches /api/install-state
// (the slice-123 BFF) from the browser so Playwright's `page.route()`
// mock can intercept it. The slice-073 server-side fetch lived in
// this file; it ran in Node and the Playwright mock for
// `**/v1/install-state` never fired (mocks only see browser traffic),
// timing out the first-time-login.spec.ts assertion. See
// docs/audit-log/123-investigate-4-unmasked-e2e-spec-failures-decisions.md
// §First-time-login for the full diagnosis.

export default async function LoginPage({
  searchParams,
}: {
  searchParams: SearchParams;
}) {
  const { from, error } = await searchParams;
  // #1756 — the email/password form always renders; install-state only
  // decides whether it sends a hidden tenant, none, or asks for one.
  const tenantMode = loginTenantMode(await fetchInstallState());

  // Slice 363 — form-error association. aria-describedby on every input
  // points at the Alert's id when the Alert is mounted. See
  // `web/components/ui/checkbox.tsx` for the convention. #1756: the
  // email/password card is always present, so errors from either form
  // render there.
  const signinLocalErrorId = error ? "signin-local-error" : undefined;

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 p-4">
      <div className="w-full max-w-md space-y-4">
        {/* Slice 075 — logo header above the sign-in card. AC-5 of slice
          075 specifies the logo renders identically on /login (where
          the user is not yet authed) and the (authed) routes. The login
          page does not use the TopBar component, so the logo is placed
          directly here.

          Slice 176 — swapped the inline `<picture media="prefers-color-scheme">`
          element for `<ThemeAwareLogo>`. The OS-coupled `<picture>` element
          was the wrong signal — slice 170's app theme picker writes
          `data-theme` to `<html>` (light / dark / system) and the logo
          variant must follow that. See
          docs/audit-log/176-logo-theme-coupling-decisions.md. */}
        <div className="flex justify-center pb-2">
          <ThemeAwareLogo
            width={64}
            height={64}
            className="h-16 w-16"
            alt="security-atlas"
          />
        </div>
        <FirstInstallCard />

        {/* Slice 209 — email/password sign-in for self-hosted operators
          who don't run an external IdP. #1756: rendered on every install,
          not only during first install. */}
        <Card>
          <CardHeader>
            <CardTitle>Sign in</CardTitle>
            <CardDescription>
              Use your account email and password.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {error ? (
              <Alert
                variant="destructive"
                className="mb-4"
                id="signin-local-error"
                aria-live="polite"
              >
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            <form action={signInLocal} className="space-y-4">
              <input type="hidden" name="from" value={from ?? "/dashboard"} />
              {tenantMode.kind === "hidden" ? (
                <input
                  type="hidden"
                  name="tenant_id"
                  value={tenantMode.tenantID}
                />
              ) : null}
              {tenantMode.kind === "ask" ? (
                <div className="space-y-2">
                  <label htmlFor="tenant_id" className="text-sm font-medium">
                    Tenant ID
                  </label>
                  <Input
                    id="tenant_id"
                    name="tenant_id"
                    type="text"
                    placeholder="00000000-0000-0000-0000-000000000000"
                    required={tenantMode.required}
                    autoComplete="off"
                    aria-describedby={signinLocalErrorId}
                  />
                </div>
              ) : null}
              <div className="space-y-2">
                <label htmlFor="email" className="text-sm font-medium">
                  Email
                </label>
                <Input
                  id="email"
                  name="email"
                  type="email"
                  placeholder="you@example.com"
                  required
                  autoComplete="email"
                  aria-describedby={signinLocalErrorId}
                />
              </div>
              <div className="space-y-2">
                <label htmlFor="password" className="text-sm font-medium">
                  Password
                </label>
                <Input
                  id="password"
                  name="password"
                  type="password"
                  required
                  autoComplete="current-password"
                  aria-describedby={signinLocalErrorId}
                />
              </div>
              <Button type="submit" className="w-full">
                Sign in
              </Button>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Developer sign-in</CardTitle>
            <CardDescription>
              Paste a bearer token issued by{" "}
              <code>atlas-cli credentials issue</code> or printed to stderr at
              server startup.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form action={signIn} className="space-y-4">
              <input type="hidden" name="from" value={from ?? "/dashboard"} />
              <div className="space-y-2">
                <label htmlFor="token" className="text-sm font-medium">
                  Bearer token
                </label>
                <Input
                  id="token"
                  name="token"
                  type="password"
                  placeholder="long opaque string"
                  required
                  autoComplete="off"
                  aria-describedby={signinLocalErrorId}
                />
              </div>
              <Button type="submit" className="w-full">
                Sign in
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
      {/* Slice 072: VersionFooter renders fixed-bottom-right and does
        not affect the centered Card layout. Lets self-hosted operators
        verify what they're running before sign-in. */}
      <VersionFooter />
    </div>
  );
}
