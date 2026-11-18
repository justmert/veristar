import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { formatUpdated } from "@/lib/format";

export type Freshness = {
  // when the numbers on the page were read
  at: string | Date;
  // how often the source refreshes, said in the tooltip ("Counts refresh
  // every 5 minutes.")
  cadence?: string;
};

// the title row of every console page, one anatomy everywhere: the title,
// one line of purpose, the facts of the object as a key-value row, the
// freshness of its numbers, and the actions pinned top right. the one
// primary action (the accent button) goes in `primary`; everything else in
// `secondary`. no tabs: the sidebar lists every page.
//
// `freshness`: leave it out on a page without numbers. `null` while the
// numbers load (or failed) keeps the line's height, so nothing moves when
// the time arrives.
export function PageHeader({
  title,
  description,
  details,
  freshness,
  primary,
  secondary,
  meta,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  // facts of the object the page is about (slug, owner, since)
  details?: { label: string; value: ReactNode }[];
  freshness?: Freshness | null;
  primary?: ReactNode;
  secondary?: ReactNode;
  // a status badge or a count shown beside the title
  meta?: ReactNode;
  className?: string;
}) {
  const actions = primary || secondary;
  return (
    <header
      className={cn(
        "grid gap-x-6 gap-y-3",
        actions && "sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start",
        className,
      )}
    >
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          {/* a long title stops at two lines; the full title is its tooltip */}
          <h1
            className="line-clamp-2 min-w-0 text-heading break-words text-fg"
            title={typeof title === "string" ? title : undefined}
          >
            {title}
          </h1>
          {meta}
        </div>
        {description ? <p className="max-w-prose text-body text-fg-2">{description}</p> : null}
        {details?.length ? (
          <dl className="flex flex-wrap gap-x-6 gap-y-1 text-small">
            {details.map((d) => (
              <div key={d.label} className="flex min-w-0 gap-2">
                <dt className="text-fg-2">{d.label}</dt>
                <dd className="min-w-0 break-words text-fg">{d.value}</dd>
              </div>
            ))}
          </dl>
        ) : null}
        {freshness ? (
          <FreshnessLine freshness={freshness} />
        ) : freshness === null ? (
          <span aria-hidden className="h-4" data-freshness-pending="" />
        ) : null}
      </div>
      {actions ? (
        <div className="flex flex-wrap items-center gap-2 max-sm:w-full sm:justify-end [&>*]:max-sm:h-(--vs-control-touch)">
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
    <time dateTime={iso} className="w-fit text-caption text-fg-2" data-freshness="">
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
