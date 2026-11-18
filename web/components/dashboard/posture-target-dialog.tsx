"use client";

// #2136 — set a framework version's posture target from its dashboard tile.
//
// Offered only to admin and grc_engineer (`canSetPostureTarget`); the
// platform refuses anyone else and checks the values again. On success the
// posture query is refetched, so the tile colours against the new target.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogCancelButton,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  POSTURE_DIRECTIONS,
  postureTargetError,
  setPostureTarget,
  type PostureDirection,
  type PostureTarget,
} from "@/lib/api/posture-targets";

type Fields = {
  target: string;
  warning: string;
  critical: string;
  direction: PostureDirection;
};

// #2406: with no target of its own, the dialog starts from the prior
// framework version's target when there is one, else from the defaults.
function initialFields(
  current: PostureTarget | null,
  prior: PostureTarget | null = null,
): Fields {
  current = current ?? prior;
  if (!current) {
    return {
      target: "90",
      warning: "80",
      critical: "60",
      direction: "higher_is_better",
    };
  }
  return {
    target: String(current.target_value),
    warning: String(current.warning_threshold),
    critical: String(current.critical_threshold),
    direction: current.direction as PostureDirection,
  };
}

export function PostureTargetDialog({
  frameworkVersionId,
  frameworkLabel,
  current,
  prior = null,
}: {
  frameworkVersionId: string;
  frameworkLabel: string;
  current: PostureTarget | null;
  prior?: PostureTarget | null;
}) {
  const [open, setOpen] = useState(false);
  const [fields, setFields] = useState<Fields>(() =>
    initialFields(current, prior),
  );
  const prefilled = !current && prior !== null;
  const queryClient = useQueryClient();
  const ids = {
    target: useId(),
    warning: useId(),
    critical: useId(),
    direction: useId(),
  };

  const input = {
    target_value: fields.target === "" ? Number.NaN : Number(fields.target),
    warning_threshold:
      fields.warning === "" ? Number.NaN : Number(fields.warning),
    critical_threshold:
      fields.critical === "" ? Number.NaN : Number(fields.critical),
    direction: fields.direction,
  };
  const problem = postureTargetError(input);

  const mutation = useMutation({
    mutationFn: () => setPostureTarget(frameworkVersionId, input),
    onSuccess: async () => {
      await queryClient.invalidateQueries({
        queryKey: ["dashboard", "framework-posture"],
      });
      setOpen(false);
    },
  });

  const set = (k: keyof Fields) => (v: string) =>
    setFields((f) => ({ ...f, [k]: v }));

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        // Re-seed from the saved target each time the dialog opens.
        if (next) setFields(initialFields(current, prior));
        mutation.reset();
        setOpen(next);
      }}
    >
      <DialogTrigger
        render={(props) => (
          <Button
            {...props}
            variant="outline"
            size="sm"
            className="mt-2"
            data-testid="framework-tile-set-target"
          >
            {current ? "Edit target" : "Set target"}
          </Button>
        )}
      />
      <DialogPortal>
        <DialogContent data-testid="posture-target-dialog">
          <DialogHeader>
            <DialogTitle>Posture target: {frameworkLabel}</DialogTitle>
            <DialogDescription>
              The coverage your organisation aims for. The tile turns amber at
              the warning threshold and red at the critical one.
            </DialogDescription>
            {prefilled ? (
              <p
                className="text-sm text-muted-foreground"
                data-testid="posture-target-prefilled"
              >
                Prefilled from the previous framework version&apos;s target.
              </p>
            ) : null}
          </DialogHeader>

          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (!problem) mutation.mutate();
            }}
            className="mt-4 space-y-3"
          >
            <div className="flex flex-col gap-1 text-sm">
              <label htmlFor={ids.direction} className="font-medium">
                Direction
              </label>
              <Select
                id={ids.direction}
                value={fields.direction}
                onChange={(e) => set("direction")(e.target.value)}
                data-testid="posture-target-direction"
              >
                {POSTURE_DIRECTIONS.map((d) => (
                  <option key={d.value} value={d.value}>
                    {d.label}
                  </option>
                ))}
              </Select>
            </div>
            {(
              [
                ["target", "Target coverage (%)"],
                ["warning", "Warning threshold (%)"],
                ["critical", "Critical threshold (%)"],
              ] as const
            ).map(([k, label]) => (
              <div key={k} className="flex flex-col gap-1 text-sm">
                <label htmlFor={ids[k]} className="font-medium">
                  {label}
                </label>
                <Input
                  id={ids[k]}
                  type="number"
                  inputMode="decimal"
                  min={0}
                  max={100}
                  step="0.01"
                  value={fields[k]}
                  onChange={(e) => set(k)(e.target.value)}
                  required
                  data-testid={`posture-target-${k}`}
                />
              </div>
            ))}

            {problem ? (
              <p
                className="text-sm text-destructive"
                role="status"
                data-testid="posture-target-problem"
              >
                {problem}
              </p>
            ) : null}

            {mutation.isError ? (
              <Alert variant="destructive">
                <AlertTitle>Target not saved</AlertTitle>
                <AlertDescription>
                  {(mutation.error as Error)?.message ?? "Unknown error"}
                </AlertDescription>
              </Alert>
            ) : null}

            <DialogFooter>
              <DialogCancelButton />
              <Button
                type="submit"
                disabled={problem !== null || mutation.isPending}
                data-testid="posture-target-save"
              >
                {mutation.isPending ? "Saving…" : "Save target"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </DialogPortal>
    </Dialog>
  );
}
