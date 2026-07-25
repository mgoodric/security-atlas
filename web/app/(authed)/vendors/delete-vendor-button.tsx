"use client";

import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPortal,
  DialogTitle,
} from "@/components/ui/dialog";

import { deleteVendorFromCookieSession } from "./actions";

// Slice 679 (ATLAS-031). The Edit-vendor copy promises "Delete removes
// the row and its cell bindings" but the page only had "Save changes".
// This is the Delete control the copy commits to.
//
// Anti-criterion (slice 679): Delete MUST confirm before firing — it is
// a destructive mutation. The confirm lives in a Dialog; the actual
// DELETE only fires from the dialog's confirm button, never from the
// trigger. Cell-binding cleanup is handled server-side by the platform's
// CASCADE on vendor_scope_cells (the backend delete already exists and
// runs under the tenant RLS context).

type Props = {
  vendorId: string;
  vendorName: string;
  onDeleted: () => void;
};

export function DeleteVendorButton({ vendorId, vendorName, onDeleted }: Props) {
  const [open, setOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleConfirm() {
    setDeleting(true);
    setError(null);
    try {
      await deleteVendorFromCookieSession(vendorId);
      setOpen(false);
      onDeleted();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setDeleting(false);
    }
  }

  return (
    <>
      <Button
        type="button"
        variant="destructive"
        onClick={() => setOpen(true)}
        data-testid="vendor-delete-trigger"
      >
        Delete vendor
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogPortal>
          <DialogContent data-testid="vendor-delete-dialog">
            <DialogHeader>
              <DialogTitle>Delete this vendor?</DialogTitle>
              <DialogDescription>
                This removes <span className="font-medium">{vendorName}</span>{" "}
                and its scope-cell bindings. This cannot be undone.
              </DialogDescription>
            </DialogHeader>
            {error ? (
              <Alert variant="destructive">
                <AlertTitle>Could not delete vendor</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={deleting}
                onClick={() => setOpen(false)}
              >
                Cancel
              </Button>
              <Button
                type="button"
                variant="destructive"
                disabled={deleting}
                onClick={handleConfirm}
                data-testid="vendor-delete-confirm"
              >
                {deleting ? "Deleting..." : "Delete vendor"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </DialogPortal>
      </Dialog>
    </>
  );
}
