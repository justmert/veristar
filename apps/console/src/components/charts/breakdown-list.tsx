// the one breakdown of a total into its keys (pages, countries, browsers,
// routes): a bar behind each label scaled to the largest value, the share and
// the exact value. no value depends on the bar: both numbers are text.

import { cn } from "@veristar/brand/cn";
import { type ReactNode, useState } from "react";
import { Link } from "react-router";
import { EmptyState, ErrorState } from "@/components/states";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { formatCount, formatShareOf } from "@/lib/format";

export type BreakdownItem = {
  key: string;
  label: ReactNode;
  // the label as plain text, for the tooltip of a truncated label and the
  // accessible name of a row action
  text: string;
  value: number;
  // the row opens this address, such as the web page filtered to the key
  href?: string;
};

export type BreakdownListProps = {
  // the accessible name of the list, such as "Countries"
  label: string;
  items: BreakdownItem[];
  // the whole the shares are of (visitors of the range); without it no share
  // column shows, since keys can overlap and their sum is not the whole
  total?: number | null;
  // the heading of the value column, such as "Visitors"
  valueLabel?: string;
  format?: (n: number) => string;
  // rows shown before "Show more"
  limit?: number;
  // a server side "more": the list asks for more rows instead of showing
  // the rows it already holds
  onShowMore?: () => void;
  showMoreLabel?: string;
  showMorePending?: boolean;
  // a row is a button that selects its key, such as a filter
  onSelect?: (key: string) => void;
  selectedKey?: string | null;
  state?: "loading" | "error" | "empty" | "data";
  error?: unknown;
  onRetry?: () => void;
  emptyTitle?: string;
  emptyText?: string;
  className?: string;
};

export function BreakdownList({
  label,
  items,
  total,
  valueLabel = "Value",
  format = formatCount,
  limit = 10,
  onShowMore,
  showMoreLabel = "Show more",
  showMorePending = false,
  onSelect,
  selectedKey,
  state = items.length ? "data" : "empty",
  error,
  onRetry,
  emptyTitle = "No data in this range",
  emptyText,
  className,
}: BreakdownListProps) {
  const [open, setOpen] = useState(false);
  if (state === "loading")
    return (
      <div role="status" aria-busy="true" className={cn("flex flex-col gap-1", className)}>
        <span className="sr-only">Loading {label.toLowerCase()}</span>
        {Array.from({ length: 5 }, (_, i) => (
          <Bone key={i} className="h-(--vs-control-sm) w-full" />
        ))}
      </div>
    );
  if (state === "error")
    return <ErrorState compact error={error} onRetry={onRetry} className={className} />;
  if (state === "empty" || items.length === 0)
    return (
      <EmptyState compact title={emptyTitle} className={className}>
        {emptyText}
      </EmptyState>
    );

  const max = Math.max(1, ...items.map((i) => i.value));
  const shown = open || onShowMore ? items : items.slice(0, limit);
  const hidden = items.length - shown.length;
  const showShare = total !== undefined && total !== null;

  return (
    <div className={cn("flex min-w-0 flex-col gap-2", className)}>
      <div
        aria-hidden
        className="flex items-center justify-between gap-3 px-2 text-caption text-fg-2"
      >
        <span>{label}</span>
        <span className="flex gap-4">
          {showShare ? <span className="w-12 text-right">Share</span> : null}
          <span className="min-w-16 text-right">{valueLabel}</span>
        </span>
      </div>
      <ul aria-label={label} className="flex flex-col gap-1">
        {shown.map((it) => {
          const bar = (
            <>
              <span
                aria-hidden
                className={cn(
                  "absolute inset-y-0 left-0 rounded-sm transition-colors duration-(--vs-duration-press)",
                  selectedKey === it.key ? "bg-pressed" : "bg-selected",
                )}
                style={{ width: `${(it.value / max) * 100}%` }}
              />
              <span className="relative min-w-0 flex-1 truncate text-fg" title={it.text}>
                {it.label}
              </span>
              <span className="relative flex shrink-0 gap-4 text-small tabular">
                {showShare ? (
                  <span className="w-12 text-right text-fg-2">
                    {formatShareOf(it.value, total ?? 0)}
                  </span>
                ) : null}
                <span className="min-w-16 text-right text-fg">{format(it.value)}</span>
              </span>
            </>
          );
          const cls =
            "relative flex h-(--vs-control-sm) w-full items-center gap-3 rounded-sm px-2 text-left text-body max-lg:h-(--vs-control-touch)";
          return (
            <li key={it.key} data-key={it.key}>
              {it.href ? (
                <Link to={it.href} className={cn(cls, "hover:bg-hover focus-ring")}>
                  {bar}
                </Link>
              ) : onSelect ? (
                <button
                  type="button"
                  aria-pressed={selectedKey === it.key}
                  onClick={() => onSelect(it.key)}
                  className={cn(cls, "hover:bg-hover focus-ring")}
                >
                  {bar}
                </button>
              ) : (
                <span className={cls}>{bar}</span>
              )}
            </li>
          );
        })}
      </ul>
      {hidden > 0 ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="self-start max-lg:h-(--vs-control-touch)"
          onClick={() => setOpen(true)}
        >
          Show {formatCount(hidden)} more
        </Button>
      ) : onShowMore ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="self-start max-lg:h-(--vs-control-touch)"
          pending={showMorePending}
          onClick={onShowMore}
        >
          {showMoreLabel}
        </Button>
      ) : null}
    </div>
  );
}
