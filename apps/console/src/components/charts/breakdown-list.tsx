// the one breakdown of a total into its keys (pages, countries, browsers,
// routes): a bar behind each label scaled to the largest value, the share and
// the exact value. no value depends on the bar: both numbers are text.

import { cn } from "@veristar/brand/cn";
import { type ReactNode, useState } from "react";
import { Link } from "react-router";
import { ErrorState } from "@/components/states";
import { Button } from "@/components/ui/button";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
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
  // an extra column after the value, such as the 5xx rate of a route
  aside?: ReactNode;
};

export type BreakdownListProps = {
  // the accessible name of the list, such as "Countries"
  label: string;
  items: BreakdownItem[];
  // the whole the shares are of (visitors of the range); without it no share
  // column shows, since keys can overlap and their sum is not the whole
  total?: number | null;
  // the heading of the key column, such as "Country" or "Route"; the panel
  // title above the list already names the list, so this names one row
  columnLabel?: string;
  // the column headings row; off where the panel shows the list without one
  header?: boolean;
  // the heading of the value column, such as "Visitors"
  valueLabel?: string;
  // what the share column counts, as a tooltip on its heading, such as when
  // one visitor counts in several rows
  shareDefinition?: string;
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
  // the colour of the share bars: the series of the data (web blue, api
  // violet), or amber for a list of quarantined traffic
  tone?: "series-1" | "series-3" | "warning";
  // the heading of the extra column, when items carry an `aside`
  asideLabel?: string;
  className?: string;
};

// a share of a part: a part smaller than the whole never reads as 100%, so
// 99.8% and 0.2% add up as the reader expects
const DEFINED =
  "w-11 rounded-sm text-right underline decoration-line-strong decoration-dotted underline-offset-4 focus-ring";

// the bar behind a row, plain and selected: a faint tint of the series the
// list counts, amber when the list reads quarantined traffic
const BAR = {
  "series-1": ["bg-series-1/16", "bg-series-1/30"],
  "series-3": ["bg-series-3/16", "bg-series-3/30"],
  warning: ["bg-warning/20", "bg-warning/35"],
} as const;

export function partShare(value: number, total: number): string {
  if (total > 0 && value < total && value / total >= 0.995)
    return `${(Math.floor((value / total) * 1000) / 10).toFixed(1)}%`;
  return formatShareOf(value, total);
}

export function BreakdownList({
  label,
  items,
  total,
  columnLabel,
  header = true,
  valueLabel = "Value",
  shareDefinition,
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
  tone = "series-1",
  asideLabel,
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
    return (
      <ErrorState
        compact
        title={`${label} did not load`}
        error={error}
        onRetry={onRetry}
        className={className}
      />
    );
  // an empty list is one line inside its panel, not a box in the box
  if (state === "empty" || items.length === 0)
    return (
      <p role="status" className={cn("text-small text-fg-2", className)}>
        {emptyTitle}
        {emptyText ? <span className="block text-caption text-fg-3">{emptyText}</span> : null}
      </p>
    );

  const max = Math.max(1, ...items.map((i) => i.value));
  const shown = open || onShowMore ? items : items.slice(0, limit);
  const hidden = items.length - shown.length;
  const showShare = total !== undefined && total !== null;
  const hasAside = shown.some((it) => it.aside !== undefined);

  return (
    <div className={cn("flex min-w-0 flex-col gap-2", className)}>
      {header ? (
        <div className="flex items-center justify-between gap-3 px-2 text-caption text-fg-2">
          <span aria-hidden>{columnLabel ?? label}</span>
          <span className="flex gap-4">
            <span aria-hidden className="min-w-12 text-right">
              {valueLabel}
            </span>
            {showShare ? (
              shareDefinition ? (
                <Tooltip content={shareDefinition}>
                  {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on focus too */}
                  <span tabIndex={0} className={DEFINED}>
                    Share
                    <span className="sr-only">. {shareDefinition}</span>
                  </span>
                </Tooltip>
              ) : (
                <span aria-hidden className="w-11 text-right">
                  Share
                </span>
              )
            ) : null}
            {hasAside ? (
              <span aria-hidden className="w-12 text-right">
                {asideLabel}
              </span>
            ) : null}
          </span>
        </div>
      ) : null}
      <ul aria-label={label} className="flex flex-col gap-1">
        {shown.map((it) => {
          // the bar spans the row under the label and the numbers, a faint
          // tint that keeps every text readable over it
          const bar = (
            <>
              <span
                aria-hidden
                className={cn(
                  "absolute inset-y-1 left-0 rounded-sm transition-colors duration-(--vs-duration-press)",
                  BAR[tone][selectedKey === it.key ? 1 : 0],
                )}
                style={{ width: `${(it.value / max) * 100}%` }}
              />
              <span className="relative min-w-0 flex-1 truncate text-fg" title={it.text}>
                {it.label}
              </span>
              <span className="relative flex shrink-0 gap-4 text-small tabular">
                <span className="min-w-12 text-right text-fg">{format(it.value)}</span>
                {showShare ? (
                  <span className="w-11 text-right text-caption leading-5 text-fg-3">
                    {partShare(it.value, total ?? 0)}
                  </span>
                ) : null}
                {hasAside ? <span className="w-12 text-right text-caption leading-5">{it.aside}</span> : null}
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
