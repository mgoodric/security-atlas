"use client";

// #2213 — the top bar's account menu below `lg` (1,024px).
//
// From `lg` up the bar shows the avatar, the display name and a Sign out
// button inline. That cluster is ~250px, which a 390px phone or a 768px
// tablet (with the sidebar showing) cannot spare, so below `lg` it folds
// into this one button: the initials (or a person glyph when `/v1/me`
// failed, so Sign out is never lost with the avatar) open a panel with
// the name and the Sign out form.
//
// A disclosure, not an ARIA menu: the panel holds a form button, not
// `menuitem`s, so the button carries `aria-expanded` + `aria-controls`
// and Tab moves through the panel as normal. Esc closes it and returns
// focus to the button; so does a click outside.
//
// The Sign out form arrives as `children` because it binds a server
// action, which the server-rendered top bar owns.

import { useEffect, useId, useRef, useState, type ReactNode } from "react";

import { cn } from "@/lib/utils";

export function AccountMenu({
  name,
  initials,
  className,
  children,
}: {
  name: string | null;
  initials: string | null;
  className?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!containerRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        buttonRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div
      ref={containerRef}
      data-testid="account-menu"
      className={cn("relative", className)}
    >
      <button
        ref={buttonRef}
        type="button"
        data-testid="account-menu-trigger"
        aria-label={name ? `Account: ${name}` : "Account"}
        aria-expanded={open}
        aria-controls={panelId}
        onClick={() => setOpen((v) => !v)}
        className="inline-flex h-9 w-9 items-center justify-center rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
      >
        <span
          aria-hidden
          className="flex h-7 w-7 items-center justify-center rounded-full bg-primary/15 text-xs font-semibold text-primary"
        >
          {initials ?? <PersonIcon />}
        </span>
      </button>
      <div
        id={panelId}
        data-testid="account-menu-panel"
        hidden={!open}
        className="absolute right-0 top-full z-50 mt-1 w-56 max-w-[calc(100vw-1rem)] rounded-md border bg-popover p-2 text-popover-foreground shadow-md"
      >
        {name ? (
          <div
            data-testid="account-menu-name"
            className="truncate px-2 py-1.5 text-sm font-medium"
          >
            {name}
          </div>
        ) : null}
        <div className={cn(name && "mt-1 border-t pt-1")}>{children}</div>
      </div>
    </div>
  );
}

function PersonIcon() {
  return (
    <svg className="h-4 w-4" viewBox="0 0 20 20" fill="currentColor">
      <path d="M10 10a4 4 0 100-8 4 4 0 000 8zm-7 8a7 7 0 0114 0H3z" />
    </svg>
  );
}
