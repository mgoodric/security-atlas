"use client";

// Slice 097 — shadcn-themed Dialog primitive.
//
// Wraps the @base-ui/react Dialog parts (Root / Trigger / Portal /
// Backdrop / Popup / Title / Description / Close) with the project's
// Tailwind theme tokens. Matches the shape of the other ui/* primitives:
// minimal pass-through over the upstream lib, plus className defaults.

import { Dialog as Base } from "@base-ui/react/dialog";
import * as React from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export const Dialog = Base.Root;
export const DialogTrigger = Base.Trigger;
export const DialogClose = Base.Close;

export function DialogPortal({ children }: { children: React.ReactNode }) {
  return (
    <Base.Portal>
      <Base.Backdrop
        className={cn(
          "fixed inset-0 z-50 bg-foreground/40 backdrop-blur-[1px]",
          "data-[state=open]:animate-in data-[state=closed]:animate-out",
        )}
      />
      {children}
    </Base.Portal>
  );
}

export function DialogContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof Base.Popup>) {
  return (
    <Base.Popup
      data-slot="dialog-content"
      className={cn(
        "fixed top-1/2 left-1/2 z-50 -translate-x-1/2 -translate-y-1/2",
        "w-full max-w-md rounded-xl bg-card p-6 text-card-foreground",
        "ring-1 ring-foreground/10 shadow-lg",
        "focus-visible:outline-none",
        className,
      )}
      {...props}
    >
      {children}
    </Base.Popup>
  );
}

export function DialogHeader({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-1.5", className)}
      {...props}
    />
  );
}

export function DialogFooter({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn("mt-6 flex justify-end gap-2", className)}
      {...props}
    />
  );
}

export function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof Base.Title>) {
  return (
    <Base.Title
      data-slot="dialog-title"
      className={cn("text-lg font-semibold tracking-tight", className)}
      {...props}
    />
  );
}

export function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof Base.Description>) {
  return (
    <Base.Description
      data-slot="dialog-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

// Convenience: cancel button that closes the dialog. The native
// `DialogClose` is a render-prop, so callers can compose freely too.
export function DialogCancelButton({
  children = "Cancel",
}: {
  children?: React.ReactNode;
}) {
  return (
    <DialogClose
      render={(props) => (
        <Button {...props} variant="outline" type="button">
          {children}
        </Button>
      )}
    />
  );
}
