"use client";

// Slice 105 — `/risks/new` create page.
//
// Owns the empty-state CTA target that slice 100 placeholder-pointed at
// `/admin`. The form binds directly to slice-019's `POST /v1/risks`
// (the createReq wire shape in `internal/api/risks/handlers.go`) and
// routes through the BFF at `POST /api/risks` so the bearer cookie
// stays httpOnly (slice 100 GET + slice 105 POST share the route).
//
// Constitutional invariants honored:
//   - Invariant 6 (tenant isolation): the BFF forwards the bearer
//     cookie; the platform enforces tenant isolation via RLS on the
//     insert path (slice 033 pattern). The form never passes tenant_id.
//   - AI-assist boundary: no AI on this surface — pure CRUD form.
//
// Anti-criteria honored (P0):
//   - Does NOT extend the slice-019 backend — write path unchanged.
//   - Does NOT add bulk-import / CSV upload.
//   - Does NOT touch /risks/hierarchy.
//   - Does NOT invent risk fields not on `createReq`. Optional fields
//     not enumerated in AC-2 are omitted, not fabricated.
//
// AC-4 / AC-5: On success the page invalidates the TanStack
// `["risks", "list"]` query (which slice 100's `/risks` list uses) and
// router-pushes to `/risks` so the new row appears immediately. On 4xx
// the upstream error renders inline in the form and the user's input
// is preserved.

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";

import { createRiskFromCookieSession } from "./actions";
import { RiskForm } from "./risk-form";

export default function NewRiskPage() {
  const router = useRouter();
  const qc = useQueryClient();

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Add risk</h1>
        <p className="text-sm text-muted-foreground">
          Capture an operational risk against the program register. You can link
          controls and refine residual scoring later from the risk detail view.
        </p>
      </div>
      <RiskForm
        onSubmit={async (body) => {
          await createRiskFromCookieSession(body);
          // AC-4: invalidate the list-view cache and route back to it.
          await qc.invalidateQueries({ queryKey: ["risks", "list"] });
          router.push("/risks");
        }}
      />
    </div>
  );
}
