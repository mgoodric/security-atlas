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

type SearchParams = Promise<{ from?: string; error?: string }>;

// Server-side install-state fetch — single-tenant deploys auto-populate
// the hidden tenant_id field from the bootstrap tenant. Multi-tenant
// picker UX is slice 141; here we keep first-install scope only.
//
// Known limitation: this is a server-component fetch, so Playwright's
// page.route() (which only sees browser-originated requests) can't mock
// it. The slice-073 BFF-relay pattern (web/app/api/install-state/route.ts +
// FirstInstallCard) exists for the test-mockable surface; this card's
// Playwright e2e (AC-14) will need a similar client-island promotion when
// the test commit lands.
async function fetchBootstrapTenantID(): Promise<string | null> {
  try {
    const response = await fetch(`${apiBaseURL()}/v1/install-state`, {
      // 5s SSR revalidation absorbs the redundant browser-side fetch
      // FirstInstallCard does on the same page render.
      next: { revalidate: 5 },
      // Bound the wait so a wedged backend doesn't stall /login forever.
      signal: AbortSignal.timeout(3000),
    });
    if (!response.ok) return null;
    const body: { first_install?: boolean; tenant_id?: string } =
      await response.json();
    if (body.first_install && body.tenant_id) return body.tenant_id;
    return null;
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
  // Slice 209 — auto-populate tenant_id from /v1/install-state when the
  // platform is in fresh-install state. null when the platform is past
  // first-install (slice 141 will handle that case with a real picker).
  const bootstrapTenantID = await fetchBootstrapTenantID();

  // Slice 363 — form-error association. The two cards each wrap an
  // independent form (signInLocal vs bearer-paste signIn), so each
  // owns a distinct errorId; aria-describedby on every input points
  // at the corresponding Alert's id when the Alert is mounted. See
  // `web/components/ui/checkbox.tsx` for the convention.
  const signinLocalErrorId =
    error && bootstrapTenantID ? "signin-local-error" : undefined;
  const signinTokenErrorId =
    error && !bootstrapTenantID ? "signin-token-error" : undefined;

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
          who don't run an external IdP. The card is only rendered when
          the platform reported a bootstrap tenant_id; if install-state
          can't be resolved, fall back to the bearer-paste card only. */}
        {bootstrapTenantID ? (
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
                <input
                  type="hidden"
                  name="tenant_id"
                  value={bootstrapTenantID}
                />
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
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>
              {bootstrapTenantID
                ? "Developer sign-in"
                : "Sign in to security-atlas"}
            </CardTitle>
            <CardDescription>
              Paste a bearer token issued by{" "}
              <code>atlas-cli credentials issue</code> or printed to stderr at
              server startup.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {/* Only show error here when there's NO local-auth card above
              (otherwise the error renders in the local-auth card to keep
              context with the form the operator just submitted). */}
            {error && !bootstrapTenantID ? (
              <Alert
                variant="destructive"
                className="mb-4"
                id="signin-token-error"
                aria-live="polite"
              >
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
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
                  aria-describedby={signinTokenErrorId}
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
