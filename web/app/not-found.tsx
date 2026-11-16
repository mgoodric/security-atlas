// Slice 672 — global not-found page for URLs that match no route.
//
// #2080 — an unmatched URL never enters a route group, so before this a
// signed-in user who mistyped a path got a bare page with no sidebar or
// nav. With a session cookie present it now renders inside `<AppShell>`
// with the same `<NotFoundPanel>` the `(authed)` boundary uses. Without
// one (a mistyped `/login/...`) there is no shell to show, and the page
// stays a minimal centred card with a link home.

import type { Metadata } from "next";
import { cookies } from "next/headers";
import Link from "next/link";

import { AppShell } from "@/components/shell/app-shell";
import {
  NOT_FOUND_TITLE,
  NotFoundPanel,
} from "@/components/shell/not-found-panel";
import { buttonVariants } from "@/components/ui/button";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";

export const metadata: Metadata = { title: NOT_FOUND_TITLE };

export default async function NotFound() {
  const jar = await cookies();
  if (jar.get(ATLAS_JWT_COOKIE)?.value) {
    return (
      <AppShell>
        <NotFoundPanel />
      </AppShell>
    );
  }
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
