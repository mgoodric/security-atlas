"use client";

// Slice 277 — Sheet (slide-out drawer) primitive.
//
// Composes the @base-ui/react Dialog parts (Root / Portal / Backdrop /
// Popup / Title / Description / Close) into a side-anchored drawer.
// Parallels the slice 097 `Dialog` primitive at
// `web/components/ui/dialog.tsx`; the only structural difference is the
// `<Popup>`'s positional classes (anchored to a side edge with a
// translate-X transition) instead of centered with a fade.
//
// Why we build the Sheet here rather than `npx shadcn add sheet`: the
// project's shadcn-themed primitives wrap `@base-ui/react`, not Radix.
// The official shadcn `<Sheet>` component sources from Radix's
// `@radix-ui/react-dialog`, which would be a new top-level dep — and
// slice 277 P0-277-8 explicitly forbids adding a new dep. Building on
// `@base-ui/react/dialog` (already used by `dialog.tsx`) is the
// idiomatic-to-this-codebase path. See decisions log D1.
//
// API surface mirrors a shadcn `<Sheet>` (open/onOpenChange root,
// SheetTrigger, SheetContent, SheetHeader, SheetTitle,
// SheetDescription, SheetClose) so callers read familiarly. The side
// prop on SheetContent controls which edge the drawer slides from
// (default "left" matches the sidebar's anchor; "right"/"top"/"bottom"
// are supported for future use).
//
// Accessibility: @base-ui/react Dialog implements the WAI-ARIA dialog
// pattern — Escape closes the dialog, focus traps to the popup while
// open, focus restores to the trigger on close, scroll lock on the
// document. Slice 277 AC-6 (clicking outside closes) and AC-7 (Escape
// closes; Tab cycles through nav items; first nav item receives focus
// on open) ride on these built-ins; the spec is verified by the
// Playwright mobile-baseline spec.

import { Dialog as Base } from "@base-ui/react/dialog";
import * as React from "react";

import { cn } from "@/lib/utils";

export const Sheet = Base.Root;
export const SheetTrigger = Base.Trigger;
export const SheetClose = Base.Close;

export function SheetPortal({ children }: { children: React.ReactNode }) {
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

type SheetSide = "left" | "right" | "top" | "bottom";

/**
 * positionalClasses returns the Tailwind class string that anchors the
 * SheetContent popup to a viewport edge and sets its slide-in / slide-
 * out transform. The left/right variants are the load-bearing pair for
 * slice 277 (sidebar drawer); top/bottom are included for future use
 * (e.g., a mobile bottom-sheet for filters).
 */
function positionalClasses(side: SheetSide): string {
  switch (side) {
    case "left":
      return "fixed inset-y-0 left-0 h-full w-72 max-w-[85vw] border-r";
    case "right":
      return "fixed inset-y-0 right-0 h-full w-72 max-w-[85vw] border-l";
    case "top":
      return "fixed inset-x-0 top-0 w-full max-h-[85vh] border-b";
    case "bottom":
      return "fixed inset-x-0 bottom-0 w-full max-h-[85vh] border-t";
  }
}

export function SheetContent({
  className,
  side = "left",
  children,
  ...props
}: React.ComponentProps<typeof Base.Popup> & { side?: SheetSide }) {
  return (
    <Base.Popup
      data-slot="sheet-content"
      className={cn(
        "z-50 flex flex-col gap-4 bg-background p-6 text-foreground",
        "ring-1 ring-foreground/10 shadow-lg",
        "focus-visible:outline-none",
        positionalClasses(side),
        className,
      )}
      {...props}
    >
      {children}
    </Base.Popup>
  );
}

export function SheetHeader({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="sheet-header"
      className={cn("flex flex-col gap-1.5", className)}
      {...props}
    />
  );
}

export function SheetTitle({
  className,
  ...props
}: React.ComponentProps<typeof Base.Title>) {
  return (
    <Base.Title
      data-slot="sheet-title"
      className={cn("text-base font-semibold tracking-tight", className)}
      {...props}
    />
  );
}

export function SheetDescription({
  className,
  ...props
}: React.ComponentProps<typeof Base.Description>) {
  return (
    <Base.Description
      data-slot="sheet-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}
