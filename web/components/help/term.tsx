"use client";

// #2144 — <Term>: a glossary term in the UI, with its short definition on
// hover, keyboard focus or tap, and a link to the full entry on the docs
// Terms page.
//
// Accessibility:
//   - the term is a real `<button>`, so it is focusable and tappable; its
//     accessible name is the visible text, and `aria-describedby` points at
//     the short definition, so a screen reader reads the definition on
//     focus without opening anything;
//   - the tooltip sits right after the button in DOM order, so Tab from the
//     term reaches the "Read more" link while it is open; focus leaving the
//     pair closes it;
//   - Escape closes it, and nothing here ever moves focus;
//   - a second click or tap on an open term closes it.
//
// The popup is `position: fixed`, placed from the button's rect, so a
// scrolling ancestor (the risk register's table wrapper) never clips it.
//
// Every `id` must exist in `web/help/terms.json`; `lib/help/terms.test.ts`
// fails the build otherwise. An unknown id at runtime renders the text
// without a tooltip rather than throwing.

import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { getTerm, termHref } from "@/lib/help/terms";
import { cn } from "@/lib/utils";

const POPUP_WIDTH = 288; // px; matches w-72
const GAP = 6; // px between the term and the popup
const EDGE = 8; // px kept clear of the viewport edge
const HOVER_CLOSE_DELAY = 150; // ms, so the pointer can cross the gap

type Position = { top: number; left: number };

export function Term({
  id,
  children,
  className,
}: {
  /** A term id from `web/help/terms.json`, as a string literal. */
  id: string;
  children: ReactNode;
  className?: string;
}) {
  const term = getTerm(id);
  const baseId = useId();
  const defId = `${baseId}-def`;

  const wrapRef = useRef<HTMLSpanElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const popupRef = useRef<HTMLSpanElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [pinned, setPinned] = useState(false);
  // Escape (or a closing click) wins over hover and focus until the pointer
  // leaves or focus moves away, so the popup doesn't reopen under the user.
  const [dismissed, setDismissed] = useState(false);
  const [pos, setPos] = useState<Position | null>(null);

  const open = !dismissed && (hovered || focused || pinned);

  const place = useCallback(() => {
    const btn = buttonRef.current;
    if (!btn) return;
    const r = btn.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const width = Math.min(POPUP_WIDTH, vw - 2 * EDGE);
    const left = Math.max(EDGE, Math.min(r.left, vw - width - EDGE));
    const height = popupRef.current?.offsetHeight ?? 0;
    const below = r.bottom + GAP;
    const top =
      below + height > vh - EDGE && r.top - GAP - height >= EDGE
        ? r.top - GAP - height
        : below;
    setPos({ top, left });
  }, []);

  useLayoutEffect(() => {
    if (!open) return;
    place();
  }, [open, place]);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setDismissed(true);
        setPinned(false);
      }
    };
    const onPointerDown = (e: PointerEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setPinned(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open, place]);

  useEffect(
    () => () => {
      if (closeTimer.current) clearTimeout(closeTimer.current);
    },
    [],
  );

  if (!term) return <>{children}</>;

  return (
    <span
      ref={wrapRef}
      className="inline"
      data-testid={`term-${id}`}
      onMouseEnter={() => {
        if (closeTimer.current) clearTimeout(closeTimer.current);
        setHovered(true);
      }}
      onMouseLeave={() => {
        closeTimer.current = setTimeout(() => {
          setHovered(false);
          setDismissed(false);
        }, HOVER_CLOSE_DELAY);
      }}
      onFocus={() => setFocused(true)}
      onBlur={(e) => {
        if (!wrapRef.current?.contains(e.relatedTarget as Node | null)) {
          setFocused(false);
          setDismissed(false);
        }
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        aria-describedby={defId}
        data-testid={`term-${id}-trigger`}
        onClick={() => {
          if (open && pinned) {
            setPinned(false);
            setDismissed(true);
          } else {
            setPinned(true);
            setDismissed(false);
          }
        }}
        className={cn(
          "inline cursor-help rounded-sm underline decoration-dotted decoration-1 underline-offset-[3px] decoration-current/50 hover:decoration-current focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          className,
        )}
      >
        {children}
      </button>
      <span
        ref={popupRef}
        hidden={!open}
        data-testid={`term-${id}-popup`}
        style={
          pos
            ? { top: pos.top, left: pos.left }
            : { top: 0, left: 0, visibility: "hidden" }
        }
        className="fixed z-50 block w-72 max-w-[calc(100vw-16px)] rounded-md border bg-popover p-3 text-left text-xs font-normal tracking-normal normal-case text-popover-foreground shadow-md"
      >
        <span className="block font-medium text-foreground">{term.name}</span>
        <span id={defId} role="tooltip" className="mt-1 block leading-relaxed">
          {term.short}
        </span>
        <a
          href={termHref(id)}
          target="_blank"
          rel="noopener noreferrer"
          data-testid={`term-${id}-link`}
          className="mt-2 inline-block text-primary underline underline-offset-2 hover:no-underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-sm"
        >
          Read the full entry
          <span className="sr-only"> for {term.name} (opens in a new tab)</span>
        </a>
      </span>
    </span>
  );
}
