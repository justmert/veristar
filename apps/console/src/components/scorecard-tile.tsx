import { cn } from "@veristar/brand/cn";
import type { ReactNode } from "react";
import { ChangeLine, type StatChange } from "@/components/stat-card";
import type { Tone } from "@/components/status";

// the scorecard of the SCF review and the operator project page: five
// tiles (Ownership, Traffic integrity, Web, API, On-chain), each with its
// state dot, its figure, its change and a picture of its range. one tile is
// selected and opens its evidence under the row. the mini pictures are
// exported on their own for the Compare cells, so a cell and a tile draw the
// same thing.

export type ScorecardVisual =
  // proofs in place out of all proofs: one cell per proof
  | { kind: "proofs"; total: number; done: number; labels?: string[] }
  // the integrity grade of each day, oldest first; null is too little
  // traffic to grade
  | { kind: "calendar"; days: (Grade | null)[]; labels?: string[] }
  // the figure per day or hour, oldest first. `running` marks the last bar
  // as the period that is still running
  | {
      kind: "spark";
      values: number[];
      series: 1 | 2 | 3 | 4 | 5;
      running?: boolean;
      labels?: string[];
    }
  // nothing to draw yet (no proven ID)
  | { kind: "none" };

export type Grade = "A" | "B" | "C";

const DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  info: "bg-info",
  neutral: "bg-fg-3",
};

export function ScorecardTile({
  name,
  tone,
  value,
  sub,
  change,
  visual,
  selected,
  onSelect,
  className,
  ...data
}: {
  name: string;
  // the state of the check: success, warning (a step left), critical,
  // neutral (nothing measured yet)
  tone: Tone;
  value: ReactNode;
  // the unit line under the value ("proofs in place", "verified visitors")
  sub?: ReactNode;
  change?: StatChange | null;
  visual: ScorecardVisual;
  selected?: boolean;
  onSelect?: () => void;
  className?: string;
  [attr: `data-${string}`]: string | undefined;
}) {
  const Comp = onSelect ? "button" : "div";
  return (
    <Comp
      type={onSelect ? "button" : undefined}
      aria-pressed={onSelect ? (selected ?? false) : undefined}
      onClick={onSelect}
      {...data}
      className={cn(
        "relative flex min-h-[206px] min-w-0 flex-col gap-2 rounded-2xl border px-4 pt-4 pb-3.5 text-left transition-colors duration-(--vs-duration-press) ease-standard",
        onSelect && "focus-ring hover:border-line-strong",
        selected ? "border-fg bg-hover ring-1 ring-fg" : "border-line bg-raised",
        className,
      )}
    >
      <span
        className={cn("flex items-center gap-2 text-small", selected ? "text-fg" : "text-fg-2")}
      >
        <span aria-hidden className={cn("size-2 shrink-0 rounded-full", DOT[tone])} />
        {name}
      </span>
      <span className="text-[30px] leading-[34px] font-semibold tracking-[-0.02em] text-fg tabular">
        {value}
      </span>
      {change || sub ? (
        <span className="flex flex-wrap items-center gap-x-1 text-[12.5px] text-fg-3 tabular">
          {change ? <ChangeLine change={change} /> : null}
          {sub ? <span>{sub}</span> : null}
        </span>
      ) : null}
      <span className="mt-auto block pt-1">
        <ScorecardVisualView visual={visual} />
      </span>
    </Comp>
  );
}

// the picture of a tile on its own (Compare cells use it)
export function ScorecardVisualView({ visual }: { visual: ScorecardVisual }) {
  switch (visual.kind) {
    case "proofs":
      return <ProofCells total={visual.total} done={visual.done} labels={visual.labels} />;
    case "calendar":
      return <GradeCalendar days={visual.days} labels={visual.labels} />;
    case "spark":
      return (
        <MiniBars
          values={visual.values}
          series={visual.series}
          running={visual.running}
          labels={visual.labels}
        />
      );
    case "none":
      return (
        <span
          aria-hidden
          className="block h-[38px] rounded-lg border border-dashed border-line-2"
        />
      );
  }
}

// one cell per proof: filled for a proof in place
export function ProofCells({
  total,
  done,
  labels,
  className,
}: {
  total: number;
  done: number;
  labels?: string[];
  className?: string;
}) {
  return (
    <span
      role="img"
      aria-label={`${done} of ${total} proofs in place`}
      className={cn("grid gap-1", className)}
      style={{ gridTemplateColumns: `repeat(${Math.max(1, total)}, minmax(0, 1fr))` }}
    >
      {Array.from({ length: total }, (_, i) => (
        <i
          key={i}
          title={labels?.[i]}
          className={cn("block h-3 rounded-[3px]", i < done ? "bg-success/85" : "bg-line")}
        />
      ))}
    </span>
  );
}

const GRADE_CELL: Record<Grade, string> = {
  A: "bg-success/55",
  B: "bg-warning",
  C: "bg-critical",
};

// the grade of each day as a strip of cells: A, B, C, or an empty ring for a
// day with too little traffic to grade
export function GradeCalendar({
  days,
  labels,
  size = "sm",
  className,
}: {
  days: (Grade | null)[];
  labels?: string[];
  // sm: the 12 px cells of a tile; lg: the 22 px cells of the evidence
  size?: "sm" | "lg";
  className?: string;
}) {
  const graded = days.filter((d) => d !== null).length;
  return (
    <span
      role="img"
      aria-label={`${graded} of ${days.length} days graded`}
      className={cn("grid", size === "sm" ? "gap-0.5" : "gap-[3px]", className)}
      style={{ gridTemplateColumns: `repeat(${Math.max(1, days.length)}, minmax(0, 1fr))` }}
    >
      {days.map((g, i) => (
        <i
          key={i}
          title={labels?.[i]}
          className={cn(
            "block",
            size === "sm" ? "h-3 rounded-[2px]" : "h-[22px] rounded-[3px]",
            g ? GRADE_CELL[g] : "border border-line-2",
          )}
        />
      ))}
    </span>
  );
}

const BAR: Record<number, string> = {
  1: "bg-series-1",
  2: "bg-series-2",
  3: "bg-series-3",
  4: "bg-series-4",
  5: "bg-series-5",
};
const EDGE: Record<number, string> = {
  1: "border-series-1",
  2: "border-series-2",
  3: "border-series-3",
  4: "border-series-4",
  5: "border-series-5",
};

// a 38 px bar picture of a range: one bar per day or hour, a zero as a flat
// line, the running period as an outlined bar
export function MiniBars({
  values,
  series,
  running,
  labels,
  className,
}: {
  values: number[];
  series: 1 | 2 | 3 | 4 | 5;
  running?: boolean;
  labels?: string[];
  className?: string;
}) {
  const max = Math.max(1, ...values);
  return (
    <span
      aria-hidden
      className={cn("grid h-[38px] items-end gap-0.5", className)}
      style={{ gridTemplateColumns: `repeat(${Math.max(1, values.length)}, minmax(0, 1fr))` }}
    >
      {values.map((v, i) => {
        const last = running && i === values.length - 1;
        return (
          <i
            key={i}
            title={labels?.[i]}
            className={cn(
              "block rounded-[1px]",
              v === 0
                ? "bg-line"
                : last
                  ? cn("border bg-transparent", EDGE[series])
                  : cn(BAR[series], "opacity-80"),
            )}
            style={{ height: v === 0 ? "2px" : `${Math.max(6, Math.round((v / max) * 100))}%` }}
          />
        );
      })}
    </span>
  );
}
