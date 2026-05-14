// Slice 042 — audit workspace client shell (AC-1, AC-2, AC-7).
//
// This client component owns the cross-control state that must survive
// navigation between controls:
//
//   * AnnotationDraftProvider — mounted HERE, above the ControlNav and
//     the per-control workspace, so in-progress annotation drafts keyed
//     by `${sampleId}:${recordId}` survive switching controls (AC-7 /
//     P0-3). Because the shell does not unmount when the selected
//     control changes (only the inner workspace swaps), the provider's
//     draft map persists.
//
//   * controls — the in-scope control list (AC-2). The backend has no
//     period-scoped control-list endpoint yet (surfaced gap); v1 seeds
//     the list from a control id the auditor enters, and the list grows
//     as the session goes. The ControlNav renders a documented
//     empty-state when the list is empty.
//
// The shell is rendered by both /audit (no control selected) and
// /audit/[controlId] (a control selected) so the provider + nav identity
// is stable across that navigation.

"use client";

import { useState } from "react";

import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import type { AuditPeriod } from "@/lib/api/audit";
import { AuditPeriodBar } from "@/components/audit/audit-period-bar";
import { ControlNav, type NavControl } from "@/components/audit/control-nav";
import { ControlWorkspace } from "@/components/audit/control-workspace";
import { AnnotationDraftProvider } from "@/components/audit/annotation-draft-store";

export function AuditShell({
  period,
  selectedControlId,
  callerUserId,
}: {
  period: AuditPeriod;
  selectedControlId?: string;
  callerUserId?: string;
}) {
  // Seed the nav with the selected control (if any) so a deep-link to
  // /audit/[controlId] shows that control highlighted in the nav.
  const [controls, setControls] = useState<NavControl[]>(
    selectedControlId ? [{ controlId: selectedControlId }] : [],
  );
  const [controlIdInput, setControlIdInput] = useState("");

  function addControl(id: string) {
    const trimmed = id.trim();
    if (trimmed === "") return;
    setControls((prev) =>
      prev.some((c) => c.controlId === trimmed)
        ? prev
        : [...prev, { controlId: trimmed }],
    );
  }

  return (
    <AnnotationDraftProvider>
      <AuditPeriodBar period={period} />
      <div className="flex flex-1 flex-col overflow-hidden sm:flex-row">
        <div className="flex flex-col sm:w-64 sm:shrink-0">
          <ControlNav
            controls={controls}
            selectedControlId={selectedControlId}
          />
          <form
            onSubmit={(e) => {
              e.preventDefault();
              addControl(controlIdInput);
              setControlIdInput("");
            }}
            className="grid gap-1.5 border-t p-3"
          >
            <label className="text-xs text-muted-foreground">
              Add a control by id
            </label>
            <div className="flex gap-1.5">
              <Input
                type="text"
                value={controlIdInput}
                onChange={(e) => setControlIdInput(e.target.value)}
                placeholder="control UUID"
                data-testid="add-control-input"
                className="flex-1"
              />
              <Button
                type="submit"
                size="sm"
                variant="outline"
                data-testid="add-control-submit"
              >
                Add
              </Button>
            </div>
          </form>
        </div>
        <main className="flex-1 overflow-y-auto p-4 sm:p-6">
          {selectedControlId ? (
            <ControlWorkspace
              controlId={selectedControlId}
              period={period}
              callerUserId={callerUserId}
            />
          ) : (
            <div
              data-testid="audit-no-control-selected"
              className="mx-auto max-w-md py-12 text-center text-sm text-muted-foreground"
            >
              <p className="mb-1 font-medium text-foreground">
                Select a control to begin testing
              </p>
              <p>
                Pick a control from the left nav, or add one by id. Each control
                opens a workspace for population sampling, walkthrough
                recording, and the Audit Hub comment thread.
              </p>
            </div>
          )}
        </main>
      </div>
    </AnnotationDraftProvider>
  );
}
