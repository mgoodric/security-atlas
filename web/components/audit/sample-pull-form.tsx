// Slice 042 — deterministic sample-pull form (AC-3).
//
// Inputs: n (sample size) + seed (deterministic reproducibility, canvas
// §8.3 "Sample(population, n, seed) — deterministic, reproducible"). On
// submit it POSTs /v1/samples via the BFF. Client-side guards reject
// non-positive n and an empty seed before the request; the platform
// re-validates as the source of truth.

"use client";

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { drawSample, type Sample } from "@/lib/api/audit";

export function SamplePullForm({
  populationId,
  onSampleDrawn,
}: {
  populationId: string;
  onSampleDrawn?: (sample: Sample) => void;
}) {
  const [n, setN] = useState("");
  const [seed, setSeed] = useState("");
  const [clientError, setClientError] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const mutation = useMutation({
    mutationFn: () =>
      drawSample({ population_id: populationId, n: Number(n), seed }),
    onSuccess: (sample) => {
      queryClient.invalidateQueries({
        queryKey: ["audit", "population", populationId, "samples"],
      });
      onSampleDrawn?.(sample);
      setN("");
      setSeed("");
    },
  });

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setClientError(null);
    const parsed = Number(n);
    if (!Number.isInteger(parsed) || parsed <= 0) {
      setClientError("n must be a positive integer");
      return;
    }
    if (seed.trim() === "") {
      setClientError("seed must be a non-empty string");
      return;
    }
    mutation.mutate();
  }

  const errorMsg =
    clientError ?? (mutation.error ? String(mutation.error.message) : null);

  return (
    <form
      onSubmit={handleSubmit}
      data-testid="sample-pull-form"
      className="grid gap-2"
    >
      <div className="flex flex-wrap items-end gap-2">
        <label className="grid gap-1 text-xs text-muted-foreground">
          Sample size (n)
          <Input
            type="number"
            min={1}
            value={n}
            onChange={(e) => setN(e.target.value)}
            data-testid="sample-n-input"
            className="w-28"
            placeholder="25"
          />
        </label>
        <label className="grid gap-1 text-xs text-muted-foreground">
          Seed
          <Input
            type="text"
            value={seed}
            onChange={(e) => setSeed(e.target.value)}
            data-testid="sample-seed-input"
            className="w-44"
            placeholder="2026-q2-iac06"
          />
        </label>
        <Button
          type="submit"
          disabled={mutation.isPending}
          data-testid="sample-pull-submit"
        >
          {mutation.isPending ? "Drawing…" : "Pull sample"}
        </Button>
      </div>
      {errorMsg ? (
        <Alert variant="destructive" data-testid="sample-pull-error">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}
    </form>
  );
}
