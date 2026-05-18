"use client";

// Slice 123 — first-install guidance card (client island).
//
// Lifted from web/app/login/page.tsx (slice 073) so the install-state
// read can happen in the BROWSER rather than at server-render time.
// Three-bullet bootstrap-token discovery list with grep-line commands
// for each install medium (docker-compose, Helm, bare binary) — same
// copy as the slice-073 server-rendered version, just relocated to a
// client component.
//
// Why client-side: Playwright's `page.route()` only intercepts the
// browser's network traffic. The slice-073 first-time-login.spec.ts
// mocks `**/v1/install-state` expecting to drive the UI deterministically
// — but the slice-073 implementation fetched from the Server Component,
// so the mock never fired and the spec timed out under a real-atlas
// response. Moving the decision to a client island with a BFF fetch
// (`/api/install-state`) lets the mock work.
//
// Anti-criterion P0-A5 (slice 073) preserved: the bearer-token form
// renders unconditionally on the parent page; this island only adds
// the optional guidance card above it. A fetch failure leaves the
// guidance card hidden — sign-in never blocks on metadata.

import { useEffect, useState } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

type InstallState = { first_install?: boolean };

export function FirstInstallCard() {
  const [firstInstall, setFirstInstall] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fetch("/api/install-state", { cache: "no-store" })
      .then((res) => (res.ok ? (res.json() as Promise<InstallState>) : null))
      .then((body) => {
        if (cancelled) return;
        setFirstInstall(body?.first_install === true);
      })
      .catch(() => {
        // P0-A5: a metadata failure never blocks the production sign-in
        // path. Leave the card hidden.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (!firstInstall) return null;

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
