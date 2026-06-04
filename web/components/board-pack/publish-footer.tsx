// Slice 043 — publish footer (per Plans/_archive/mockups/board-pack.html footer).
//
// The publish button is disabled until every fixed section is approved
// (AC-6 + slice 032 decision D6). The platform enforces the gate too —
// the UI gate is defense-in-depth + a clearer affordance ("not ready"
// banner names the unapproved sections).
//
// A published pack shows the frozen state — the approver name and a
// note that the artifact is immutable (AC-7).

"use client";

import { useMutation } from "@tanstack/react-query";
import { useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { APIError } from "@/lib/api/base";
import { BoardPack, publishBoardPack } from "@/lib/api/board";

type PublishFooterProps = {
  packID: string;
  isPublished: boolean;
  allApproved: boolean;
  canApprove: boolean;
  publishedBy?: string;
  publishedAt?: string;
  unapprovedTitles: string[];
  onPublished: (pack: BoardPack) => void;
};

export function PublishFooter({
  packID,
  isPublished,
  allApproved,
  canApprove,
  publishedBy,
  publishedAt,
  unapprovedTitles,
  onPublished,
}: PublishFooterProps) {
  const [publisher, setPublisher] = useState("");

  const publish = useMutation({
    mutationFn: () => publishBoardPack(packID, publisher),
    onSuccess: onPublished,
  });

  if (isPublished) {
    return (
      <Card
        id="publish-footer"
        className="border-emerald-200"
        data-testid="publish-footer-published"
      >
        <CardHeader>
          <CardTitle>Published</CardTitle>
          <CardDescription>
            This pack is frozen and immutable. Published by{" "}
            <span className="font-medium">{publishedBy || "unknown"}</span>
            {publishedAt ? ` on ${publishedAt}` : ""}.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  if (!canApprove) {
    return (
      <Card id="publish-footer" data-testid="publish-footer-readonly">
        <CardHeader>
          <CardTitle>Publish</CardTitle>
          <CardDescription>
            Publishing this pack requires the approver role.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card
      id="publish-footer"
      className="border-indigo-200"
      data-testid="publish-footer-draft"
    >
      <CardHeader>
        <CardTitle>Approve and publish</CardTitle>
        <CardDescription>
          Publishing freezes the pack at the current data snapshot. Every
          section must be approved first.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {!allApproved && (
          <Alert data-testid="publish-not-ready">
            <AlertTitle>Not ready to publish</AlertTitle>
            <AlertDescription>
              Approve the following section
              {unapprovedTitles.length === 1 ? "" : "s"} above before
              publishing: {unapprovedTitles.join(", ")}.
            </AlertDescription>
          </Alert>
        )}
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (publisher) publish.mutate();
          }}
        >
          <div className="space-y-1">
            <label className="text-sm font-medium text-slate-700">
              Approver name
            </label>
            <Input
              value={publisher}
              placeholder="e.g. Sam Rivera (CISO)"
              onChange={(e) => setPublisher(e.target.value)}
              className="w-64"
              data-testid="publish-approver-input"
            />
          </div>
          <Button
            type="submit"
            disabled={!allApproved || !publisher || publish.isPending}
            data-testid="publish-submit"
          >
            {publish.isPending ? "Publishing…" : "Approve & publish"}
          </Button>
        </form>
        {publish.isError && (
          <Alert variant="destructive">
            <AlertTitle>Publish failed</AlertTitle>
            <AlertDescription>
              {publish.error instanceof APIError
                ? publish.error.message
                : "Unexpected error."}
            </AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
