"use client";

// #2129 — the risk's Owner on its detail page. An owner is a user, shown by
// name through <Person>. A risk whose free-text owner matched no user
// shows that text as "Owner (unlinked)" with a picker to choose the user
// it names; a risk with no owner gets the same picker. Picking saves at
// once through PATCH /v1/risks/{id} (`owner_user_id`), which clears the
// unlinked text. The text renders as React text only.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { EntityPicker } from "@/components/ui/entity-picker";
import { Person } from "@/components/ui/person";
import { useToast } from "@/components/ui/toast";
import { type Risk, updateRisk } from "@/lib/api/risks";

export function RiskOwner({ risk }: { risk: Risk }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [picking, setPicking] = useState(false);
  const [picked, setPicked] = useState("");

  const save = useMutation({
    mutationFn: (ownerUserId: string) =>
      updateRisk(risk.id, { owner_user_id: ownerUserId }),
    onSuccess: async () => {
      setPicking(false);
      setPicked("");
      await qc.invalidateQueries({ queryKey: ["risk", risk.id] });
      await qc.invalidateQueries({ queryKey: ["risks"] });
      toast("Owner set");
    },
    onError: (err) =>
      toast(`Could not set the owner: ${(err as Error).message}`),
  });

  if (risk.owner_user_id) {
    return <Person id={risk.owner_user_id} testId="risk-detail-owner" />;
  }

  const unlinked = (risk.owner_unlinked ?? "").trim();
  return (
    <span className="inline-flex flex-wrap items-center gap-2">
      {unlinked ? (
        <span data-testid="risk-detail-owner-unlinked">
          <span className="text-muted-foreground">(unlinked) </span>
          {unlinked}
        </span>
      ) : (
        <span data-testid="risk-detail-owner" className="italic">
          unassigned
        </span>
      )}
      {picking ? (
        <span className="inline-flex items-center gap-2">
          <span className="w-64">
            <EntityPicker
              kinds={["user"]}
              id="risk-detail-owner-pick"
              value={picked}
              onChange={setPicked}
              testId="risk-detail-owner-pick"
              placeholder="Search people by name"
            />
          </span>
          <Button
            type="button"
            size="sm"
            disabled={!picked || save.isPending}
            onClick={() => save.mutate(picked)}
            data-testid="risk-detail-owner-save"
          >
            Save owner
          </Button>
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => {
              setPicking(false);
              setPicked("");
            }}
          >
            Cancel
          </Button>
        </span>
      ) : (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => setPicking(true)}
          data-testid="risk-detail-owner-pick-open"
        >
          {unlinked ? "Pick the user" : "Set owner"}
        </Button>
      )}
    </span>
  );
}
