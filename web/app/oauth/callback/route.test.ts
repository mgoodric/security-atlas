// route.test.ts — #2211: the callback's completion script redirects to
// completeLoginFlow's sanitised returnTo, falling back to /dashboard.
// The sanitising itself is covered in lib/auth/oauth-client.test.ts.

import { describe, expect, it } from "vitest";
import { NextRequest } from "next/server";

import { GET } from "./route";

describe("GET /oauth/callback", () => {
  it("assigns the sanitised returnTo with a /dashboard fallback, never '/'", async () => {
    const resp = await GET(
      new NextRequest(
        "https://atlas.example.test/oauth/callback?code=c&state=s",
      ),
    );
    const html = await resp.text();
    expect(html).toContain(
      "window.location.assign(result.returnTo || '/dashboard');",
    );
    expect(html).not.toContain("result.returnTo || '/'");
  });
});
