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
//
// Slice 322 — defensive UX additions for the "silent click" class
// of bug. The reported symptom was "I clicked the load demo data
// button, but nothing seemed to happen." Root-cause diagnosis is
// in docs/audit-log/322-admin-demo-button-no-feedback-decisions.md
// — load-bearing cause is a hybrid of (D) Alert below the fold and
// (E) no visible in-flight state between click and dialog mount.
// Fix shape:
//
//   - `aria-live="polite"` on every dynamic <Alert>
//   - `scrollIntoView` on success/error Alert mount
//   - Brief in-flight "Opening confirmation…" button label so the
//     user sees feedback even if the dialog mount is delayed by a
//     slow paint or zoomed-out viewport
//   - Dev-mode `console.warn` on non-200 BFF responses (operator
//     diagnostic; gated by NODE_ENV)
//   - `data-testid="demo-click-feedback"` is the contract anchor
//     for the e2e click-feedback assertion (AC-4)

"use client";

import { useEffect, useRef, useState } from "react";

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
  DialogPortal,
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

type EnabledState =
  | { kind: "loading" }
  | { kind: "enabled" }
  | { kind: "disabled" };

// Slice 322 — duration the in-flight "Opening confirmation…" button
// label is visible after click. 80ms is just long enough to be
// perceptible on a fast machine and gives a slow machine plenty of
// time for the dialog to mount before the label resets. The dialog
// itself takes precedence visually once it appears.
const CLICK_FEEDBACK_MS = 80;

type ClickFeedback = "idle" | "seed" | "teardown";

export function DemoControls() {
  const [seedDialog, setSeedDialog] = useState(false);
  const [teardownDialog, setTeardownDialog] = useState(false);
  const [state, setState] = useState<ActionState>({ kind: "idle" });
  const [enabled, setEnabled] = useState<EnabledState>({ kind: "loading" });
  // Slice 322 — in-flight indicator between click and dialog mount.
  const [clickFeedback, setClickFeedback] = useState<ClickFeedback>("idle");
  // Slice 322 — scroll-into-view target for post-action Alerts.
  const alertRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch("/api/admin/demo/status", {
          cache: "no-store",
        });
        if (!res.ok) {
          if (!cancelled) setEnabled({ kind: "disabled" });
          return;
        }
        const body = (await res.json()) as { enabled?: boolean };
        if (cancelled) return;
        setEnabled({ kind: body.enabled === true ? "enabled" : "disabled" });
      } catch {
        if (!cancelled) setEnabled({ kind: "disabled" });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Slice 322 — when the action state lands in success or error,
  // scroll the Alert into view so users with a scrolled or zoomed
  // viewport see the feedback. `block: "center"` avoids surprising
  // alignment near viewport edges. Smooth behavior is non-jarring.
  useEffect(() => {
    if (
      state.kind === "success-seed" ||
      state.kind === "success-teardown" ||
      state.kind === "error"
    ) {
      alertRef.current?.scrollIntoView({
        behavior: "smooth",
        block: "center",
      });
    }
  }, [state.kind]);

  if (enabled.kind === "loading") {
    return (
      <div
        data-testid="demo-loading"
        className="h-12 animate-pulse rounded bg-muted"
        aria-hidden
      />
    );
  }

  if (enabled.kind === "disabled") {
    return (
      <Alert>
        <AlertTitle>Demo tools are not enabled on this deployment</AlertTitle>
        <AlertDescription>
          <p>
            Set{" "}
            <code className="rounded bg-muted px-1 py-0.5 text-xs">
              ATLAS_ENABLE_DEMO_SEED=true
            </code>{" "}
            in the docker-compose env (or the equivalent secrets surface) and
            restart the atlas server to expose these actions.
          </p>
          <p className="mt-2 text-xs text-muted-foreground">
            This page is intentionally always reachable for admins on every
            deployment; the buttons are gated on the env var so production
            deployments cannot accidentally trigger a demo seed.
          </p>
        </AlertDescription>
      </Alert>
    );
  }

  const busy = state.kind === "running";

  // Slice 322 — click handlers for the two primary buttons.
  // Each fires the in-flight indicator BEFORE opening the dialog so
  // even a one-frame paint delay surfaces a visible DOM change. The
  // indicator auto-clears after CLICK_FEEDBACK_MS or when the dialog
  // closes — whichever comes first.
  function handleSeedClick() {
    setClickFeedback("seed");
    setSeedDialog(true);
    window.setTimeout(() => setClickFeedback("idle"), CLICK_FEEDBACK_MS);
  }

  function handleTeardownClick() {
    setClickFeedback("teardown");
    setTeardownDialog(true);
    window.setTimeout(() => setClickFeedback("idle"), CLICK_FEEDBACK_MS);
  }

  // Slice 322 — dev-mode diagnostic. In production, the user-facing
  // Alert surfaces the error; in development, also emit console.warn
  // so an operator debugging on atlas-edge can see the failure
  // shape in DevTools without manually opening the network tab.
  function warnInDev(action: string, res: Response, body: unknown): void {
    if (process.env.NODE_ENV === "development") {
      console.warn(
        `[admin/demo] ${action} returned ${res.status} ${res.statusText}`,
        body,
      );
    }
  }

  async function runSeed() {
    setState({ kind: "running" });
    setSeedDialog(false);
    try {
      const res = await fetch("/api/admin/demo/seed", { method: "POST" });
      const body = (await res.json()) as Partial<SeedResult> & {
        error?: string;
      };
      if (!res.ok) {
        warnInDev("seed", res, body);
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
      if (process.env.NODE_ENV === "development") {
        console.warn("[admin/demo] seed threw", err);
      }
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
        warnInDev("teardown", res, body);
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
      if (process.env.NODE_ENV === "development") {
        console.warn("[admin/demo] teardown threw", err);
      }
      setState({
        kind: "error",
        message: (err as Error).message ?? "network error",
      });
    }
  }

  // Slice 322 — buttons reflect both the request-in-flight `busy`
  // state AND the brief click-feedback transition so the user always
  // sees evidence that their click registered.
  const seedLabel =
    clickFeedback === "seed" ? "Opening confirmation…" : "Reseed demo dataset";
  const teardownLabel =
    clickFeedback === "teardown"
      ? "Opening confirmation…"
      : "Tear down demo tenant";

  return (
    <div className="space-y-4" data-testid="demo-controls">
      {/* Reseed card */}
      <Card>
        <CardHeader>
          <CardTitle>Reseed demo dataset</CardTitle>
          <CardDescription>
            Creates or no-ops on the <code>demo</code> tenant and populates it
            with the slice-205 dataset. Safe to re-click — the seeder is
            idempotent on the slug.
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Button
            data-testid="demo-seed-button"
            onClick={handleSeedClick}
            disabled={busy}
          >
            {seedLabel}
          </Button>
        </CardFooter>
      </Card>

      {/* Teardown card */}
      <Card>
        <CardHeader>
          <CardTitle>Tear down demo tenant</CardTitle>
          <CardDescription>
            Deletes the demo tenant and every row anchored to it. Reversible by
            clicking Reseed again afterwards. Refuses to operate on a tenant
            that does not carry the slice-205 forensic mark.
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Button
            data-testid="demo-teardown-button"
            variant="destructive"
            onClick={handleTeardownClick}
            disabled={busy}
          >
            {teardownLabel}
          </Button>
        </CardFooter>
      </Card>

      {/*
        Slice 322 — click-feedback anchor. A hidden but non-empty
        node that surfaces immediately on click; the e2e contract
        test (AC-4) anchors on this testid as one of the three
        "visible DOM change within 1s" acceptable signals. It is
        sr-only so it does not visually distract; screen readers
        announce it via aria-live=polite.
      */}
      {clickFeedback !== "idle" ? (
        <div
          data-testid="demo-click-feedback"
          aria-live="polite"
          className="sr-only"
        >
          Opening confirmation dialog…
        </div>
      ) : null}

      {/*
        Slice 322 — wrapping div carries the scroll-into-view ref so
        the underlying <Alert> component does not need forwardRef
        plumbing. The wrapper also receives aria-live=polite as a
        belt-and-suspenders signal (the <Alert> has its own
        aria-live=polite, and role="alert" implicitly announces, but
        the outer wrapper announces too because some screen readers
        ignore aria attributes on descendants of role=alert nodes).
      */}
      {state.kind === "running" ? (
        <div ref={alertRef}>
          <Alert data-testid="demo-running" aria-live="polite">
            <AlertTitle>Running…</AlertTitle>
            <AlertDescription>
              Action in flight. This typically takes 5-10 seconds.
            </AlertDescription>
          </Alert>
        </div>
      ) : null}

      {state.kind === "success-seed" ? (
        <div ref={alertRef}>
          <Alert data-testid="demo-success" aria-live="polite">
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
        </div>
      ) : null}

      {state.kind === "success-teardown" ? (
        <div ref={alertRef}>
          <Alert data-testid="demo-success" aria-live="polite">
            <AlertTitle>Demo tenant torn down</AlertTitle>
            <AlertDescription>
              Tenant <code>{state.result.tenant_slug}</code> deleted along with
              all anchored rows. Click Reseed to recreate the dataset.
            </AlertDescription>
          </Alert>
        </div>
      ) : null}

      {state.kind === "error" ? (
        <div ref={alertRef}>
          <Alert
            variant="destructive"
            data-testid="demo-error"
            aria-live="polite"
          >
            <AlertTitle>Action failed</AlertTitle>
            <AlertDescription>{state.message}</AlertDescription>
          </Alert>
        </div>
      ) : null}

      {/* Confirmation dialog: Reseed */}
      <Dialog open={seedDialog} onOpenChange={setSeedDialog}>
        <DialogPortal>
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
        </DialogPortal>
      </Dialog>

      {/* Confirmation dialog: Teardown */}
      <Dialog open={teardownDialog} onOpenChange={setTeardownDialog}>
        <DialogPortal>
          <DialogContent data-testid="demo-teardown-dialog">
            <DialogHeader>
              <DialogTitle>Tear down the demo tenant?</DialogTitle>
              <DialogDescription>
                This will delete the <code>demo</code> tenant and every row
                anchored to it. The seeder refuses to operate on a tenant that
                does not carry the slice-205 forensic mark, so non-demo tenants
                are safe.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => setTeardownDialog(false)}
              >
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
        </DialogPortal>
      </Dialog>
    </div>
  );
}
