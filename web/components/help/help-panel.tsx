"use client";

// #2143 — the "How this works" panel: PageShell's **?** action.
//
// The button renders only on a page some module's help covers
// (`helpModuleForPath`); every other page shows no **?**. It opens a
// right-hand Sheet with the module's help (through the shared <Markdown>
// renderer), its key terms from `web/help/terms.json`, any diagrams, and
// "Read more" to the module's page on the docs site.
//
// Accessibility rides on the base-ui Dialog under <Sheet>: it is a modal
// dialog (`aria-modal`), focus moves into it on open and is trapped
// there, Escape closes it, and focus returns to the **?** button on
// close. The Playwright spec `e2e/help-panel.spec.ts` checks each of
// these and runs axe on the open panel.
//
// Diagrams are served as <img src> from `web/public/help/diagrams/`, never
// inlined as SVG markup.

import { usePathname } from "next/navigation";
import {
  createContext,
  useContext,
  useId,
  useState,
  type ReactNode,
} from "react";

import { Markdown } from "@/components/markdown";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetHeader,
  SheetPortal,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import {
  helpDiagramSrc,
  helpDocsHref,
  helpModuleForPath,
  type HelpModule,
} from "@/lib/help/modules";
import { getTerm, termHref } from "@/lib/help/terms";
import { cn } from "@/lib/utils";

const LINK =
  "rounded-sm text-primary underline underline-offset-2 hover:no-underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring";

type HelpState = {
  mod: HelpModule | undefined;
  open: boolean;
  setOpen: (open: boolean) => void;
};

const HelpContext = createContext<HelpState | null>(null);

/** #2145 — PageShell's help scope: the page's help module and whether its
 *  panel is open, shared by the **?** button and the first-visit hint
 *  (`onboarding-hint.tsx`), which opens the same panel. */
export function HelpScope({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const mod = pathname ? helpModuleForPath(pathname) : undefined;
  const [open, setOpen] = useState(false);
  return (
    <HelpContext.Provider value={{ mod, open, setOpen }}>
      {children}
    </HelpContext.Provider>
  );
}

/** The enclosing HelpScope's state; no module outside one. */
export function useHelp(): HelpState {
  return (
    useContext(HelpContext) ?? {
      mod: undefined,
      open: false,
      setOpen: () => {},
    }
  );
}

export function HelpButton() {
  const { mod, open, setOpen } = useHelp();
  const termsHeadingId = useId();

  if (!mod) return null;

  const terms = mod.terms.flatMap((id) => {
    const t = getTerm(id);
    return t ? [t] : [];
  });

  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger
        data-testid="help-button"
        aria-label={`How this works: ${mod.title}`}
        title="How this works"
        className={cn(
          "inline-flex size-8 shrink-0 items-center justify-center rounded-full border border-border bg-background text-sm font-semibold text-muted-foreground",
          "hover:bg-muted hover:text-foreground aria-expanded:bg-muted aria-expanded:text-foreground",
          "focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50",
        )}
      >
        <span aria-hidden="true">?</span>
      </SheetTrigger>
      <SheetPortal>
        <SheetContent
          side="right"
          aria-modal="true"
          data-testid="help-panel"
          className="w-[28rem] max-w-[92vw] gap-5 overflow-y-auto"
        >
          <SheetHeader className="flex-row items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                How this works
              </p>
              <SheetTitle className="text-lg">{mod.title}</SheetTitle>
            </div>
            <SheetClose
              data-testid="help-panel-close"
              aria-label="Close help"
              className="inline-flex size-8 shrink-0 items-center justify-center rounded-md text-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
            >
              <span aria-hidden="true">×</span>
            </SheetClose>
          </SheetHeader>

          <Markdown source={mod.body} data-testid="help-panel-body" />

          {mod.diagrams.length > 0 ? (
            <div className="space-y-3" data-testid="help-panel-diagrams">
              {mod.diagrams.map((d) => (
                // eslint-disable-next-line @next/next/no-img-element -- a static SVG from public/, not a photo to optimise
                <img
                  key={d.src}
                  src={helpDiagramSrc(d.src)}
                  alt={d.alt}
                  className="w-full rounded-md border border-border bg-background"
                />
              ))}
            </div>
          ) : null}

          {terms.length > 0 ? (
            <section
              aria-labelledby={termsHeadingId}
              data-testid="help-panel-terms"
            >
              <h3 id={termsHeadingId} className="text-sm font-semibold">
                Key terms
              </h3>
              <dl className="mt-2 space-y-3 text-sm">
                {terms.map((t) => (
                  <div key={t.id}>
                    <dt className="font-medium">
                      <a
                        href={termHref(t.id)}
                        target="_blank"
                        rel="noopener noreferrer"
                        className={LINK}
                      >
                        {t.name}
                        <span className="sr-only"> (opens in a new tab)</span>
                      </a>
                    </dt>
                    <dd className="mt-0.5 text-muted-foreground">{t.short}</dd>
                  </div>
                ))}
              </dl>
            </section>
          ) : null}

          <p className="border-t border-border pt-4 text-sm">
            <a
              href={helpDocsHref(mod.id)}
              target="_blank"
              rel="noopener noreferrer"
              data-testid="help-panel-read-more"
              className={LINK}
            >
              Read more
              <span className="sr-only">
                {" "}
                about {mod.title} in the documentation (opens in a new tab)
              </span>
            </a>
          </p>
        </SheetContent>
      </SheetPortal>
    </Sheet>
  );
}
