"use client";

// Slice 094 — event-type filter sidebar.
//
// Five checkboxes — audit / exception / policy / vendor / control. State
// lives in the parent (URL query string). Clicking a checkbox toggles its
// type in the selection.
//
// Slice 675 added the "Vendor reviews" entry so the legend includes every
// type the agenda surfaces (it previously lacked vendor, even though the
// dashboard "Upcoming" widget listed vendor reviews).

import type { CalendarEventType } from "@/lib/api/calendar";

const TYPE_LABELS: Record<CalendarEventType, { label: string; color: string }> =
  {
    audit: {
      label: "Audit periods",
      color: "bg-blue-500",
    },
    exception: {
      label: "Exception expirations",
      color: "bg-amber-500",
    },
    policy: {
      label: "Policy reviews",
      color: "bg-purple-500",
    },
    vendor: {
      label: "Vendor reviews",
      color: "bg-rose-500",
    },
    control: {
      label: "Control reviews",
      color: "bg-emerald-500",
    },
  };

type Props = {
  selected: readonly string[];
  onToggle: (t: CalendarEventType) => void;
};

export function TypeFilter({ selected, onToggle }: Props) {
  return (
    <div className="rounded-md border bg-card p-4">
      <h2 className="mb-3 text-sm font-semibold">Event types</h2>
      <ul className="space-y-2">
        {Object.entries(TYPE_LABELS).map(([key, meta]) => (
          <li key={key} className="flex items-center gap-2">
            <input
              type="checkbox"
              id={`type-${key}`}
              checked={selected.includes(key)}
              onChange={() => onToggle(key as CalendarEventType)}
              className="h-4 w-4 rounded border-input"
            />
            <span
              aria-hidden
              className={`inline-block h-2 w-2 rounded-full ${meta.color}`}
            />
            <label htmlFor={`type-${key}`} className="text-sm cursor-pointer">
              {meta.label}
            </label>
          </li>
        ))}
      </ul>
    </div>
  );
}
