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

// the title row of every console page: what the page answers, one line of
// purpose when the title is not enough, the freshness of its numbers, and
// the page's actions. the one primary action (the accent button) goes in
// `primary`; everything else in `secondary`. no tabs: the sidebar lists
// every page.
export function PageHeader({
  title,
  description,
  freshness,
  primary,
  secondary,
  meta,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  freshness?: Freshness | null;
  primary?: ReactNode;
  secondary?: ReactNode;
  // a status badge or a count shown beside the title
  meta?: ReactNode;
  className?: string;
}) {
  return (
    <header className={cn("flex flex-wrap items-start justify-between gap-x-6 gap-y-3", className)}>
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <h1 className="min-w-0 text-heading break-words text-fg">{title}</h1>
          {meta}
        </div>
        {description ? <p className="max-w-prose text-body text-fg-2">{description}</p> : null}
        {freshness ? <FreshnessLine freshness={freshness} /> : null}
      </div>
      {primary || secondary ? (
        <div className="flex flex-wrap items-center gap-2 max-sm:w-full [&>*]:max-sm:h-(--vs-control-touch)">
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
