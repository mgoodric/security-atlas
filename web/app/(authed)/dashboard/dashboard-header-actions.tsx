"use client";

import { useMutation, useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button";
import { APIError } from "@/lib/api/base";
import { generateBoardPack } from "@/lib/api/board";
import { getSessionMe } from "@/lib/api/board";
import { cn } from "@/lib/utils";

type DashboardHeaderActionsProps = {
  disabled: boolean;
};

const EXPORT_ROLES = new Set(["admin", "grc_engineer"]);

function todayUTC(): string {
  return new Date().toISOString().slice(0, 10);
}

export function DashboardHeaderActions({
  disabled,
}: DashboardHeaderActionsProps) {
  const router = useRouter();
  const meQuery = useQuery({
    queryKey: ["session-me", "dashboard-actions"],
    queryFn: getSessionMe,
  });

  const roles = meQuery.data?.roles ?? [];
  const isAdmin = meQuery.data?.is_admin === true || roles.includes("admin");
  const canExport =
    isAdmin || roles.some((role) => EXPORT_ROLES.has(role.toLowerCase()));
  const canGenerateBoardPack = isAdmin;

  const generate = useMutation({
    mutationFn: () => generateBoardPack(todayUTC()),
    onSuccess: (pack) => {
      router.push(`/board-packs/${encodeURIComponent(pack.id)}`);
    },
  });

  if (!canExport && !canGenerateBoardPack) {
    return null;
  }

  const disabledReason = disabled
    ? "Snapshot incomplete - refresh panels first"
    : undefined;

  return (
    <div
      className="flex items-center gap-2"
      data-testid="dashboard-header-actions"
    >
      {canExport && !disabled && (
        <details className="relative" data-testid="dashboard-export-menu">
          <summary
            className={cn(
              buttonVariants({ variant: "outline", size: "default" }),
              "cursor-pointer list-none [&::-webkit-details-marker]:hidden",
            )}
            data-testid="dashboard-export-trigger"
          >
            Export
          </summary>
          <div className="absolute right-0 z-20 mt-2 w-40 rounded-md border bg-background p-1 shadow-md">
            {[
              ["json", "JSON"],
              ["csv", "CSV"],
              ["xlsx", "XLSX"],
            ].map(([format, label]) => (
              <a
                key={format}
                className="block rounded px-2 py-1.5 text-sm text-foreground hover:bg-muted"
                data-testid={`dashboard-export-${format}`}
                href={`/api/dashboard/export?format=${format}`}
              >
                {label}
              </a>
            ))}
          </div>
        </details>
      )}
      {canExport && disabled && (
        <Button
          type="button"
          variant="outline"
          disabled
          title={disabledReason}
          data-testid="dashboard-export-disabled"
        >
          Export
        </Button>
      )}
      {canGenerateBoardPack && (
        <Button
          type="button"
          disabled={disabled || generate.isPending}
          title={disabledReason}
          onClick={() => generate.mutate()}
          data-testid="dashboard-new-board-report"
        >
          {generate.isPending ? "Generating..." : "New board report"}
        </Button>
      )}
      {generate.isError && (
        <span
          className="text-sm text-destructive"
          role="alert"
          data-testid="dashboard-new-board-report-error"
        >
          {generate.error instanceof APIError
            ? generate.error.message
            : "Could not generate board report."}
        </span>
      )}
    </div>
  );
}
