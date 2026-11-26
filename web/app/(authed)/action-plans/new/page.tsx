"use client";

// Slice 384 — /action-plans/new create page (AC-24). Binds the form to the
// BFF create flow (POST /api/action-plans + the link sub-resources). On
// success it invalidates the list-view cache and routes to the new plan's
// detail page so the operator lands where they can manage it.
//
// #2106: opened from a risk's "Add action plan" (?risk_id=<id>), the form
// starts with that risk linked and, once the plan is created, returns to
// the risk, where the plan now shows under Linked Action Plans. The
// platform still checks the risk when the plan is linked to it.

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { use } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { useToast } from "@/components/ui/toast";
import { createActionPlan } from "@/lib/api/action-plans";

import { ActionPlanForm } from "./action-plan-form";
import { prefillFromSearch } from "./prefill";

export default function NewActionPlanPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const router = useRouter();
  const qc = useQueryClient();
  const { toast } = useToast();
  const prefill = prefillFromSearch(use(searchParams));

  return (
    <PageShell
      width="reading"
      title="New action plan"
      subtitle="A forward-looking commitment to close a gap. Capture the owner, an optional due date, the triggering event, and link the risks and controls the gap touches."
    >
      <ActionPlanForm
        initialRiskIds={prefill.riskIds}
        onSubmit={async (body) => {
          const { id } = await createActionPlan(body);
          await qc.invalidateQueries({ queryKey: ["action-plans"] });
          toast("Action plan created");
          router.push(
            prefill.returnTo ?? `/action-plans/${encodeURIComponent(id)}`,
          );
        }}
      />
    </PageShell>
  );
}
