"use client";

// Slice 384 — /action-plans/new create page (AC-24). Binds the form to the
// BFF create flow (POST /api/action-plans + the link sub-resources). On
// success it invalidates the list-view cache and routes to the new plan's
// detail page so the operator lands where they can manage it.

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";

import { createActionPlan } from "@/lib/api/action-plans";

import { ActionPlanForm } from "./action-plan-form";

export default function NewActionPlanPage() {
  const router = useRouter();
  const qc = useQueryClient();

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">
          New action plan
        </h1>
        <p className="text-sm text-muted-foreground">
          A forward-looking commitment to close a gap. Capture the owner, an
          optional due date, the triggering event, and link the risks and
          controls the gap touches.
        </p>
      </div>
      <ActionPlanForm
        onSubmit={async (body) => {
          const { id } = await createActionPlan(body);
          await qc.invalidateQueries({ queryKey: ["action-plans", "list"] });
          router.push(`/action-plans/${encodeURIComponent(id)}`);
        }}
      />
    </div>
  );
}
