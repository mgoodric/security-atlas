// #2094 — the one Markdown component for long operator-authored text
// (policy bodies, risk descriptions, vendor and review notes, action-plan
// descriptions). Rendering and every security property live in
// `web/lib/markdown.ts`; this file only injects that output and styles it.
//
// No typography plugin is installed, so the element styles are written
// here as child selectors rather than a `prose` class.

import { renderMarkdown } from "@/lib/markdown";
import { cn } from "@/lib/utils";

const STYLES = [
  "break-words text-sm text-foreground space-y-3",
  "[&_h1]:text-xl [&_h1]:font-semibold",
  "[&_h2]:text-lg [&_h2]:font-semibold",
  "[&_h3]:text-base [&_h3]:font-semibold",
  "[&_h4]:font-semibold [&_h5]:font-semibold [&_h6]:font-semibold",
  "[&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-0.5",
  "[&_a]:text-primary [&_a]:underline [&_a]:underline-offset-2",
  "[&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:font-mono [&_code]:text-xs",
  "[&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-3",
  "[&_pre_code]:bg-transparent [&_pre_code]:p-0",
  "[&_blockquote]:border-l-4 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-muted-foreground",
  "[&_table]:block [&_table]:max-w-full [&_table]:overflow-x-auto [&_table]:border-collapse",
  "[&_th]:border [&_th]:border-border [&_th]:bg-muted [&_th]:px-2 [&_th]:py-1 [&_th]:text-left [&_th]:font-medium",
  "[&_td]:border [&_td]:border-border [&_td]:px-2 [&_td]:py-1 [&_td]:align-top",
  "[&_hr]:border-border",
].join(" ");

type MarkdownProps = {
  /** Markdown source. Raw HTML in it is shown as text, never rendered. */
  source: string;
  className?: string;
  "data-testid"?: string;
};

export function Markdown({ source, className, ...rest }: MarkdownProps) {
  return (
    <div
      className={cn(STYLES, className)}
      data-testid={rest["data-testid"]}
      // Safe: renderMarkdown escapes the whole input before building any
      // markup and only emits a fixed set of tags plus allow-listed links
      // (see web/lib/markdown.ts).
      dangerouslySetInnerHTML={{ __html: renderMarkdown(source) }}
    />
  );
}
