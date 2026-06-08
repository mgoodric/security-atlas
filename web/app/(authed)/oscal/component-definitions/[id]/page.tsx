"use client";

// Slice 589 — /oscal/component-definitions/[id] vendor-claims view.
//
// Lists one imported component-definition's vendor CLAIMS (joined to their
// owning component) and lets a grc_engineer disposition each claim:
// accept / reject / needs-info.
//
// THE LOAD-BEARING BOUNDARY: a vendor claim is an ASSERTION, not
// platform-verified evidence (canvas invariant #2 / P0-512-1). Accepting a
// claim records that the operator credits the vendor's assertion — it does
// NOT auto-satisfy a control. The UI labels every claim as a vendor claim and
// never implies acceptance produces control coverage. The unmapped flag
// surfaces the slice-512 `scf_anchor_id IS NULL` claims that still need an
// SCF-anchor mapping.
//
// Data source:
//   GET  /api/oscal/component-definitions/[id]                  (claims)
//   POST /api/oscal/component-claims/[claimId]/disposition      (disposition)

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { use, useState } from "react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { bffControlFetch } from "@/lib/api/_shared";
import type {
  ClaimStatus,
  ComponentDefinitionDetail,
  Disposition,
  DispositionResult,
} from "@/lib/api/oscal-components";

function statusBadgeVariant(
  status: ClaimStatus,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "accepted":
      return "default";
    case "rejected":
      return "destructive";
    case "needs_info":
      return "outline";
    default:
      return "secondary";
  }
}

function statusLabel(status: ClaimStatus): string {
  switch (status) {
    case "accepted":
      return "Accepted";
    case "rejected":
      return "Rejected";
    case "needs_info":
      return "Needs info";
    default:
      return "Asserted";
  }
}

export default function ComponentDefinitionDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const qc = useQueryClient();
  const [note, setNote] = useState<Record<string, string>>({});
  const [actionError, setActionError] = useState<string | null>(null);

  const detailQ = useQuery({
    queryKey: ["oscal", "component-definition", id],
    queryFn: () =>
      bffControlFetch<ComponentDefinitionDetail>(
        `/api/oscal/component-definitions/${id}`,
      ),
  });

  const dispositionM = useMutation({
    mutationFn: async (args: {
      claimId: string;
      disposition: Disposition;
    }): Promise<DispositionResult> => {
      const res = await fetch(
        `/api/oscal/component-claims/${args.claimId}/disposition`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            disposition: args.disposition,
            note: note[args.claimId] ?? "",
          }),
        },
      );
      if (!res.ok) {
        let msg = `${res.status} ${res.statusText}`;
        try {
          const j = (await res.json()) as { error?: string };
          if (j.error) msg = j.error;
        } catch {
          // keep status line
        }
        throw new Error(msg);
      }
      return (await res.json()) as DispositionResult;
    },
    onSuccess: () => {
      setActionError(null);
      void qc.invalidateQueries({
        queryKey: ["oscal", "component-definition", id],
      });
    },
    onError: (err: Error) => setActionError(err.message),
  });

  if (detailQ.isLoading) {
    return (
      <div className="space-y-3" data-testid="component-definition-loading">
        <Skeleton className="h-8 w-1/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (detailQ.isError || !detailQ.data) {
    return (
      <Alert variant="destructive" data-testid="component-definition-error">
        Could not load this component-definition.
      </Alert>
    );
  }

  const d = detailQ.data;

  return (
    <div className="space-y-6" data-testid="component-definition-detail">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">
          {d.source_label || d.catalog_title || d.id}
        </h1>
        <p className="text-sm text-muted-foreground">
          {d.catalog_title} · OSCAL {d.oscal_version} · imported by{" "}
          {d.imported_by}
        </p>
        <p
          className="text-xs text-muted-foreground"
          data-testid="vendor-claim-disclaimer"
        >
          Every row below is a vendor assertion, not platform-verified evidence.
          Accepting a claim credits the vendor&apos;s statement; it does not
          satisfy a control on its own.
        </p>
      </header>

      {actionError && (
        <Alert variant="destructive" data-testid="disposition-error">
          {actionError}
        </Alert>
      )}

      {d.claims.length === 0 ? (
        <div
          className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground"
          data-testid="claims-empty"
        >
          This component-definition has no vendor claims.
        </div>
      ) : (
        <ul className="space-y-3" data-testid="claims-list">
          {d.claims.map((c) => (
            <li
              key={c.id}
              className="space-y-3 rounded-lg border p-4"
              data-testid="claim-row"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span
                  className="font-mono text-sm"
                  data-testid="claim-control-id"
                >
                  {c.control_id}
                </span>
                <Badge variant="secondary" data-testid="claim-vendor-badge">
                  Vendor claim
                </Badge>
                <Badge
                  variant={statusBadgeVariant(c.claim_status)}
                  data-testid="claim-status-badge"
                >
                  {statusLabel(c.claim_status)}
                </Badge>
                {c.unmapped && (
                  <Badge variant="outline" data-testid="claim-unmapped-badge">
                    Unmapped to SCF
                  </Badge>
                )}
                {c.scf_anchor_id && (
                  <Badge variant="outline" data-testid="claim-scf-anchor">
                    {c.scf_anchor_id}
                  </Badge>
                )}
              </div>

              <div className="text-xs text-muted-foreground">
                {c.component_title} · {c.component_type}
              </div>
              <p className="text-sm" data-testid="claim-statement">
                {c.statement || "(no vendor statement)"}
              </p>

              {c.dispositioned_by && (
                <p
                  className="text-xs text-muted-foreground"
                  data-testid="claim-disposition-meta"
                >
                  {statusLabel(c.claim_status)} by {c.dispositioned_by}
                  {c.disposition_note ? ` — ${c.disposition_note}` : ""}
                </p>
              )}

              <textarea
                className="w-full rounded-md border p-2 text-sm"
                rows={2}
                placeholder="Disposition note (optional)"
                data-testid="claim-note-input"
                value={note[c.id] ?? ""}
                onChange={(e) =>
                  setNote((prev) => ({ ...prev, [c.id]: e.target.value }))
                }
              />

              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  data-testid="claim-accept"
                  disabled={dispositionM.isPending}
                  onClick={() =>
                    dispositionM.mutate({
                      claimId: c.id,
                      disposition: "accept",
                    })
                  }
                >
                  Accept
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  data-testid="claim-reject"
                  disabled={dispositionM.isPending}
                  onClick={() =>
                    dispositionM.mutate({
                      claimId: c.id,
                      disposition: "reject",
                    })
                  }
                >
                  Reject
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  data-testid="claim-needs-info"
                  disabled={dispositionM.isPending}
                  onClick={() =>
                    dispositionM.mutate({
                      claimId: c.id,
                      disposition: "needs-info",
                    })
                  }
                >
                  Needs info
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
