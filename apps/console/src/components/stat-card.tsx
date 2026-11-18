import { cn } from "@veristar/brand/cn";
import { ArrowDown, ArrowRight, ArrowUp, ChartColumn, CircleAlert, Info } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { AnimatedNumber } from "@/components/motion";
import { Missing } from "@/components/states";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { formatChange, formatCount } from "@/lib/format";

export type StatChange = {
  // the change as text, from lib/format ("+12%", "-1.2 pp", "no change")
  text: string;
  direction: "up" | "down" | "flat";
  // whether the move is good for the project; null when it is neither
  good: boolean | null;
  // what it is compared with, "previous 30 days"
  period: string;
};

// one number per card: the label (with its definition in a tooltip), the
// value in the metric step (tabular sans; it steps down to fit a narrow
// card), an optional unit, the change against the
// previous period and a sparkline. a card that drives the chart below is a
// toggle (aria-pressed) and shows its selected state. a value that does not
// exist is the missing glyph with the reason, never "0" or "n/a".
//
// the change against the previous period sits right after the value, the
// most visible thing after it: an arrow and the change in its colour, no
// words (the period is read out to assistive technology). without a
// previous value there is no change, and nothing shows.
//
// an optional metric that has no value is not a card of dashes: with
// `hideWhenEmpty` the card draws nothing, and a StatGrid closes the gap.
export function StatCard({
  label,
  definition,
  value,
  format = formatCount,
  unit,
  missingReason = "No data in this range",
  change,
  spark,
  selected,
  onSelect,
  state = "data",
  footer,
  hideWhenEmpty = false,
  className,
  ...data
}: {
  label: string;
  definition?: string;
  // a number counts to its new value when it changes; a string is shown
  // as it is (an already formatted value such as ">10 s")
  value: number | string | null;
  format?: (n: number) => string;
  unit?: string;
  missingReason?: string;
  change?: StatChange | null;
  spark?: ReactNode;
  selected?: boolean;
  onSelect?: () => void;
  state?: "data" | "loading" | "error";
  footer?: ReactNode;
  // draw nothing when the value is missing (state "data" and value null)
  hideWhenEmpty?: boolean;
  className?: string;
  [attr: `data-${string}`]: string | undefined;
}) {
  if (hideWhenEmpty && state === "data" && value === null) return null;
  const Comp = onSelect ? "button" : "div";
  // the value always fits the card: its length sets how far the metric step
  // may shrink on a narrow card (a phone, six cards in a row). the exact
  // value stays; it is never cut or compacted
  const text = value === null ? "" : typeof value === "number" ? format(value) : value;
  const fit = {
    "--metric-chars": Math.max(6, text.length + (unit ? unit.length * 0.6 + 1 : 0)),
  } as CSSProperties;
  return (
    <Comp
      type={onSelect ? "button" : undefined}
      aria-pressed={onSelect ? (selected ?? false) : undefined}
      onClick={onSelect}
      {...data}
      className={cn(
        "group/stat @container flex min-w-0 flex-col gap-2 rounded-lg border bg-raised p-4 text-left transition duration-(--vs-duration-press) ease-standard",
        onSelect &&
          "focus-ring hover:border-line-strong hover:bg-hover active:translate-y-px disabled:pointer-events-none",
        // the card that drives the chart: an ink edge and the chart icon, on
        // the card's own fill (a grey fill reads as pressed or disabled)
        selected ? "border-fg ring-1 ring-fg hover:bg-raised" : "border-line",
        className,
      )}
    >
      <span className="flex w-full items-start justify-between gap-2">
        {/* a label wraps, never truncates: its end often carries the meaning */}
        <span className="flex min-w-0 items-start gap-1 text-caption text-fg-2">
          <span className="min-w-0 break-words">{label}</span>
          {definition ? (
            <Tooltip content={definition}>
              <span
                tabIndex={onSelect ? -1 : 0}
                role="img"
                aria-label={definition}
                className="inline-flex rounded-full text-fg-3 focus-ring"
              >
                <Info aria-hidden className="size-3.5" />
              </span>
            </Tooltip>
          ) : null}
        </span>
        {selected ? (
          // an icon, not a word: a narrow card keeps its full label
          <span className="inline-flex shrink-0 animate-fade-in text-fg" title="In chart">
            <ChartColumn aria-hidden className="size-3.5" />
            <span className="sr-only">In chart</span>
          </span>
        ) : null}
      </span>

      {state === "loading" ? (
        <span aria-busy="true" className="flex flex-col gap-2">
          <span className="sr-only">Loading {label}</span>
          <Skeleton className="h-8 w-3/5" />
          <Skeleton className="h-3 w-2/5" />
        </span>
      ) : state === "error" ? (
        <span className="flex flex-col gap-1">
          <Missing reason="The value did not load" className="text-metric" />
          <span className="flex items-center gap-1 text-small text-critical">
            <CircleAlert aria-hidden className="size-3.5" />
            Did not load
          </span>
        </span>
      ) : (
        <>
          <span
            className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 tabular"
            style={fit}
          >
            <span className="flex min-w-0 items-baseline gap-1">
              {value === null ? (
                <Missing reason={missingReason} className="text-metric" />
              ) : (
                <span className="metric-fit text-fg" data-value="">
                  {typeof value === "number" ? (
                    <AnimatedNumber value={value} format={format} />
                  ) : (
                    value
                  )}
                </span>
              )}
              {unit && value !== null ? <span className="text-small text-fg-2">{unit}</span> : null}
            </span>
            {change && value !== null ? <ChangeLine change={change} /> : null}
          </span>
          {spark ? <span className="mt-auto block pt-1">{spark}</span> : null}
          {footer}
        </>
      )}
    </Comp>
  );
}

// the change of a count against the same count over the previous period;
// nothing to compare with (no previous count) is no change at all
export function countChange(
  current: number,
  previous: number | null | undefined,
  period = "previous period",
): StatChange | null {
  if (previous === null || previous === undefined || previous <= 0) return null;
  const r = (current - previous) / previous;
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: formatChange(r),
    direction,
    good: direction === "flat" ? null : direction === "up",
    period,
  };
}

// the arrow and the change in its colour, after a value; a table cell or a
// figure line passes a smaller text step
export function ChangeLine({ change, className }: { change: StatChange; className?: string }) {
  const flat = change.direction === "flat";
  const Icon =
    change.direction === "up" ? ArrowUp : change.direction === "down" ? ArrowDown : ArrowRight;
  const tone =
    change.good === null || flat ? "text-fg-2" : change.good ? "text-success" : "text-critical";
  return (
    <span
      data-change={change.direction}
      className={cn("flex items-center gap-1 text-body font-semibold tabular", tone, className)}
    >
      <Icon aria-hidden className="size-4" strokeWidth={2.4} />
      {flat ? <span className="sr-only">{change.text}</span> : change.text}
      <span className="sr-only"> vs {change.period}</span>
    </span>
  );
}

// stat cards in a row that closes up: a card that draws nothing (a missing
// optional metric) leaves no gap, and the cards share the row evenly
export function StatGrid({
  children,
  min = "11rem",
  className,
  ...rest
}: {
  children: ReactNode;
  // the narrowest a card gets before the row wraps
  min?: string;
  className?: string;
} & React.HTMLAttributes<HTMLElement>) {
  // a section, so a labelled row of numbers is a region a reader can jump to
  return (
    <section
      className={cn("grid gap-3", className)}
      style={{ gridTemplateColumns: `repeat(auto-fit, minmax(min(${min}, 100%), 1fr))` }}
      {...rest}
    >
      {children}
    </section>
  );
}

// a 40 px trend line for a card: the current series in blue over the
// previous period, dashed. values are scaled to the card; the card shows the
// number.
export function Sparkline({
  values,
  previous,
  label,
}: {
  values: number[];
  previous?: number[];
  label?: string;
}) {
  const all = [...values, ...(previous ?? [])];
  const max = Math.max(1, ...all);
  const min = Math.min(0, ...all);
  const W = 240;
  const H = 40;
  const pts = (vs: number[]) =>
    vs
      .map(
        (v, i) =>
          `${((i * W) / Math.max(1, vs.length - 1)).toFixed(1)},${(H - 2 - ((v - min) / (max - min || 1)) * (H - 6)).toFixed(1)}`,
      )
      .join(" ");
  const last = values.length ? (values[values.length - 1] ?? 0) : 0;
  return (
    <svg
      viewBox={`0 0 ${W} ${H}`}
      width="100%"
      height={H}
      preserveAspectRatio="none"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      {previous?.length ? (
        <polyline
          points={pts(previous)}
          fill="none"
          className="stroke-series-prev"
          strokeWidth="1.5"
          strokeDasharray="4 3"
          vectorEffect="non-scaling-stroke"
        />
      ) : null}
      {values.length ? (
        <>
          <polyline
            points={pts(values)}
            fill="none"
            className="stroke-series-1"
            strokeWidth="2"
            strokeLinejoin="round"
            vectorEffect="non-scaling-stroke"
          />
          <circle
            cx={W}
            cy={H - 2 - ((last - min) / (max - min || 1)) * (H - 6)}
            r="2.5"
            className="fill-series-1"
          />
        </>
      ) : null}
    </svg>
  );
}
