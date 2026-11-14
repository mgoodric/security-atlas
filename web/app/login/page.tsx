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

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { signIn, signInLocal } from "./actions";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  devTokenFlagEnabled,
  devTokenSignInAllowed,
  signedInRedirectTarget,
} from "@/lib/auth/dev-token-signin";
import { loginTenantMode } from "@/lib/auth/login-tenant";
import { fetchInstallState, hasValidSession } from "@/lib/auth/login.server";
import { safeRedirectTarget } from "@/lib/safe-redirect";

type SearchParams = Promise<{ from?: string; error?: string }>;

// Server-side install-state fetch (lib/auth/login.server.ts). #1756: it
// decides how the email/password form names the tenant (see
// loginTenantMode); #2062: it also decides whether the developer token
// card renders (see devTokenSignInAllowed). null means install-state could
// not be read.
//
// Known limitation: this is a server-component fetch, so Playwright's
// page.route() (which only sees browser-originated requests) can't mock
// it. The first-install branch of devTokenSignInAllowed is covered in
// vitest (lib/auth/dev-token-signin.test.ts) for that reason.

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

  // #2062 — a visitor with a live session has nothing to do here. Only a
  // session /v1/me accepts counts, so a stale cookie cannot loop.
  const session = (await cookies()).get(ATLAS_JWT_COOKIE)?.value;
  if (session && (await hasValidSession(session))) {
    redirect(signedInRedirectTarget(safeRedirectTarget(from ?? "/dashboard")));
  }

  // #1756 — the email/password form always renders; install-state only
  // decides whether it sends a hidden tenant, none, or asks for one.
  const installState = await fetchInstallState();
  const tenantMode = loginTenantMode(installState);
  // #2062 — the token card is for the bootstrap/recovery token and for
  // deployments that opt in; the signIn action re-checks the same rule.
  const showDevTokenSignIn = devTokenSignInAllowed(
    devTokenFlagEnabled(process.env),
    installState,
  );

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

        {showDevTokenSignIn ? (
          <Card data-testid="dev-token-signin-card">
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
        ) : null}
      </div>
      {/* Slice 072: VersionFooter renders fixed-bottom-right and does
        not affect the centered Card layout. Lets self-hosted operators
        verify what they're running before sign-in. */}
      <VersionFooter />
    </div>
  );
}
