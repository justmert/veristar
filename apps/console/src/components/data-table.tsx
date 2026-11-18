import { cn } from "@veristar/brand/cn";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { ErrorState } from "@/components/states";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { formatCount } from "@/lib/format";

// one table for every list of the console. on a wide screen it is a real
// <table> with a sticky header and sortable headers (th > button, aria-sort).
// below the sm breakpoint the same rows render as stacked cards: the name
// line and the two or three columns marked `mobile`. both renderings are in
// the dom and one is hidden with display:none, so assistive technology only
// ever meets the visible one.

export type Column<Row> = {
  key: string;
  header: string;
  // what the column counts, in one or two sentences: a tooltip on the
  // header (hover and keyboard focus), so the table carries its own
  // definitions instead of paragraphs under it
  definition?: string;
  cell: (row: Row) => ReactNode;
  // the server sort key this header sorts by
  sortKey?: string;
  align?: "left" | "right";
  // a min-w-* class that keeps the column readable
  minWidth?: string;
  // shown on the phone card under the name line
  mobile?: boolean;
  // the name line of the phone card and the cell that carries the row link;
  // the first column when no column says so
  primary?: boolean;
  // long values are cut with an ellipsis; the full value is in a tooltip
  truncate?: (row: Row) => string;
  // numbers are set in mono with tabular figures
  numeric?: boolean;
  className?: string;
};

export type TableFooter =
  | { kind: "more"; hasMore: boolean; loading: boolean; onMore: () => void }
  | {
      kind: "pager";
      // zero based
      page: number;
      pageSize: number;
      total: number;
      onPage: (page: number) => void;
    };

export type Sort = { key: string; order: "asc" | "desc" };

export function DataTable<Row>({
  label,
  columns,
  rows,
  getRowKey,
  rowHref,
  rowLabel,
  rowProps,
  sort,
  onSort,
  state = "data",
  error,
  onRetry,
  empty,
  footer,
  selection,
  loadingRows = 5,
  className,
}: {
  // the accessible name of the table and its scroll region
  label: string;
  columns: Column<Row>[];
  rows: Row[];
  getRowKey: (row: Row) => string;
  rowHref?: (row: Row) => string;
  // names a row for selection labels and the phone card link
  rowLabel?: (row: Row) => string;
  // data attributes per row (data-tenant, data-route) for tests and scripts
  rowProps?: (row: Row) => Record<`data-${string}`, string>;
  sort?: Sort;
  onSort?: (key: string) => void;
  state?: "data" | "loading" | "error";
  error?: unknown;
  onRetry?: () => void;
  empty?: { title: string; text?: ReactNode; action?: ReactNode };
  footer?: TableFooter;
  selection?: {
    selected: ReadonlySet<string>;
    onSelect: (keys: Set<string>) => void;
    // a row that cannot be selected (a full comparison, say)
    disabled?: (row: Row) => boolean;
  };
  loadingRows?: number;
  className?: string;
}) {
  const primary = columns.find((c) => c.primary) ?? columns[0];
  const span = columns.length + (selection ? 1 : 0);
  const name = (r: Row) => rowLabel?.(r) ?? getRowKey(r);
  const pageKeys = rows.map(getRowKey);
  const allOn = pageKeys.length > 0 && pageKeys.every((k) => selection?.selected.has(k));
  const someOn = pageKeys.some((k) => selection?.selected.has(k));

  function toggle(key: string, on: boolean) {
    if (!selection) return;
    const next = new Set(selection.selected);
    if (on) next.add(key);
    else next.delete(key);
    selection.onSelect(next);
  }

  function toggleAll(on: boolean) {
    if (!selection) return;
    const next = new Set(selection.selected);
    for (const r of rows) {
      if (selection.disabled?.(r)) continue;
      if (on) next.add(getRowKey(r));
      else next.delete(getRowKey(r));
    }
    selection.onSelect(next);
  }

  const showEmpty = state === "data" && rows.length === 0;

  return (
    <div className={cn("flex min-w-0 flex-col rounded-lg border border-line bg-raised", className)}>
      <section
        aria-label={label}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: a scroll region must take focus to scroll by keyboard
        tabIndex={0}
        className="max-h-[70vh] overflow-auto rounded-t-lg focus-ring max-sm:hidden"
      >
        <table aria-label={label} className="w-full border-separate border-spacing-0 text-small">
          <thead>
            <tr>
              {selection ? (
                <th scope="col" className={cn(HEAD, "w-10")}>
                  <Checkbox
                    aria-label="Select all on this page"
                    checked={allOn ? true : someOn ? "indeterminate" : false}
                    disabled={state !== "data" || rows.length === 0}
                    onCheckedChange={(c) => toggleAll(c === true)}
                  />
                </th>
              ) : null}
              {columns.map((c) => (
                <HeaderCell key={c.key} column={c} sort={sort} onSort={onSort} />
              ))}
            </tr>
          </thead>
          <tbody aria-busy={state === "loading" || undefined}>
            {state === "loading" ? (
              Array.from({ length: loadingRows }, (_, i) => (
                <tr key={i} aria-hidden className="h-(--vs-row)">
                  {Array.from({ length: span }, (_, j) => (
                    <td key={j} className={CELL}>
                      <Skeleton className={cn("h-3", j === 0 ? "w-3/4" : "w-1/2")} />
                    </td>
                  ))}
                </tr>
              ))
            ) : state === "error" ? (
              <tr>
                <td colSpan={span} className="p-4">
                  <ErrorState error={error} onRetry={onRetry} compact className="border-0" />
                </td>
              </tr>
            ) : showEmpty ? (
              <tr>
                <td colSpan={span} className="p-4">
                  <EmptyRow empty={empty} />
                </td>
              </tr>
            ) : (
              rows.map((r) => {
                const key = getRowKey(r);
                const href = rowHref?.(r);
                const on = selection?.selected.has(key) ?? false;
                return (
                  <tr
                    key={key}
                    {...rowProps?.(r)}
                    data-state={on ? "selected" : undefined}
                    className="group/row relative h-(--vs-row) transition-colors duration-(--vs-duration-press) ease-standard hover:bg-hover data-[state=selected]:bg-selected"
                  >
                    {selection ? (
                      <td className={cn(CELL, "relative z-(--vs-layer-raised) w-10")}>
                        <Checkbox
                          aria-label={`Select ${name(r)}`}
                          checked={on}
                          disabled={!on && selection.disabled?.(r)}
                          onCheckedChange={(c) => toggle(key, c === true)}
                        />
                      </td>
                    ) : null}
                    {columns.map((c) => (
                      <td
                        key={c.key}
                        className={cn(
                          CELL,
                          c.align === "right" && "text-right",
                          c.numeric && "font-mono tabular",
                          c.minWidth,
                          c.className,
                          href && c !== primary && ABOVE_ROW_LINK,
                        )}
                      >
                        {c === primary && href ? (
                          // the link stretches over the whole row, so a click
                          // anywhere opens it; the keyboard reaches it once
                          <Link
                            to={href}
                            className="rounded-sm font-medium text-fg focus-ring after:absolute after:inset-0 hover:underline"
                          >
                            <CellValue column={c} row={r} />
                          </Link>
                        ) : (
                          <CellValue column={c} row={r} />
                        )}
                      </td>
                    ))}
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </section>

      {/* the phone rendering: one card per row */}
      <div className="sm:hidden">
        {state === "loading" ? (
          <ul aria-busy="true" aria-label={label} className="flex flex-col">
            {Array.from({ length: Math.min(loadingRows, 3) }, (_, i) => (
              <li
                key={i}
                aria-hidden
                className="flex flex-col gap-2 border-b border-line-subtle p-4 last:border-b-0"
              >
                <Skeleton className="h-4 w-2/3" />
                <Skeleton className="h-3 w-1/2" />
              </li>
            ))}
          </ul>
        ) : state === "error" ? (
          <div className="p-4">
            <ErrorState error={error} onRetry={onRetry} compact className="border-0" />
          </div>
        ) : showEmpty ? (
          <div className="p-4">
            <EmptyRow empty={empty} />
          </div>
        ) : (
          <ul aria-label={label} className="flex flex-col">
            {rows.map((r) => (
              <PhoneCard
                key={getRowKey(r)}
                row={r}
                rowKey={getRowKey(r)}
                primary={primary}
                columns={columns.filter((c) => c.mobile && c !== primary).slice(0, 3)}
                href={rowHref?.(r)}
                name={name(r)}
                rowProps={rowProps}
                selection={selection}
                onToggle={toggle}
              />
            ))}
          </ul>
        )}
      </div>

      {footer && state === "data" && rows.length > 0 ? <Footer footer={footer} /> : null}
    </div>
  );
}

const HEAD =
  "sticky top-0 z-(--vs-layer-raised) h-9 border-b border-line bg-raised px-3 text-left align-middle text-caption font-medium whitespace-nowrap text-fg-2";
const CELL = "border-b border-line-subtle px-3 align-middle text-fg group-last/row:border-b-0";
// the row link stretches over the whole row; a control inside another cell
// (a menu, a reasons button, a second link) sits above it and gets its own
// click
const ABOVE_ROW_LINK =
  "[&_:is(a,button,input,select,textarea,[role=button])]:relative [&_:is(a,button,input,select,textarea,[role=button])]:z-(--vs-layer-raised)";

function HeaderCell<Row>({
  column: c,
  sort,
  onSort,
}: {
  column: Column<Row>;
  sort?: Sort;
  onSort?: (key: string) => void;
}) {
  const active = c.sortKey !== undefined && sort?.key === c.sortKey;
  const ariaSort = active ? (sort?.order === "asc" ? "ascending" : "descending") : undefined;
  const right = c.align === "right";
  const label = c.definition ? (
    <span className="underline decoration-line-strong decoration-dotted underline-offset-4">
      {c.header}
    </span>
  ) : (
    c.header
  );
  if (!c.sortKey || !onSort)
    return (
      <th scope="col" className={cn(HEAD, right && "text-right", c.minWidth)}>
        {c.definition ? (
          <Tooltip content={c.definition}>
            {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on keyboard focus too */}
            <span tabIndex={0} className="rounded-sm focus-ring">
              {label}
              <span className="sr-only">. {c.definition}</span>
            </span>
          </Tooltip>
        ) : (
          label
        )}
      </th>
    );
  const sortKey = c.sortKey;
  const Icon = !active ? ArrowUpDown : sort?.order === "asc" ? ArrowUp : ArrowDown;
  const button = (
    <button
      type="button"
      onClick={() => onSort(sortKey)}
      aria-description={c.definition}
      className={cn(
        "-mx-1 inline-flex items-center gap-1 rounded-sm px-1 py-1 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring active:bg-selected",
        right && "flex-row-reverse",
        active && "text-fg",
      )}
    >
      {label}
      <Icon aria-hidden className={cn("size-3", active ? "text-fg" : "text-fg-3")} />
    </button>
  );
  return (
    <th
      scope="col"
      aria-sort={ariaSort ?? "none"}
      className={cn(HEAD, right && "text-right", c.minWidth)}
    >
      {c.definition ? <Tooltip content={c.definition}>{button}</Tooltip> : button}
    </th>
  );
}

function CellValue<Row>({ column: c, row }: { column: Column<Row>; row: Row }) {
  if (!c.truncate) return <>{c.cell(row)}</>;
  const full = c.truncate(row);
  return (
    <Tooltip content={full}>
      <span className="block max-w-full truncate">{c.cell(row)}</span>
    </Tooltip>
  );
}

function EmptyRow({ empty }: { empty?: { title: string; text?: ReactNode; action?: ReactNode } }) {
  return (
    <div role="status" className="flex flex-col items-center gap-2 px-4 py-6 text-center">
      <span className="text-body font-medium text-fg">{empty?.title ?? "Nothing to show"}</span>
      {empty?.text ? <span className="text-small text-fg-2">{empty.text}</span> : null}
      {empty?.action}
    </div>
  );
}

function PhoneCard<Row>({
  row,
  rowKey,
  primary,
  columns,
  href,
  name,
  rowProps,
  selection,
  onToggle,
}: {
  row: Row;
  rowKey: string;
  primary: Column<Row> | undefined;
  columns: Column<Row>[];
  href?: string;
  name: string;
  rowProps?: (row: Row) => Record<`data-${string}`, string>;
  selection?: { selected: ReadonlySet<string>; disabled?: (row: Row) => boolean };
  onToggle: (key: string, on: boolean) => void;
}) {
  const on = selection?.selected.has(rowKey) ?? false;
  return (
    <li
      {...rowProps?.(row)}
      data-state={on ? "selected" : undefined}
      className="relative flex items-start gap-3 border-b border-line-subtle p-4 transition-colors duration-(--vs-duration-press) ease-standard last:border-b-0 hover:bg-hover data-[state=selected]:bg-selected"
    >
      {selection ? (
        <span className="relative z-(--vs-layer-raised) pt-1">
          <Checkbox
            aria-label={`Select ${name}`}
            checked={on}
            disabled={!on && selection.disabled?.(row)}
            onCheckedChange={(c) => onToggle(rowKey, c === true)}
          />
        </span>
      ) : null}
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <span className="min-w-0 truncate text-body font-medium text-fg">
          {primary && href ? (
            <Link to={href} className="rounded-sm focus-ring after:absolute after:inset-0">
              {primary.cell(row)}
            </Link>
          ) : primary ? (
            primary.cell(row)
          ) : null}
        </span>
        {columns.length ? (
          <dl className="grid grid-cols-3 gap-3">
            {columns.map((c) => (
              <div key={c.key} className="flex min-w-0 flex-col gap-1">
                <dt className="text-caption text-fg-2">{c.header}</dt>
                <dd
                  className={cn(
                    "min-w-0 truncate text-small text-fg",
                    c.numeric && "font-mono tabular",
                    href && ABOVE_ROW_LINK,
                  )}
                >
                  {c.cell(row)}
                </dd>
              </div>
            ))}
          </dl>
        ) : null}
      </div>
    </li>
  );
}

function Footer({ footer }: { footer: TableFooter }) {
  if (footer.kind === "more") {
    if (!footer.hasMore) return null;
    return (
      <div className="flex justify-center border-t border-line p-3">
        <Button
          variant="outline"
          size="sm"
          pending={footer.loading}
          onClick={footer.onMore}
          className="max-lg:h-(--vs-control-touch)"
        >
          {footer.loading ? "Loading" : "Load more"}
        </Button>
      </div>
    );
  }
  const { page, pageSize, total, onPage } = footer;
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const first = total === 0 ? 0 : page * pageSize + 1;
  const last = Math.min(total, (page + 1) * pageSize);
  return (
    <nav
      aria-label="Pages"
      className="flex flex-wrap items-center justify-between gap-3 border-t border-line px-4 py-2"
    >
      <span className="text-small text-fg-2 tabular">
        {formatCount(first)} to {formatCount(last)} of {formatCount(total)}
      </span>
      <span className="flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Previous page"
          disabled={page <= 0}
          onClick={() => onPage(page - 1)}
          className="max-lg:size-(--vs-control-touch)"
        >
          <ChevronLeft aria-hidden />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Next page"
          disabled={page >= pages - 1}
          onClick={() => onPage(page + 1)}
          className="max-lg:size-(--vs-control-touch)"
        >
          <ChevronRight aria-hidden />
        </Button>
      </span>
    </nav>
  );
}
