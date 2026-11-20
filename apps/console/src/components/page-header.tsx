import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";
import { LabeledFacts } from "@/components/labeled-facts";
import { Tooltip } from "@/components/ui/tooltip";
import { formatCount, formatUpdated } from "@/lib/format";

export type Freshness = {
  // when the numbers on the page were read
  at: string | Date;
  // how often the source refreshes, said in the tooltip ("Counts refresh
  // every 5 minutes.")
  cadence?: string;
};

// the title row of every console page, as on the canvas: the title with an
// optional muted count, one meta line under it whose items are separate
// elements (the freshness first, then a range, a domain, a link), and the
// actions pinned top right. the one primary action (the ink button) goes in
// `primary`; everything else in `secondary`. no tabs: the sidebar lists
// every page. `meta` sits beside the title (a status pill). `details` are
// labeled facts of the object, `description` an older line of purpose that
// the redesigned pages no longer use.
export function PageHeader({
  title,
  count,
  items,
  description,
  details,
  freshness,
  primary,
  secondary,
  meta,
  crumb,
  className,
}: {
  title: ReactNode;
  // the number of things the page lists ("Review 3")
  count?: number | null;
  // the meta line: each item its own element, never joined with a separator
  items?: ReactNode[];
  description?: ReactNode;
  // facts of the object the page is about (slug, owner, since)
  details?: { label: string; value: ReactNode }[];
  freshness?: Freshness | null;
  primary?: ReactNode;
  secondary?: ReactNode;
  // a status badge shown beside the title
  meta?: ReactNode;
  // a link back to the parent page, above the title ("Sites")
  crumb?: ReactNode;
  className?: string;
}) {
  const actions = primary || secondary;
  const line = [
    freshness ? <FreshnessLine key="_freshness" freshness={freshness} /> : null,
    ...(items ?? []),
  ].filter((x) => x !== null && x !== undefined && x !== false && x !== "");
  return (
    <header
      className={cn(
        "grid gap-x-4 gap-y-3",
        actions && "sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center",
        className,
      )}
    >
      <div className="flex min-w-0 flex-col">
        {crumb ? (
          <div className="mb-1.5 flex items-center gap-1.5 text-small text-fg-3">{crumb}</div>
        ) : null}
        <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1">
          {/* a long title stops at two lines; the full title is its tooltip */}
          <h1
            className="line-clamp-2 min-w-0 text-heading break-words text-fg"
            title={typeof title === "string" ? title : undefined}
          >
            {title}
          </h1>
          {count !== undefined && count !== null ? (
            <span className="text-[15px] text-fg-3 tabular" data-header-count="">
              {formatCount(count)}
            </span>
          ) : null}
          {meta}
        </div>
        {line.length ? (
          <div className="mt-1 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-small text-fg-3">
            {line.map((item, i) => (
              <span key={i} className="flex min-w-0 items-center">
                {item}
              </span>
            ))}
          </div>
        ) : null}
        {description ? <p className="mt-1 max-w-prose text-body text-fg-2">{description}</p> : null}
        {details?.length ? <LabeledFacts className="mt-2" items={details} /> : null}
      </div>
      {actions ? (
        <div className="flex flex-wrap items-center gap-2 max-sm:w-full sm:justify-end [&>*:not(dialog)]:max-sm:h-(--vs-control-touch)">
          {secondary}
          {primary}
        </div>
      ) : null}
    </header>
  );
}

function FreshnessLine({ freshness }: { freshness: Freshness }) {
  const text = formatUpdated(freshness.at);
  const iso = freshness.at instanceof Date ? freshness.at.toISOString() : freshness.at;
  const line = (
    <time dateTime={iso} className="block w-fit text-small text-fg-3" data-freshness="">
      {text}
    </time>
  );
  if (!freshness.cadence) return line;
  return (
    <Tooltip content={freshness.cadence}>
      {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the tooltip with the source's cadence opens on focus too */}
      <span tabIndex={0} className="w-fit rounded-sm focus-ring">
        {line}
        <span className="sr-only">. {freshness.cadence}</span>
      </span>
    </Tooltip>
  );
}
