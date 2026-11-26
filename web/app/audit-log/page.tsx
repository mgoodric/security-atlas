// /audit-log — retired; Activity is the one audit-log page (#2093).
//
// The redirect keeps the query string, so a bookmarked or shared filter
// (`?from=…&to=…&actor=…&kind=…`) opens the same view on /activity.
// It grants nothing: /activity reads through `/v1/activity/unified`,
// which every tenant member already reaches, and the backend's
// row-visibility predicate decides what each caller sees there.

import { redirect } from "next/navigation";

export default async function AuditLogRedirect({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(await searchParams)) {
    if (Array.isArray(v)) v.forEach((x) => params.append(k, x));
    else if (v !== undefined) params.set(k, v);
  }
  const qs = params.toString();
  redirect(qs ? `/activity?${qs}` : "/activity");
}
