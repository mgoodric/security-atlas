// Slice 672 — minimal global not-found page for routes OUTSIDE the
// `(authed)` group (unauthed / pre-login URLs).
//
// The load-bearing fix for ATLAS-024 is the in-shell authed boundary
// (`(authed)/not-found.tsx`), which keeps the sidebar/nav for a 404
// inside the app. This global page is the fallback for a 404 on an
// unauthed route (e.g. a mistyped `/login/...`), where there is no app
// shell to preserve. It is intentionally minimal: a centered card with a
// link home, rendered inside the root layout (html/body/Providers).

import Link from "next/link";

import { buttonVariants } from "@/components/ui/button";

export default function NotFound() {
  return (
    <main className="flex min-h-screen flex-col items-center justify-center gap-4 p-6 text-center">
      <h1 className="text-lg font-semibold text-foreground">Page not found</h1>
      <p className="max-w-sm text-sm text-muted-foreground">
        The page you were looking for doesn&apos;t exist.
      </p>
      <Link href="/" className={buttonVariants({ size: "sm" })}>
        Go home
      </Link>
    </main>
  );
}
