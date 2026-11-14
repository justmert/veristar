import { cn } from "@veristar/brand/cn";
import { ArrowDown, ArrowRight, ArrowUp, CircleAlert, Info } from "lucide-react";
import type { ReactNode } from "react";
import { AnimatedNumber } from "@/components/motion";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";

export type Trend = {
  text: string;
  direction: "up" | "down" | "flat";
  good: boolean | null;
  period: string;
};

// one value per tile with its comparison and period, and a readable trend
// line (R1). a tile that drives the chart below is a toggle button
// (aria-pressed); otherwise it is a plain figure.
export function StatTile({
  label,
  hint,
  value,
  numeric,
  format,
  trend,
  spark,
  state = "data",
  emptyText = "No data in this range",
  pressed,
  onPress,
  className,
  footer,
}: {
  label: string;
  hint?: string;
  value: string;
  // when given, the value counts to its new number on change
  numeric?: number;
  format?: (n: number) => string;
  trend?: Trend | null;
  spark?: ReactNode;
  state?: "data" | "loading" | "empty" | "error";
  emptyText?: string;
  pressed?: boolean;
  onPress?: () => void;
  className?: string;
  footer?: ReactNode;
}) {
  const Comp = onPress ? "button" : "div";
  const arrow =
    trend?.direction === "up" ? (
      <ArrowUp aria-hidden className="size-3" />
    ) : trend?.direction === "down" ? (
      <ArrowDown aria-hidden className="size-3" />
    ) : (
      <ArrowRight aria-hidden className="size-3" />
    );
  return (
    <Comp
      type={onPress ? "button" : undefined}
      aria-pressed={onPress ? pressed : undefined}
      onClick={onPress}
      className={cn(
        "group/tile flex min-w-0 flex-col gap-1.5 rounded-lg border bg-raised px-4 pt-3.5 pb-3 text-left tabular transition duration-(--vs-duration-reveal) ease-standard",
        onPress && "focus-ring hover:border-line-strong hover:bg-hover active:translate-y-px",
        pressed ? "border-fg-disabled bg-overlay hover:bg-overlay" : "border-line",
        className,
      )}
    >
      <span className="flex w-full items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1.5 text-small text-fg-2">
          <span className="truncate">{label}</span>
          {hint ? (
            <Tooltip content={hint}>
              <span
                tabIndex={onPress ? -1 : 0}
                className="inline-flex rounded-full text-fg-3 focus-ring"
                aria-label={hint}
                role="img"
              >
                <Info aria-hidden className="size-3.5" />
              </span>
            </Tooltip>
          ) : null}
        </span>
        {pressed ? (
          <span className="shrink-0 animate-fade-in whitespace-nowrap rounded-full border border-line-strong px-1.5 text-axis font-medium text-fg">
            In chart
          </span>
        ) : null}
      </span>
      {state === "loading" ? (
        <span aria-busy="true" className="flex flex-col gap-2.5 pt-1">
          <Skeleton className="h-7 w-3/5" />
          <Skeleton className="h-3 w-2/5" />
          <Skeleton className="h-10 w-full" />
        </span>
      ) : state === "error" ? (
        <span className="flex flex-col gap-1.5 pt-0.5">
          <span className="font-mono text-metric text-fg-3">n/a</span>
          <span className="flex items-center gap-1.5 text-small text-critical">
            <CircleAlert aria-hidden className="size-3.5" />
            Could not load
          </span>
        </span>
      ) : (
        <>
          <span
            className={cn("font-mono text-metric", state === "empty" ? "text-fg-3" : "text-fg")}
          >
            {numeric !== undefined && format ? (
              <AnimatedNumber value={numeric} format={format} />
            ) : (
              value
            )}
          </span>
          {state === "empty" ? (
            <span className="text-small text-fg-2">{emptyText}</span>
          ) : trend ? (
            <span className="flex items-center gap-1.5 text-caption font-normal">
              <span
                className={cn(
                  "flex items-center gap-0.5 font-mono",
                  trend.good === null || trend.direction === "flat"
                    ? "text-fg-2"
                    : trend.good
                      ? "text-success"
                      : "text-critical",
                )}
              >
                {arrow}
                {trend.text}
              </span>
              <span className="text-fg-3">vs {trend.period}</span>
            </span>
          ) : null}
          {spark ? <span className="mt-auto block pt-1">{spark}</span> : null}
          {footer}
        </>
      )}
    </Comp>
  );
}

// a 48 px trend line for a tile: the current series in blue over the previous
// period, dashed. values are scaled to the tile; the tile shows the number.
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
