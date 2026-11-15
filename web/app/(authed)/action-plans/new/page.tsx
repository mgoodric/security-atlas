"use client";

// Slice 384 — /action-plans/new create page (AC-24). Binds the form to the
// BFF create flow (POST /api/action-plans + the link sub-resources). On
// success it invalidates the list-view cache and routes to the new plan's
// detail page so the operator lands where they can manage it.

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";

import { PageShell } from "@/components/shell/page-shell";
import { createActionPlan } from "@/lib/api/action-plans";

import { ActionPlanForm } from "./action-plan-form";

export default function NewActionPlanPage() {
  const router = useRouter();
  const qc = useQueryClient();

  return (
    <PageShell
      width="reading"
      title="New action plan"
      subtitle="A forward-looking commitment to close a gap. Capture the owner, an optional due date, the triggering event, and link the risks and controls the gap touches."
    >
      <ActionPlanForm
        onSubmit={async (body) => {
          const { id } = await createActionPlan(body);
          await qc.invalidateQueries({ queryKey: ["action-plans", "list"] });
          router.push(`/action-plans/${encodeURIComponent(id)}`);
        }}
      />
    </PageShell>
  );
}
