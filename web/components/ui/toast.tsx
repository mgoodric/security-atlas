"use client";

// #2089 — toast system for the create / update conventions.
//
//   - create -> redirect to the new record, then toast ("Risk created");
//   - update -> stay on the page, then toast;
//   - errors -> inline beside the field or action, never only a toast.
//
// The provider is mounted in the authed layout, which persists across
// client-side navigation, so a toast raised just before `router.push`
// is still showing on the page the user lands on.
//
// Accessibility: the viewport is a polite live region (role="status"),
// so screen readers announce a toast without interrupting. A toast never
// takes focus, and each one has a labelled dismiss button. Toasts
// auto-dismiss after TOAST_DURATION_MS.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

export const TOAST_DURATION_MS = 6000;

type Toast = { id: number; message: string };

type ToastContextValue = {
  toast: (message: string) => void;
};

const ToastContext = createContext<ToastContextValue | null>(null);

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(1);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const dismiss = useCallback((id: number) => {
    const t = timers.current.get(id);
    if (t) clearTimeout(t);
    timers.current.delete(id);
    setToasts((prev) => prev.filter((x) => x.id !== id));
  }, []);

  const toast = useCallback(
    (message: string) => {
      const id = nextId.current++;
      setToasts((prev) => [...prev, { id, message }]);
      timers.current.set(
        id,
        setTimeout(() => dismiss(id), TOAST_DURATION_MS),
      );
    },
    [dismiss],
  );

  useEffect(() => {
    const live = timers.current;
    return () => {
      for (const t of live.values()) clearTimeout(t);
      live.clear();
    };
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        role="status"
        aria-live="polite"
        data-testid="toast-viewport"
        className="pointer-events-none fixed right-4 bottom-12 z-50 flex w-full max-w-sm flex-col gap-2 print:hidden"
      >
        {toasts.map((t) => (
          <div
            key={t.id}
            data-testid="toast"
            className="pointer-events-auto flex items-start justify-between gap-3 rounded-md border bg-background px-4 py-3 text-sm text-foreground shadow-lg"
          >
            <span>{t.message}</span>
            <button
              type="button"
              aria-label="Dismiss notification"
              data-testid="toast-dismiss"
              onClick={() => dismiss(t.id)}
              className="-mr-1 rounded-sm px-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <span aria-hidden="true">×</span>
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

/** useToast returns the toast trigger. Outside a provider it is a no-op,
 * so a component reused off the authed shell still renders. */
export function useToast(): ToastContextValue {
  return useContext(ToastContext) ?? NOOP;
}

const NOOP: ToastContextValue = { toast: () => {} };
