// #1818 — /risks/taxonomy: the organisation's risk taxonomy in one place.
//
//   Categories — every category with its usage and state: add, rename,
//                archive / un-archive, set default, delete. Delete is
//                disabled, with the counts, while risks or aggregation
//                rules use it; the default can be neither archived nor
//                deleted, and an archived category can't be the default.
//   Themes     — the organisation's own themes with usage: add, rename,
//                delete (disabled while in use). Built-ins are listed
//                read-only, labelled "Built-in".
//
// Anyone who can read risks can view the page. Edit controls render only
// for admin / grc_engineer (`canManageTaxonomy`), the same roles the
// platform's risk-taxonomy guard admits; the platform still refuses every
// write from anyone else (403), so hiding the controls is presentation.
// Errors show inline beside the section they came from; successes toast.

"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState, type FormEvent } from "react";

import { PageShell } from "@/components/shell/page-shell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  ConfirmInline,
  useFocusAfterRemoval,
  useTriggerRefs,
} from "@/components/ui/row-actions";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { getMe } from "@/lib/api/me";
import {
  canManageTaxonomy,
  createCategory,
  createTheme,
  deleteBlockedReason,
  deleteCategory,
  deleteTheme,
  listTaxonomyCategories,
  listTaxonomyThemes,
  renameCategory,
  renameTheme,
  transitionCategory,
  usageLabel,
  type RiskCategory,
  type TaxonomyTheme,
} from "@/lib/api/taxonomy";
import { pageTitle } from "@/lib/page-titles";
import { Term } from "@/components/help/term";

const CATEGORIES_KEY = ["taxonomy-categories"];
const THEMES_KEY = ["taxonomy-themes"];

function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export default function RiskTaxonomyPage() {
  const meQuery = useQuery({ queryKey: ["taxonomy-me"], queryFn: getMe });
  const canEdit = canManageTaxonomy(meQuery.data);

  return (
    <PageShell
      title={pageTitle("/risks/taxonomy")}
      width="reading"
      data-testid="taxonomy-page"
      subtitle={
        <>
          Your organisation&apos;s risk categories and{" "}
          <Term id="theme">themes</Term>.{" "}
          {canEdit
            ? "Slugs never change, so integrations keep working after a rename."
            : "Changing them needs the admin or GRC engineer role."}
        </>
      }
    >
      <CategoriesSection canEdit={canEdit} />
      <ThemesSection canEdit={canEdit} />
    </PageShell>
  );
}

// ----- Categories -----

function CategoriesSection({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [error, setError] = useState<string | null>(null);
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");

  const q = useQuery({
    queryKey: CATEGORIES_KEY,
    queryFn: listTaxonomyCategories,
  });
  // #2682 — after a delete, focus the next row's Delete, not <body>.
  const deleteTriggers = useTriggerRefs();
  const headingRef = useRef<HTMLHeadingElement>(null);

  const run = useMutation({
    mutationFn: (op: { fn: () => Promise<unknown>; done: string }) => op.fn(),
    onSuccess: (_d, op) => {
      setError(null);
      toast(op.done);
      return qc.invalidateQueries({ queryKey: CATEGORIES_KEY });
    },
    onError: (err) => setError(errorText(err)),
  });
  const removal = useFocusAfterRemoval(
    (q.data ?? []).map((c) => c.slug),
    {
      get: deleteTriggers.get,
      fallback: () => headingRef.current,
      settled: !run.isPending,
    },
  );

  function add(e: FormEvent) {
    e.preventDefault();
    const s = slug.trim();
    const n = name.trim();
    if (!s || !n) {
      setError("A category needs a slug and a name.");
      return;
    }
    run.mutate(
      {
        fn: () => createCategory({ slug: s, name: n }),
        done: `Category “${n}” added`,
      },
      {
        onSuccess: () => {
          setSlug("");
          setName("");
        },
      },
    );
  }

  return (
    <section
      id="categories"
      aria-labelledby="taxonomy-categories-heading"
      className="space-y-3"
      data-testid="taxonomy-categories"
    >
      <h2
        id="taxonomy-categories-heading"
        ref={headingRef}
        tabIndex={-1}
        className="text-lg font-semibold"
      >
        Categories
      </h2>
      <p className="text-sm text-muted-foreground">
        Each risk has one category. Archived categories stay on the risks that
        have them but can&apos;t be picked for new ones. Risks generated by
        aggregation rules fall back to the default.
      </p>
      {error ? (
        <Alert variant="destructive" data-testid="taxonomy-categories-error">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {canEdit ? (
        <form
          onSubmit={add}
          className="flex flex-wrap items-end gap-2"
          data-testid="category-add-form"
        >
          <label className="flex flex-col gap-1 text-sm">
            Slug
            <Input
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              placeholder="cloud-security"
              className="w-44"
              data-testid="category-add-slug"
            />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Name
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Cloud Security"
              className="w-56"
              data-testid="category-add-name"
            />
          </label>
          <Button
            type="submit"
            disabled={run.isPending}
            data-testid="category-add-submit"
          >
            Add category
          </Button>
        </form>
      ) : null}
      {q.isError ? (
        <Alert variant="destructive">
          <AlertDescription>
            Could not load categories: {errorText(q.error)}
          </AlertDescription>
        </Alert>
      ) : (
        <Table data-testid="category-table">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Slug</TableHead>
              <TableHead>Usage</TableHead>
              {canEdit ? (
                <TableHead className="text-right">Actions</TableHead>
              ) : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {(q.data ?? []).map((c) => (
              <CategoryRow
                key={c.id}
                category={c}
                canEdit={canEdit}
                busy={run.isPending}
                onRun={(fn, done) => run.mutate({ fn, done })}
                deleteRef={deleteTriggers.ref(c.slug)}
                getDeleteTrigger={() => deleteTriggers.get(c.slug)}
                onDeleteConfirmed={() => removal.arm(c.slug)}
              />
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

function CategoryRow({
  category: c,
  canEdit,
  busy,
  onRun,
  deleteRef,
  getDeleteTrigger,
  onDeleteConfirmed,
}: {
  category: RiskCategory;
  canEdit: boolean;
  busy: boolean;
  onRun: (fn: () => Promise<unknown>, done: string) => void;
  deleteRef: (el: HTMLElement | null) => void;
  getDeleteTrigger: () => HTMLElement | null;
  onDeleteConfirmed: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [draft, setDraft] = useState(c.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const blocked = deleteBlockedReason(c);

  return (
    <TableRow data-testid={`category-row-${c.slug}`} data-archived={c.archived}>
      <TableCell>
        {renaming ? (
          <form
            className="flex items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              const next = draft.trim();
              if (next && next !== c.name) {
                onRun(
                  () => renameCategory(c.slug, next),
                  `Category renamed to “${next}”`,
                );
              }
              setRenaming(false);
            }}
          >
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              aria-label={`New name for ${c.name}`}
              className="w-48"
              autoFocus
              data-testid={`category-rename-input-${c.slug}`}
            />
            <Button
              type="submit"
              size="sm"
              data-testid={`category-rename-save-${c.slug}`}
            >
              Save
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => setRenaming(false)}
            >
              Cancel
            </Button>
          </form>
        ) : (
          <span className="flex flex-wrap items-center gap-2">
            <span data-testid={`category-name-${c.slug}`}>{c.name}</span>
            {c.is_default ? (
              <Badge
                variant="secondary"
                data-testid={`category-default-${c.slug}`}
              >
                Default
              </Badge>
            ) : null}
            {c.archived ? (
              <Badge
                variant="outline"
                data-testid={`category-archived-${c.slug}`}
              >
                Archived
              </Badge>
            ) : null}
          </span>
        )}
      </TableCell>
      <TableCell className="font-mono text-xs">{c.slug}</TableCell>
      <TableCell className="text-sm" data-testid={`category-usage-${c.slug}`}>
        {usageLabel(c)}
      </TableCell>
      {canEdit ? (
        <TableCell className="text-right">
          <div className="flex flex-wrap justify-end gap-1">
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setDraft(c.name);
                setRenaming(true);
              }}
              data-testid={`category-rename-${c.slug}`}
            >
              Rename
            </Button>
            {!c.archived && !c.is_default ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() =>
                  onRun(
                    () => transitionCategory(c.slug, "set-default"),
                    `“${c.name}” is now the default`,
                  )
                }
                data-testid={`category-set-default-${c.slug}`}
              >
                Set default
              </Button>
            ) : null}
            {c.archived ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() =>
                  onRun(
                    () => transitionCategory(c.slug, "unarchive"),
                    `“${c.name}” restored`,
                  )
                }
                data-testid={`category-unarchive-${c.slug}`}
              >
                Un-archive
              </Button>
            ) : !c.is_default ? (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() =>
                  onRun(
                    () => transitionCategory(c.slug, "archive"),
                    `“${c.name}” archived`,
                  )
                }
                data-testid={`category-archive-${c.slug}`}
              >
                Archive
              </Button>
            ) : null}
            <Button
              size="sm"
              variant="ghost"
              className="text-destructive"
              disabled={busy || blocked !== null}
              title={blocked ?? undefined}
              aria-describedby={
                blocked ? `category-delete-reason-${c.slug}` : undefined
              }
              ref={deleteRef}
              onClick={() => setConfirmDelete(true)}
              data-testid={`category-delete-${c.slug}`}
            >
              Delete
            </Button>
          </div>
          {blocked ? (
            <p
              id={`category-delete-reason-${c.slug}`}
              className="mt-1 text-xs text-muted-foreground"
              data-testid={`category-delete-reason-${c.slug}`}
            >
              Can&apos;t delete: {blocked}
            </p>
          ) : null}
          {confirmDelete ? (
            <div className="mt-2 flex justify-end">
              <ConfirmInline
                prompt={`Delete “${c.name}”? This can't be undone.`}
                confirmLabel="Delete"
                busy={busy}
                onCancel={() => setConfirmDelete(false)}
                returnFocus={getDeleteTrigger}
                onConfirm={() => {
                  setConfirmDelete(false);
                  onDeleteConfirmed();
                  onRun(
                    () => deleteCategory(c.slug),
                    `Category “${c.name}” deleted`,
                  );
                }}
                data-testid={`category-delete-confirm-${c.slug}`}
              />
            </div>
          ) : null}
        </TableCell>
      ) : null}
    </TableRow>
  );
}

// ----- Themes -----

function ThemesSection({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");

  const q = useQuery({ queryKey: THEMES_KEY, queryFn: listTaxonomyThemes });

  const run = useMutation({
    mutationFn: (op: { fn: () => Promise<unknown>; done: string }) => op.fn(),
    onSuccess: (_d, op) => {
      setError(null);
      toast(op.done);
      return qc.invalidateQueries({ queryKey: THEMES_KEY });
    },
    onError: (err) => setError(errorText(err)),
  });

  function add(e: FormEvent) {
    e.preventDefault();
    const n = name.trim();
    if (!n) {
      setError("A theme needs a name.");
      return;
    }
    run.mutate(
      { fn: () => createTheme({ name: n }), done: `Theme “${n}” added` },
      { onSuccess: () => setName("") },
    );
  }

  // The organisation's own first, then the built-ins; each alphabetical.
  const themes = [...(q.data ?? [])].sort(
    (a, b) =>
      Number(a.source === "default") - Number(b.source === "default") ||
      a.name.localeCompare(b.name),
  );
  // #2682 — after a delete, focus the next row's Delete, not <body>.
  const themeKey = (t: TaxonomyTheme) => `${t.source}:${t.name}`;
  const deleteTriggers = useTriggerRefs();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const removal = useFocusAfterRemoval(themes.map(themeKey), {
    get: deleteTriggers.get,
    fallback: () => headingRef.current,
    settled: !run.isPending,
  });

  return (
    <section
      id="themes"
      aria-labelledby="taxonomy-themes-heading"
      className="space-y-3"
      data-testid="taxonomy-themes"
    >
      <h2
        id="taxonomy-themes-heading"
        ref={headingRef}
        tabIndex={-1}
        className="text-lg font-semibold"
      >
        Themes
      </h2>
      <p className="text-sm text-muted-foreground">
        Themes tag cross-cutting concerns on risks and drive aggregation rules.
        Built-in themes are shared and can&apos;t be changed. Renaming one of
        your themes updates every risk and rule that uses it.
      </p>
      {error ? (
        <Alert variant="destructive" data-testid="taxonomy-themes-error">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {canEdit ? (
        <form
          onSubmit={add}
          className="flex flex-wrap items-end gap-2"
          data-testid="theme-add-form"
        >
          <label className="flex flex-col gap-1 text-sm">
            Name
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="ransomware"
              className="w-56"
              data-testid="theme-add-name"
            />
          </label>
          <Button
            type="submit"
            disabled={run.isPending}
            data-testid="theme-add-submit"
          >
            Add theme
          </Button>
        </form>
      ) : null}
      {q.isError ? (
        <Alert variant="destructive">
          <AlertDescription>
            Could not load themes: {errorText(q.error)}
          </AlertDescription>
        </Alert>
      ) : (
        <Table data-testid="theme-table">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Usage</TableHead>
              {canEdit ? (
                <TableHead className="text-right">Actions</TableHead>
              ) : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {themes.map((t) => (
              <ThemeRow
                key={themeKey(t)}
                theme={t}
                canEdit={canEdit}
                busy={run.isPending}
                onRun={(fn, done) => run.mutate({ fn, done })}
                deleteRef={deleteTriggers.ref(themeKey(t))}
                getDeleteTrigger={() => deleteTriggers.get(themeKey(t))}
                onDeleteConfirmed={() => removal.arm(themeKey(t))}
              />
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

function ThemeRow({
  theme: t,
  canEdit,
  busy,
  onRun,
  deleteRef,
  getDeleteTrigger,
  onDeleteConfirmed,
}: {
  theme: TaxonomyTheme;
  canEdit: boolean;
  busy: boolean;
  onRun: (fn: () => Promise<unknown>, done: string) => void;
  deleteRef: (el: HTMLElement | null) => void;
  getDeleteTrigger: () => HTMLElement | null;
  onDeleteConfirmed: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [draft, setDraft] = useState(t.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const builtIn = t.source === "default";
  const blocked = deleteBlockedReason(t);

  return (
    <TableRow data-testid={`theme-row-${t.name}`} data-source={t.source}>
      <TableCell>
        {renaming ? (
          <form
            className="flex items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              const next = draft.trim();
              if (next && next !== t.name) {
                onRun(
                  () => renameTheme(t.name, next),
                  `Theme renamed to “${next}”`,
                );
              }
              setRenaming(false);
            }}
          >
            <Input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              aria-label={`New name for ${t.name}`}
              className="w-48"
              autoFocus
              data-testid={`theme-rename-input-${t.name}`}
            />
            <Button
              type="submit"
              size="sm"
              data-testid={`theme-rename-save-${t.name}`}
            >
              Save
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => setRenaming(false)}
            >
              Cancel
            </Button>
          </form>
        ) : (
          <span className="flex flex-wrap items-center gap-2">
            <span>{t.name}</span>
            {builtIn ? (
              <Badge variant="outline" data-testid={`theme-builtin-${t.name}`}>
                Built-in
              </Badge>
            ) : null}
          </span>
        )}
      </TableCell>
      <TableCell className="text-sm" data-testid={`theme-usage-${t.name}`}>
        {usageLabel(t)}
      </TableCell>
      {canEdit ? (
        <TableCell className="text-right">
          {builtIn ? (
            <span className="text-xs text-muted-foreground">Read-only</span>
          ) : (
            <>
              <div className="flex flex-wrap justify-end gap-1">
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy}
                  onClick={() => {
                    setDraft(t.name);
                    setRenaming(true);
                  }}
                  data-testid={`theme-rename-${t.name}`}
                >
                  Rename
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive"
                  disabled={busy || blocked !== null}
                  title={blocked ?? undefined}
                  aria-describedby={
                    blocked ? `theme-delete-reason-${t.name}` : undefined
                  }
                  ref={deleteRef}
                  onClick={() => setConfirmDelete(true)}
                  data-testid={`theme-delete-${t.name}`}
                >
                  Delete
                </Button>
              </div>
              {blocked ? (
                <p
                  id={`theme-delete-reason-${t.name}`}
                  className="mt-1 text-xs text-muted-foreground"
                  data-testid={`theme-delete-reason-${t.name}`}
                >
                  Can&apos;t delete: {blocked}
                </p>
              ) : null}
              {confirmDelete ? (
                <div className="mt-2 flex justify-end">
                  <ConfirmInline
                    prompt={`Delete “${t.name}”? This can't be undone.`}
                    confirmLabel="Delete"
                    busy={busy}
                    onCancel={() => setConfirmDelete(false)}
                    returnFocus={getDeleteTrigger}
                    onConfirm={() => {
                      setConfirmDelete(false);
                      onDeleteConfirmed();
                      onRun(
                        () => deleteTheme(t.name),
                        `Theme “${t.name}” deleted`,
                      );
                    }}
                    data-testid={`theme-delete-confirm-${t.name}`}
                  />
                </div>
              ) : null}
            </>
          )}
        </TableCell>
      ) : null}
    </TableRow>
  );
}
