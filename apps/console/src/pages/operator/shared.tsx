// small parts the operator pages share: the cohort line, the api data
// source, the on-chain summary of a list row and the handover of a
// comparison between the project list and the compare page.

import { cn } from "@veristar/brand/cn";
import type { Cohort } from "@/api/cohorts";
import type { OnchainSummary } from "@/api/onchain";
import { RangeControl } from "@/components/range-control";
import { Missing } from "@/components/states";
import { Tooltip } from "@/components/ui/tooltip";
import { countOf, formatCount, formatDate, utcDay } from "@/lib/format";
import { type DayRange, lastDays, MAX_COMPARE, presetOf, RANGE_PRESETS } from "@/lib/global";
import { weeklyActiveText } from "@/lib/onchain";

// the cohort a project is confirmed in, as one short line
export function cohortText(c: Pick<Cohort, "program" | "round" | "category">): string | null {
  const parts = [c.program, c.round, c.category].filter((x): x is string => !!x);
  return parts.length ? parts.join(", ") : null;
}

export function cohortLine(c: Cohort): string | null {
  const t = cohortText(c);
  if (!t) return null;
  return c.award_date ? `${t}, awarded ${formatDate(c.award_date)}` : t;
}

// ---- api data source

export type ProjectSource = "metric" | "span" | "mixed" | null;

export const SOURCE_DEFINITION =
  "The numbers of a service come from its metrics. A service that sent no metrics in the range is read from its traces. Traces are sampled, so their counts can be lower.";

export function sourceText(
  source: ProjectSource,
  spanServices: number,
  services: number,
): string | null {
  if (source === null) return null;
  if (source === "metric") return "Metrics";
  if (source === "span") return "Traces";
  return `Metrics and traces (${formatCount(spanServices)} of ${countOf(services, "service")} from traces)`;
}

export function SourceText({
  source,
  spanServices,
  services,
}: {
  source: ProjectSource;
  spanServices: number;
  services: number;
}) {
  const t = sourceText(source, spanServices, services);
  if (t === null) return <Missing reason="No requests in this range" />;
  return <span data-source={source}>{t}</span>;
}

// ---- on-chain summary of a list row

export function onchainNote(s: OnchainSummary): string {
  return [
    `${s.ids_verified} of ${countOf(s.ids, "id")} verified`,
    countOf(s.transactions, "transaction"),
    s.days_missing ? `${countOf(s.days_missing, "id day")} without data` : null,
    s.ids_failing ? `${countOf(s.ids_failing, "id")} not read` : null,
    `Weekly active accounts: ${weeklyActiveText(s.weekly_active_accounts)}`,
  ]
    .filter(Boolean)
    .join(". ");
}

// the operations count, with the rest of the summary in a tooltip that
// opens on keyboard focus too
export function OnchainValue({ s }: { s: OnchainSummary | undefined }) {
  if (!s || s.ids === 0) return <Missing reason="No accounts or contracts declared" />;
  const note = onchainNote(s);
  return (
    <Tooltip content={note}>
      <span
        // biome-ignore lint/a11y/noNoninteractiveTabindex: the summary opens on focus too
        tabIndex={0}
        data-col="onchain"
        className={cn("rounded-sm focus-ring", s.ids_verified === 0 && "text-fg-2")}
      >
        {formatCount(s.operations)}
        <span className="sr-only">. {note}</span>
      </span>
    </Tooltip>
  );
}

// ---- comparison handover

export type Selection = { id: string; name: string }[];

// the compare page hands its projects over in the router state when an
// operator goes to add one, so the list starts with them selected
export type CompareHandover = { compare: Selection };

export function handedOver(state: unknown): Selection {
  const c = (state as Partial<CompareHandover> | null)?.compare;
  if (!Array.isArray(c)) return [];
  return c
    .filter(
      (s): s is { id: string; name: string } =>
        typeof s?.id === "string" && typeof s?.name === "string",
    )
    .slice(0, MAX_COMPARE);
}

// the series colour a compared project has, in its slot
export const SERIES_TONES = ["series-1", "series-2", "series-3", "series-4", "series-5"] as const;
const SWATCH = ["bg-series-1", "bg-series-2", "bg-series-3", "bg-series-4", "bg-series-5"];

export function SeriesSwatch({ slot, className }: { slot: number; className?: string }) {
  if (slot < 0) return null;
  return (
    <span
      aria-hidden
      className={cn("inline-block size-2 shrink-0 rounded-bar", SWATCH[slot], className)}
    />
  );
}

// ---- range of utc days

const RANGE_OPTIONS = RANGE_PRESETS.map((n) => ({ value: String(n), label: `${n} days` }));

// the last 7, 30, 90 or 365 utc days, or a custom pair of days. `resolved` is
// the range the server answered for, which the control shows
export function ListRange({
  resolved,
  requested,
  onRange,
}: {
  resolved: DayRange | null;
  requested: DayRange;
  onRange: (r: DayRange) => void;
}) {
  const shown = resolved ?? (requested.from && requested.to ? requested : null);
  const preset = shown ? presetOf(shown.from, shown.to, new Date()) : 30;
  return (
    <RangeControl
      value={preset === null ? "custom" : String(preset)}
      presets={RANGE_OPTIONS}
      onPreset={(v) => onRange(lastDays(Number(v), new Date()))}
      custom={preset === null ? shown : null}
      onCustom={(r) => onRange(r)}
      range={shown}
      maxDays={366}
      maxDay={utcDay()}
    />
  );
}
