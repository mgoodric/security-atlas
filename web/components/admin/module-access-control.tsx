"use client";

// #2172 — the module-access control on a member's row in Admin › Users.
//
// Shows the member's module access next to their role and opens a dialog
// to change it: "All modules" (a wildcard that includes modules switched
// on later) or an explicit set of optional modules. Admins and auditors
// are never narrowed, so their rows say so and offer no control. The
// server is the gate (tenant admin; you cannot narrow yourself); the
// dialog shows its refusal verbatim.

import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  accessApplies,
  accessSummary,
  type OptionalModule,
} from "@/lib/admin/module-access";
import { upstreamErrorMessage } from "@/lib/api/_shared";

export function ModuleAccessControl({
  userID,
  userLabel,
  roles,
  access,
  modules,
}: {
  userID: string;
  userLabel: string;
  roles: readonly string[];
  /** null for all modules, else the granted optional-module keys. */
  access: readonly string[] | null | undefined;
  modules: readonly OptionalModule[];
}) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [all, setAll] = useState(true);
  const [picked, setPicked] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: async (body: { modules: string[] | null }) => {
      const res = await fetch(
        `/api/admin/users/${encodeURIComponent(userID)}/module-access`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      if (!res.ok) {
        throw new Error(await upstreamErrorMessage(res));
      }
    },
    onSuccess: async () => {
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: ["admin", "users"] });
    },
    onError: (e: Error) => setError(e.message),
  });

  if (!accessApplies(roles)) {
    return (
      <span
        className="text-xs text-muted-foreground"
        data-testid={`module-access-${userID}`}
      >
        All modules ({roles.includes("admin") ? "admin" : "auditor"})
      </span>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-xs" data-testid={`module-access-${userID}`}>
        {accessSummary(access, modules)}
      </span>
      <Button
        type="button"
        variant="outline"
        size="sm"
        data-testid={`module-access-edit-${userID}`}
        onClick={() => {
          setAll(access === null || access === undefined);
          setPicked(access ? [...access] : []);
          setError(null);
          setOpen(true);
        }}
      >
        Change
      </Button>
      <Dialog open={open} onOpenChange={(o) => !o && setOpen(false)}>
        <DialogPortal>
          <DialogContent data-testid="module-access-dialog">
            <DialogHeader>
              <DialogTitle>Module access</DialogTitle>
              <DialogDescription>
                Where {userLabel} can work. Their role still says what they can
                do. Core modules are always available.
              </DialogDescription>
            </DialogHeader>
            <form
              className="space-y-4"
              onSubmit={(e) => {
                e.preventDefault();
                setError(null);
                save.mutate({ modules: all ? null : picked });
              }}
            >
              <div className="flex items-center gap-2">
                <Checkbox
                  id={`module-access-all-${userID}`}
                  data-testid="module-access-all"
                  checked={all}
                  onCheckedChange={(c) => setAll(c === true)}
                />
                <label
                  htmlFor={`module-access-all-${userID}`}
                  className="text-sm"
                >
                  All modules, including ones switched on later
                </label>
              </div>
              <fieldset className="space-y-2" disabled={all}>
                <legend className="text-xs font-medium text-muted-foreground">
                  Optional modules
                </legend>
                {modules.map((m) => (
                  <div key={m.key} className="flex items-center gap-2">
                    <Checkbox
                      id={`module-access-${userID}-${m.key}`}
                      data-testid={`module-access-module-${m.key}`}
                      checked={all || picked.includes(m.key)}
                      disabled={all}
                      onCheckedChange={(c) =>
                        setPicked((prev) =>
                          c === true
                            ? [...prev.filter((k) => k !== m.key), m.key]
                            : prev.filter((k) => k !== m.key),
                        )
                      }
                    />
                    <label
                      htmlFor={`module-access-${userID}-${m.key}`}
                      className="text-sm"
                    >
                      {m.name}
                    </label>
                  </div>
                ))}
              </fieldset>
              {error ? (
                <Alert variant="destructive" aria-live="polite">
                  <AlertDescription data-testid="module-access-error">
                    {error}
                  </AlertDescription>
                </Alert>
              ) : null}
              <DialogFooter>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => setOpen(false)}
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  data-testid="module-access-save"
                  disabled={save.isPending}
                >
                  {save.isPending ? "Saving…" : "Save"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </div>
  );
}
