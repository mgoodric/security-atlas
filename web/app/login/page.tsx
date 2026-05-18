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

import { signIn } from "./actions";

type SearchParams = Promise<{ from?: string; error?: string }>;

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

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 p-4">
      <div className="w-full max-w-md space-y-4">
        {/* Slice 075 — logo header above the sign-in card. AC-5 of slice
          075 specifies the logo renders identically on /login (where
          the user is not yet authed) and the (authed) routes. The login
          page does not use the TopBar component, so the logo is placed
          directly here with the same <picture> theme-aware semantics. */}
        <div className="flex justify-center pb-2">
          <picture>
            <source
              media="(prefers-color-scheme: dark)"
              srcSet="/logo-dark.svg"
            />
            <source
              media="(prefers-color-scheme: light)"
              srcSet="/logo-light.svg"
            />
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src="/logo-light.svg"
              alt="security-atlas"
              width={64}
              height={64}
              className="h-16 w-16"
            />
          </picture>
        </div>
        <FirstInstallCard />
        <Card>
          <CardHeader>
            <CardTitle>Sign in to security-atlas</CardTitle>
            <CardDescription>
              Paste a bearer token issued by{" "}
              <code>atlas-cli credentials issue</code> or printed to stderr at
              server startup.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {error ? (
              <Alert variant="destructive" className="mb-4">
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
