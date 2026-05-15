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

import { apiBaseURL } from "@/lib/api";
import { signIn } from "./actions";

type SearchParams = Promise<{ from?: string; error?: string }>;

// Slice 073 — server-side fetch of the public /v1/install-state endpoint.
// The endpoint is intentionally bearer-exempt; this fetch carries no
// credential. A failure (network, 503) falls back to "not fresh install"
// so the existing copy renders — never block the production sign-in
// path on a metadata read (P0-A5).
async function fetchFirstInstall(): Promise<boolean> {
  try {
    const res = await fetch(`${apiBaseURL()}/v1/install-state`, {
      cache: "no-store",
    });
    if (!res.ok) return false;
    const body = (await res.json()) as { first_install?: boolean };
    return body.first_install === true;
  } catch {
    return false;
  }
}

export default async function LoginPage({
  searchParams,
}: {
  searchParams: SearchParams;
}) {
  const { from, error } = await searchParams;
  const firstInstall = await fetchFirstInstall();

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/30 p-4">
      <div className="w-full max-w-md space-y-4">
        {firstInstall ? <FirstInstallGuidance /> : null}
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

// FirstInstallGuidance renders the "First time signing in?" card with
// the three orthogonal ways to find the bootstrap token. Three-bullet
// list per CLAUDE.md "Markdown over prose" working norm; the
// troubleshooting link points at the docs-site page that this slice adds.
function FirstInstallGuidance() {
  return (
    <Card data-testid="first-install-card">
      <CardHeader>
        <CardTitle>First time signing in?</CardTitle>
        <CardDescription>
          The bootstrap admin token was generated when the platform started.
          Find it with one of:
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="space-y-2 text-sm">
          <li>
            <strong>docker-compose:</strong>{" "}
            <code className="rounded bg-muted px-1 py-0.5">
              docker compose logs atlas 2&gt;&amp;1 | grep BOOTSTRAP_TOKEN
            </code>
          </li>
          <li>
            <strong>Helm:</strong>{" "}
            <code className="rounded bg-muted px-1 py-0.5">
              kubectl logs deploy/atlas --tail=200 2&gt;&amp;1 | grep
              BOOTSTRAP_TOKEN
            </code>
          </li>
          <li>
            <strong>Bare binary:</strong> look in the stderr of the{" "}
            <code>atlas</code> process you launched, or read{" "}
            <code>$ATLAS_DATA_DIR/bootstrap-token</code> (file mode 0600).
          </li>
        </ul>
        <p className="mt-3 text-xs text-muted-foreground">
          If the token has scrolled out of the log buffer, see{" "}
          <a
            href="/docs/troubleshooting/first-login"
            className="underline underline-offset-2"
          >
            Troubleshooting &rarr; First-time login
          </a>
          .
        </p>
      </CardContent>
    </Card>
  );
}
