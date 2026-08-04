"use client";

// OE-664 — /personnel-security/[id] detail view. Shows the checklist's
// person + due date + status plus every item, with an inline
// item-completion form (evidence URI + notes; completed_by defaults
// server-side to the caller's credential). Completing an item POSTs to
// the OE-663 complete endpoint via the BFF, which writes the
// personnel_security.workflow.v1 evidence record (invariant 9). Tenant
// isolation is RLS-enforced (invariant 6); a cross-tenant id resolves
// to a clean upstream 404.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useMemo, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  completePersonnelChecklistItem,
  fetchPersonnelChecklist,
  type ChecklistDetailResponse,
  type ChecklistItem,
} from "@/lib/api/personnel-security";

import {
  dateLabel,
  itemProgress,
  kindLabel,
  statusBadgeClass,
  statusBadgeLabel,
} from "../status";

export default function PersonnelChecklistDetailPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;

  const detailQ = useQuery<ChecklistDetailResponse>({
    queryKey: ["personnel-security", "detail", id],
    queryFn: () => fetchPersonnelChecklist(id),
    enabled: Boolean(id),
  });

  const now = useMemo(() => new Date(), []);

  if (detailQ.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  if (detailQ.isError) {
    return (
      <Alert variant="destructive" data-testid="personnel-detail-error">
        <AlertTitle>Could not load checklist</AlertTitle>
        <AlertDescription>{(detailQ.error as Error).message}</AlertDescription>
      </Alert>
    );
  }

  const checklist = detailQ.data!.checklist;
  const items = [...checklist.items].sort(
    (a, b) => a.sort_order - b.sort_order,
  );

  return (
    <div className="space-y-6 max-w-3xl" data-testid="personnel-detail">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1
            className="text-2xl font-semibold tracking-tight"
            data-testid="personnel-detail-person"
          >
            {checklist.person_display_name || checklist.person_external_id}
          </h1>
          <div className="mt-1 flex items-center gap-2">
            <span className="text-sm" data-testid="personnel-detail-kind">
              {kindLabel(checklist.kind)}
            </span>
            <span
              className={
                "inline-flex items-center rounded-md px-1.5 py-0.5 text-[11px] font-medium " +
                statusBadgeClass(checklist, now)
              }
              data-testid="personnel-detail-status"
            >
              {statusBadgeLabel(checklist, now)}
            </span>
            <span className="text-xs text-muted-foreground">
              Due {dateLabel(checklist.due_at)}
            </span>
          </div>
        </div>
        <Link
          href="/personnel-security"
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Back to list
        </Link>
      </div>

      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="Person ID" value={checklist.person_external_id} mono />
        <Field
          label="Work email"
          value={checklist.person_work_email || "—"}
          mono={Boolean(checklist.person_work_email)}
        />
        <Field label="Source" value={checklist.source} />
        <Field
          label="Linked control"
          value={checklist.control_id ?? "—"}
          mono={Boolean(checklist.control_id)}
        />
      </dl>

      <section className="space-y-3">
        <div className="flex items-baseline justify-between">
          <h2 className="text-sm font-medium">Checklist items</h2>
          <span
            className="text-xs text-muted-foreground"
            data-testid="personnel-detail-progress"
          >
            {itemProgress(items)}
          </span>
        </div>
        {items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No items.</p>
        ) : (
          <ul className="space-y-2" data-testid="personnel-detail-items">
            {items.map((item) => (
              <ItemRow key={item.id} item={item} checklistId={checklist.id} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function ItemRow({
  item,
  checklistId,
}: {
  item: ChecklistItem;
  checklistId: string;
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [evidenceUri, setEvidenceUri] = useState("");
  const [notes, setNotes] = useState("");

  const completeMut = useMutation({
    mutationFn: () =>
      completePersonnelChecklistItem(item.id, {
        evidence_uri: evidenceUri.trim(),
        notes: notes.trim(),
      }),
    onSuccess: () => {
      // The item completion may also flip the checklist status to
      // completed (last open item) — refetch the whole detail, and drop
      // the list cache so badges/sort refresh on the next visit.
      void queryClient.invalidateQueries({
        queryKey: ["personnel-security", "detail", checklistId],
      });
      void queryClient.invalidateQueries({
        queryKey: ["personnel-security", "list"],
      });
    },
  });

  const completed = Boolean(item.completed_at);

  return (
    <li
      className="rounded-md border p-3 space-y-2"
      data-testid="personnel-item"
      data-item-slug={item.slug}
    >
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium" data-testid="personnel-item-title">
            {item.title}
          </p>
          <p className="text-xs text-muted-foreground">{item.category}</p>
        </div>
        {completed ? (
          <span
            className="inline-flex items-center rounded-md bg-emerald-50 px-1.5 py-0.5 text-[11px] font-medium text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
            data-testid="personnel-item-completed"
          >
            Completed {dateLabel(item.completed_at)}
          </span>
        ) : (
          <Button
            size="sm"
            variant="outline"
            onClick={() => setOpen((v) => !v)}
            data-testid="personnel-item-complete-toggle"
          >
            {open ? "Cancel" : "Complete"}
          </Button>
        )}
      </div>

      {completed ? (
        <div className="space-y-1 text-xs text-muted-foreground">
          <p data-testid="personnel-item-completed-by">
            By {item.completed_by || "—"}
          </p>
          {item.evidence_uri ? (
            <p
              className="font-mono break-all"
              data-testid="personnel-item-evidence-uri"
            >
              {item.evidence_uri}
            </p>
          ) : null}
          {item.notes ? (
            <p data-testid="personnel-item-notes">{item.notes}</p>
          ) : null}
        </div>
      ) : null}

      {!completed && open ? (
        <form
          className="space-y-2 border-t pt-2"
          data-testid="personnel-item-complete-form"
          onSubmit={(e) => {
            e.preventDefault();
            completeMut.mutate();
          }}
        >
          <div className="space-y-1">
            <label
              htmlFor={`evidence-${item.id}`}
              className="text-xs font-medium"
            >
              Evidence URI
            </label>
            <Input
              id={`evidence-${item.id}`}
              value={evidenceUri}
              onChange={(e) => setEvidenceUri(e.target.value)}
              placeholder="https://… (ticket, doc, screenshot)"
              data-testid="personnel-item-evidence-input"
            />
          </div>
          <div className="space-y-1">
            <label htmlFor={`notes-${item.id}`} className="text-xs font-medium">
              Notes
            </label>
            <textarea
              id={`notes-${item.id}`}
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              rows={2}
              className="w-full rounded-md border bg-transparent px-3 py-2 text-sm"
              data-testid="personnel-item-notes-input"
            />
          </div>
          {completeMut.isError ? (
            <p
              className="text-xs text-rose-600"
              data-testid="personnel-item-complete-error"
            >
              {(completeMut.error as Error).message}
            </p>
          ) : null}
          <Button
            type="submit"
            size="sm"
            disabled={completeMut.isPending}
            data-testid="personnel-item-complete-submit"
          >
            {completeMut.isPending ? "Completing…" : "Mark complete"}
          </Button>
        </form>
      ) : null}
    </li>
  );
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={"text-sm " + (mono ? "font-mono break-all" : "")}>
        {value}
      </dd>
    </div>
  );
}
