"use client";

// Slice 149 — `/audits/new` audit-period create page.
//
// Owns the create flow that slice 102 placeholder-pointed at `/admin`.
// The form binds directly to slice-028's `POST /v1/audit-periods` (the
// `createReq` wire shape in `internal/api/auditperiods/handlers.go`)
// and routes through the BFF at `POST /api/audits` so the bearer
// cookie stays httpOnly. Follows the slice 105 `/risks/new` precedent
// exactly.
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF forwards the bearer
//     cookie; the platform enforces tenant isolation via RLS on the
//     insert path. The form never passes tenant_id.
//   - Invariant 10 (audit-period freezing): NOT touched by create — a
//     newly-created period starts in `status=open` (slice 028
//     handlers.go line 119). Freezing is a separate transition.
//   - AI-assist boundary: no AI on this surface — pure CRUD form.
//
// Anti-criteria honored (P0):
//   - P0-AUD-1: Button now routes to a working create flow (this page),
//     NOT to `/admin`. The toolbar action and the empty-state CTA on
//     `/audits` both `router.push("/audits/new")`.
//   - P0-AUD-2: Does NOT redesign the audit workspace. The workspace
//     view at `/audit/[controlId]` (slice 042) is unchanged.
//
// AC-3 (slice doc): on success the page invalidates the TanStack
// `["audits", "list"]` query (which slice 102's `/audits` list uses)
// and router-pushes to `/audits` so the new row appears immediately.
// On 4xx the upstream error renders inline in the form and the user's
// input is preserved.

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";

import { createAuditPeriodFromCookieSession } from "./actions";
import { AuditPeriodForm } from "./audit-period-form";

export default function NewAuditPeriodPage() {
  const router = useRouter();
  const qc = useQueryClient();

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          New audit period
        </h1>
        <p className="text-sm text-muted-foreground">
          Open a period when an external audit begins. The period bounds the
          evidence the sample population draws from once you freeze it.
        </p>
      </div>
      <AuditPeriodForm
        onSubmit={async (body) => {
          await createAuditPeriodFromCookieSession(body);
          // Invalidate the list-view cache and route back to it so the
          // new row appears immediately.
          await qc.invalidateQueries({ queryKey: ["audits", "list"] });
          router.push("/audits");
        }}
      />
    </div>
  );
}
