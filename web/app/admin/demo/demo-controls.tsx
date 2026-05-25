// Slice 278 — client component for the demo-tools page.
//
// Renders two action buttons. Each opens a confirmation dialog;
// confirmation fires the POST and shows a success or error Alert
// in-place (no toast lib in the codebase per D8). Loading state
// disables both buttons while a request is in flight.
//
// D4 — Dialog (not AlertDialog) because shadcn's AlertDialog isn't
// in the codebase; the Dialog primitive is the established slice
// 142 pattern.
//
// D7 — UI does NOT call out the second audit-log row that the
// seeder writes; that's an implementation detail. The user sees
// one consolidated post-action summary.

"use client";

import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type SeedResult = {
  tenant_id: string;
  tenant_slug: string;
  controls: number;
  risks: number;
  evidence: number;
  audit_periods: number;
  samples: number;
  idempotent: boolean;
};

type TeardownResult = {
  tenant_slug: string;
  status: string;
};

type ActionState =
  | { kind: "idle" }
  | { kind: "running" }
  | { kind: "success-seed"; result: SeedResult }
  | { kind: "success-teardown"; result: TeardownResult }
  | { kind: "error"; message: string };

export function DemoControls() {
  const [seedDialog, setSeedDialog] = useState(false);
  const [teardownDialog, setTeardownDialog] = useState(false);
  const [state, setState] = useState<ActionState>({ kind: "idle" });

  const busy = state.kind === "running";

  async function runSeed() {
    setState({ kind: "running" });
    setSeedDialog(false);
    try {
      const res = await fetch("/api/admin/demo/seed", { method: "POST" });
      const body = (await res.json()) as Partial<SeedResult> & {
        error?: string;
      };
      if (!res.ok) {
        setState({
          kind: "error",
          message: body.error ?? `${res.status} ${res.statusText}`,
        });
        return;
      }
      setState({
        kind: "success-seed",
        result: body as SeedResult,
      });
    } catch (err) {
      setState({
        kind: "error",
        message: (err as Error).message ?? "network error",
      });
    }
  }

  async function runTeardown() {
    setState({ kind: "running" });
    setTeardownDialog(false);
    try {
      const res = await fetch("/api/admin/demo/teardown", { method: "POST" });
      const body = (await res.json()) as Partial<TeardownResult> & {
        error?: string;
      };
      if (!res.ok) {
        setState({
          kind: "error",
          message: body.error ?? `${res.status} ${res.statusText}`,
        });
        return;
      }
      setState({
        kind: "success-teardown",
        result: body as TeardownResult,
      });
    } catch (err) {
      setState({
        kind: "error",
        message: (err as Error).message ?? "network error",
      });
    }
  }

  return (
    <div className="space-y-4" data-testid="demo-controls">
      {/* Reseed card */}
      <Card>
        <CardHeader>
          <CardTitle>Reseed demo dataset</CardTitle>
          <CardDescription>
            Creates or no-ops on the <code>demo</code> tenant and populates
            it with the slice-205 dataset. Safe to re-click — the seeder is
            idempotent on the slug.
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Button
            data-testid="demo-seed-button"
            onClick={() => setSeedDialog(true)}
            disabled={busy}
          >
            Reseed demo dataset
          </Button>
        </CardFooter>
      </Card>

      {/* Teardown card */}
      <Card>
        <CardHeader>
          <CardTitle>Tear down demo tenant</CardTitle>
          <CardDescription>
            Deletes the demo tenant and every row anchored to it. Reversible
            by clicking Reseed again afterwards. Refuses to operate on a
            tenant that does not carry the slice-205 forensic mark.
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Button
            data-testid="demo-teardown-button"
            variant="destructive"
            onClick={() => setTeardownDialog(true)}
            disabled={busy}
          >
            Tear down demo tenant
          </Button>
        </CardFooter>
      </Card>

      {/* Result alert */}
      {state.kind === "running" ? (
        <Alert data-testid="demo-running">
          <AlertTitle>Running…</AlertTitle>
          <AlertDescription>
            Action in flight. This typically takes 5-10 seconds.
          </AlertDescription>
        </Alert>
      ) : null}

      {state.kind === "success-seed" ? (
        <Alert data-testid="demo-success">
          <AlertTitle>Demo dataset seeded</AlertTitle>
          <AlertDescription>
            <p>
              {state.result.idempotent ? (
                <>
                  Tenant <code>{state.result.tenant_slug}</code> already
                  carried the slice-205 forensic mark; no rows were written.
                  The dataset is already in place.
                </>
              ) : (
                <>
                  {state.result.controls} controls · {state.result.risks}{" "}
                  risks · {state.result.evidence} evidence ·{" "}
                  {state.result.audit_periods} audit periods ·{" "}
                  {state.result.samples} samples seeded into tenant{" "}
                  <code>{state.result.tenant_slug}</code>.
                </>
              )}
            </p>
          </AlertDescription>
        </Alert>
      ) : null}

      {state.kind === "success-teardown" ? (
        <Alert data-testid="demo-success">
          <AlertTitle>Demo tenant torn down</AlertTitle>
          <AlertDescription>
            Tenant <code>{state.result.tenant_slug}</code> deleted along with
            all anchored rows. Click Reseed to recreate the dataset.
          </AlertDescription>
        </Alert>
      ) : null}

      {state.kind === "error" ? (
        <Alert variant="destructive" data-testid="demo-error">
          <AlertTitle>Action failed</AlertTitle>
          <AlertDescription>{state.message}</AlertDescription>
        </Alert>
      ) : null}

      {/* Confirmation dialog: Reseed */}
      <Dialog open={seedDialog} onOpenChange={setSeedDialog}>
        <DialogContent data-testid="demo-seed-dialog">
          <DialogHeader>
            <DialogTitle>Reseed the demo tenant?</DialogTitle>
            <DialogDescription>
              This will create (or no-op on) the <code>demo</code> tenant and
              write the slice-205 dataset. Safe to run on the atlas-edge
              deployment.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setSeedDialog(false)}>
              Cancel
            </Button>
            <Button data-testid="demo-seed-confirm" onClick={runSeed}>
              Reseed
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Confirmation dialog: Teardown */}
      <Dialog open={teardownDialog} onOpenChange={setTeardownDialog}>
        <DialogContent data-testid="demo-teardown-dialog">
          <DialogHeader>
            <DialogTitle>Tear down the demo tenant?</DialogTitle>
            <DialogDescription>
              This will delete the <code>demo</code> tenant and every row
              anchored to it. The seeder refuses to operate on a tenant that
              does not carry the slice-205 forensic mark, so non-demo
              tenants are safe.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setTeardownDialog(false)}>
              Cancel
            </Button>
            <Button
              data-testid="demo-teardown-confirm"
              variant="destructive"
              onClick={runTeardown}
            >
              Tear down
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
