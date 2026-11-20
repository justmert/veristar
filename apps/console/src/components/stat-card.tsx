import { cn } from "@veristar/brand/cn";
import { CircleAlert } from "lucide-react";
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

// the series a figure draws in the chart: its dot carries the colour
export type FigureSeries = 1 | 2 | 3 | 4 | 5 | "neutral" | "success" | "warning" | "critical";
const SERIES_DOT: Record<string, string> = {
  1: "bg-series-1",
  2: "bg-series-2",
  3: "bg-series-3",
  4: "bg-series-4",
  5: "bg-series-5",
  neutral: "bg-fg-3",
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
};

// the figure tile of the canvas (Overview, Web, API, On-chain): a small
// bordered tile with the label after its series dot, the value, and the
// change in its colour (or one action when the figure has no source yet).
// the tile that draws the chart is selected. it is the same component as
// StatCard, under the canvas name
//
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
  series,
  action,
  lead,
  className,
  ...data
}: {
  label: string;
  // the dot before the label: the chart series this figure draws
  series?: FigureSeries;
  // one action in place of the change, for a figure without a source yet
  // ("Connect API"): a link or a button the caller renders
  action?: ReactNode;
  // drawn before the value (the integrity grade's ring)
  lead?: ReactNode;
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
        "group/stat @container flex min-h-[92px] min-w-0 flex-col gap-[3px] rounded-xl border px-3.5 py-3 text-left transition duration-(--vs-duration-press) ease-standard",
        onSelect &&
          "focus-ring hover:border-line-strong active:translate-y-px disabled:pointer-events-none",
        selected ? "border-line-focus bg-hover" : "border-line bg-raised",
        className,
      )}
    >
      <span
        className={cn(
          "flex min-w-0 items-center gap-[7px] text-[12.5px] leading-4",
          selected ? "text-fg" : "text-fg-2",
        )}
      >
        {series ? (
          <span aria-hidden className={cn("size-2 shrink-0 rounded-full", SERIES_DOT[series])} />
        ) : null}
        {definition ? (
          <Tooltip content={definition}>
            <span className="min-w-0 truncate underline decoration-line-strong decoration-dotted underline-offset-[3px]">
              {label}
            </span>
          </Tooltip>
        ) : (
          <span className="min-w-0 truncate">{label}</span>
        )}
        {definition ? <span className="sr-only">. {definition}</span> : null}
        {selected ? <span className="sr-only">. In chart</span> : null}
      </span>

      {state === "loading" ? (
        <span aria-busy="true" className="flex flex-col gap-2 pt-1">
          <span className="sr-only">Loading {label}</span>
          <Skeleton className="h-7 w-3/5" />
          <Skeleton className="h-3 w-2/5" />
        </span>
      ) : state === "error" ? (
        <span className="flex flex-col gap-1">
          <Missing reason="The value did not load" className="text-[24px] leading-[30px]" />
          <span className="flex items-center gap-1 text-caption text-critical">
            <CircleAlert aria-hidden className="size-3.5" />
            Did not load
          </span>
        </span>
      ) : (
        <>
          <span className="flex min-w-0 items-center gap-2 tabular" style={fit}>
            {lead}
            <span className="flex min-w-0 items-baseline gap-1">
              {value === null ? (
                <Missing reason={missingReason} className="text-[24px] leading-[30px]" />
              ) : (
                <span
                  className="metric-fit font-semibold text-fg [--vs-text-metric:24px] [line-height:30px]"
                  data-value=""
                >
                  {typeof value === "number" ? (
                    <AnimatedNumber value={value} format={format} />
                  ) : (
                    value
                  )}
                </span>
              )}
              {unit && value !== null ? <span className="text-small text-fg-2">{unit}</span> : null}
            </span>
          </span>
          {change && value !== null ? (
            <ChangeLine change={change} />
          ) : action ? (
            <span className="text-caption text-fg [&_a]:underline [&_a]:underline-offset-[3px] [&_button]:underline [&_button]:underline-offset-[3px]">
              {action}
            </span>
          ) : null}
          {spark ? <span className="mt-auto block pt-1">{spark}</span> : null}
          {footer}
        </>
      )}
    </Comp>
  );
}

export { StatCard as FigureTile };

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

// the change after a value: a small triangle and the change, green when
// the move is good, red when bad, quiet when neither. the period is read out
// to assistive technology
export function ChangeLine({ change, className }: { change: StatChange; className?: string }) {
  const flat = change.direction === "flat";
  const tone =
    change.good === null || flat ? "text-fg-3" : change.good ? "text-success" : "text-critical";
  return (
    <span
      data-change={change.direction}
      className={cn("flex items-center gap-1 text-[12.5px] leading-4 tabular", tone, className)}
    >
      {flat ? null : (
        // a drawn triangle, not a glyph, so the text of the change stays the change
        <svg aria-hidden="true" viewBox="0 0 10 10" className="size-2 fill-current">
          <path d={change.direction === "up" ? "M5 1 9.5 9h-9z" : "M5 9 .5 1h9z"} />
        </svg>
      )}
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
      className={cn("grid gap-2", className)}
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
