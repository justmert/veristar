import { useInfiniteQuery, useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Download, Info, Plus, Search, X } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import type { Cohort } from "@/api/cohorts";
import { exportPaths, operatorOnchainQuery } from "@/api/onchain";
import {
  apiCompareQuery,
  integrityQuery,
  operatorTenantQuery,
  webCompareQuery,
  webProjectsQuery,
} from "@/api/queries";
import type {
  ApiCompare,
  ApiCompareProject,
  ApiNumbers,
  TenantRef,
  WebCompare,
  WebCompareProject,
  WebDimension,
} from "@/api/schemas";
import { BreakdownList, type TimeBucket, TimeChart } from "@/components/charts";
import { CompareIntegrity } from "@/components/integrity";
import { PageHeader } from "@/components/page-header";
import { GradeCalendar, MiniBars, ProofCells } from "@/components/scorecard-tile";
import { ChangeLine, countChange, type StatChange } from "@/components/stat-card";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  countOf,
  countryName,
  formatCompact,
  formatCount,
  formatDate,
  formatDateRange,
  formatDuration,
  formatLatency,
  formatPercent,
  formatTick,
  niceTicks,
  OPEN_BUCKET,
  utcDay,
} from "@/lib/format";
import {
  compareSearch,
  type DayRange,
  EMPTY_FILTERS,
  MAX_COMPARE,
  readCompare,
} from "@/lib/global";
import { weeklyActiveText } from "@/lib/onchain";
import { CompareOnchainSection } from "./onchain-parts";
import { dailyGrades, proofsOf } from "./project";
import { ListRange, SERIES_TONES, SeriesSwatch, SourceText } from "./shared";

const DIMENSIONS: { dim: WebDimension; label: string }[] = [
  { dim: "referrer", label: "Referrers" },
  { dim: "country", label: "Countries" },
  { dim: "device", label: "Devices" },
  { dim: "browser", label: "Browsers" },
  { dim: "os", label: "Operating systems" },
];

// the series number of each slot, as the swatches and the chart lines use it
const SLOT_SERIES = [1, 2, 4, 5, 3] as const;

// up to five projects side by side (contract 8.4 and 8.6). each project is a
// column with its scf scorecard: the award, the ownership proofs and the
// integrity calendar, then web, api and on-chain with their change and
// their 30-day shape. the best value of each row is bright. a row opens
// its chart above the table, one line per project on one axis.
export function ComparePage() {
  const [params, setParams] = useSearchParams();
  const { tenants, range } = readCompare(params);
  const setRange = (r: DayRange) => setParams(compareSearch(tenants, r), { replace: true });
  const web = useQuery({ ...webCompareQuery(tenants, range), enabled: tenants.length > 0 });
  const apiq = useQuery({ ...apiCompareQuery(tenants, range), enabled: tenants.length > 0 });
  const [picking, setPicking] = useState(false);
  const [row, setRow] = useState<RowKey | null>(null);

  if (tenants.length === 0) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Compare" />
        <Picker
          range={range}
          initial={[]}
          onPick={(ids) => setParams(compareSearch(ids, range), { replace: true })}
        />
      </div>
    );
  }

  const resolved = web.data
    ? { from: web.data.from, to: web.data.to }
    : apiq.data
      ? { from: apiq.data.from, to: apiq.data.to }
      : null;
  // when the web numbers were last read; the api compare reads live
  const readAt = web.data?.read_at ?? apiq.data?.read_at ?? null;
  const remove = (id: string) =>
    setParams(
      compareSearch(
        tenants.filter((t) => t !== id),
        resolved ?? range,
      ),
      { replace: true },
    );

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Compare"
        items={[
          resolved ? (
            <span key="range">{`${formatDateRange(resolved.from, resolved.to)}, UTC`}</span>
          ) : null,
        ]}
        freshness={
          readAt
            ? {
                at: readAt,
                cadence: "Web numbers are read every hour. API numbers refresh every 5 minutes.",
              }
            : null
        }
        secondary={
          <>
            <ListRange resolved={resolved} requested={range} onRange={setRange} />
            <AboutNumbers />
            {tenants.length < MAX_COMPARE ? (
              <Button variant="outline" onClick={() => setPicking(true)}>
                <Plus aria-hidden />
                Add a project
              </Button>
            ) : null}
          </>
        }
      />

      <Dialog open={picking} onOpenChange={setPicking} title="Add a project" size="md">
        {picking ? (
          <Picker
            range={resolved ?? range}
            initial={tenants}
            bare
            onPick={(ids) => {
              setPicking(false);
              setParams(compareSearch(ids, resolved ?? range), { replace: true });
            }}
          />
        ) : null}
      </Dialog>

      {web.isPending || apiq.isPending ? (
        <LoadingState label="Loading the numbers">
          <Skeleton.Table rows={10} columns={Math.min(5, tenants.length + 1)} />
        </LoadingState>
      ) : web.error && apiq.error ? (
        <ErrorState
          title="The numbers did not load"
          error={web.error}
          onRetry={() => {
            web.refetch();
            apiq.refetch();
          }}
        />
      ) : (
        <Scorecards
          web={web.data}
          api={apiq.data}
          webError={web.error}
          apiError={apiq.error}
          onRetry={() => {
            web.refetch();
            apiq.refetch();
          }}
          resolved={resolved}
          tenants={tenants}
          row={row}
          onRow={setRow}
          onRemove={remove}
        />
      )}
    </div>
  );
}

// what the numbers are, behind the (i) next to the period
function AboutNumbers() {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        aria-label="About these numbers"
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
      >
        <Info aria-hidden />
      </Button>
      <Dialog open={open} onOpenChange={setOpen} title="About these numbers" size="sm">
        <ul className="flex list-disc flex-col gap-1.5 pb-4 pl-5 text-body text-fg-2">
          <li>Every number comes from daily totals of UTC days.</li>
          <li>A hatched day is still running. Its value grows until the day ends.</li>
          <li>A gap in a web chart is a day that the web analytics have not read yet.</li>
          <li>Visitors are unique per UTC day and site.</li>
          <li>Latency percentiles come from summed duration buckets, not from averages.</li>
          <li>The brightest value of a row is the best of the compared projects.</li>
        </ul>
      </Dialog>
    </>
  );
}

// ---- the picker

// the empty state and the add dialog: the projects to pick, searched by
// name, each with its scf award or its status, up to the limit
function Picker({
  range,
  initial,
  onPick,
  bare = false,
}: {
  range: DayRange;
  initial: string[];
  onPick: (ids: string[]) => void;
  bare?: boolean;
}) {
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setSearch(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);
  const list = useInfiniteQuery(
    webProjectsQuery({
      range,
      filters: { ...EMPTY_FILTERS, q: search },
      sort: "name",
      order: "asc",
    }),
  );
  const rows = list.data?.pages.flatMap((p) => p.items) ?? [];
  const total = list.data?.pages[0]?.total ?? null;
  const [picked, setPicked] = useState<string[]>(initial);
  const full = picked.length >= MAX_COMPARE;
  const toggle = (id: string) =>
    setPicked((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]));
  return (
    <section
      aria-labelledby="picker-h"
      className={cn(
        "flex min-w-0 flex-col",
        !bare &&
          "mx-auto w-full max-w-[38.5rem] overflow-hidden rounded-2xl border border-line bg-raised",
      )}
    >
      <div className={cn("flex flex-col gap-4", bare ? "pb-3" : "px-6 pt-6 pb-4")}>
        <h2
          id="picker-h"
          className={cn("text-section font-semibold text-fg leading-5", bare && "sr-only")}
        >
          Pick up to {MAX_COMPARE} projects
        </h2>
        <div className="relative">
          <Search
            aria-hidden
            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-3"
          />
          <Input
            aria-label="Search projects"
            placeholder={
              total !== null && search === ""
                ? `Search ${formatCount(total)} projects`
                : "Search projects"
            }
            value={q}
            onChange={(e) => setQ(e.target.value)}
            className="pl-9"
          />
        </div>
      </div>
      {list.isPending ? (
        <div className="px-6 pb-4">
          <LoadingState label="Loading the projects">
            <Skeleton.Table rows={6} columns={2} />
          </LoadingState>
        </div>
      ) : list.error ? (
        <div className="px-6 pb-4">
          <ErrorState
            compact
            title="The projects did not load"
            error={list.error}
            onRetry={() => list.refetch()}
          />
        </div>
      ) : rows.length === 0 ? (
        <p className="px-6 pb-6 text-small text-fg-3">No project matches.</p>
      ) : (
        <ul aria-label="Projects" className="max-h-[22rem] overflow-y-auto border-t border-line">
          {rows.map((p) => {
            const on = picked.includes(p.tenant.id);
            const id = `pick-${p.tenant.id}`;
            const award = [p.cohort.round, p.cohort.program].filter(Boolean);
            return (
              <li key={p.tenant.id} className="border-b border-line last:border-b-0">
                <label
                  htmlFor={id}
                  className={cn(
                    "flex min-h-11 cursor-pointer items-center gap-4 px-5 py-2.5 hover:bg-hover",
                    !on && full && "cursor-not-allowed opacity-60",
                  )}
                >
                  <Checkbox
                    id={id}
                    checked={on}
                    disabled={!on && full}
                    onCheckedChange={() => toggle(p.tenant.id)}
                  />
                  <span className="min-w-0 flex-1 truncate text-body text-fg">{p.tenant.name}</span>
                  <span className="flex shrink-0 gap-1.5 text-small text-fg-3">
                    {p.tenant.status !== "active" ? (
                      <StatusBadge kind="project" status={p.tenant.status} operator />
                    ) : award.length ? (
                      award.map((a) => <span key={a}>{a}</span>)
                    ) : (
                      "No SCF award"
                    )}
                  </span>
                </label>
              </li>
            );
          })}
          {list.hasNextPage ? (
            <li className="px-5 py-3">
              <Button
                variant="outline"
                size="sm"
                onClick={() => list.fetchNextPage()}
                pending={list.isFetchingNextPage}
              >
                {list.isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            </li>
          ) : null}
        </ul>
      )}
      <div
        className={cn(
          "flex flex-wrap items-center justify-between gap-3 border-t border-line",
          bare ? "pt-3 pb-4" : "px-5 py-3.5",
        )}
      >
        <span className="text-small text-fg-2" aria-live="polite">
          {picked.length} of {MAX_COMPARE} picked
        </span>
        <Button disabled={picked.length === 0} onClick={() => onPick(picked)}>
          {picked.length === 0 ? "Compare" : `Compare ${countOf(picked.length, "project")}`}
        </Button>
      </div>
    </section>
  );
}
// ---- charts

// the one partial key of the page: the hatching that marks the running day
// in every chart
function PartialKey() {
  return (
    <span data-legend="partial" className="flex items-center gap-1 text-caption text-fg-2">
      <svg aria-hidden width="10" height="10" className="rounded-bar">
        <defs>
          <pattern
            id="compare-partial"
            width="4"
            height="4"
            patternUnits="userSpaceOnUse"
            patternTransform="rotate(45)"
          >
            <rect width="4" height="4" className="fill-series-prev" />
            <rect width="1.5" height="4" fill="var(--vs-series-partial)" />
          </pattern>
        </defs>
        <rect width="10" height="10" fill="url(#compare-partial)" />
      </svg>
      Partial: today is still running
    </span>
  );
}

// the axis of a chart uses the same ticks as the chart: from 0 to the
// largest value of every series
function ticksOf(values: (number | null)[][]): number[] {
  return niceTicks(Math.max(0, ...values.flat().map((v) => v ?? 0)));
}

// a rate axis keeps the decimals its steps need: 0.25%, 0.5%, 0.75%, not
// 0.3%, 0.5%, 0.8%
export function percentAxis(values: (number | null)[][]): (n: number) => string {
  const ticks = ticksOf(values).map((t) => t * 100);
  const places =
    [0, 1, 2, 3].find((d) => ticks.every((t) => Math.abs(Number(t.toFixed(d)) - t) < 1e-9)) ?? 4;
  return (n) => (n === 0 ? "0%" : `${(n * 100).toFixed(places)}%`);
}

// a latency axis in one unit: milliseconds while the largest tick is under
// a second, else seconds
export function latencyAxis(values: (number | null)[][]): (n: number) => string {
  const top = ticksOf(values).at(-1) ?? 0;
  if (top < 1) return (n) => `${Math.round(n * 1000)} ms`;
  return (n) => `${Number(n.toFixed(2))} s`;
}

function bucketsOf(days: string[]): TimeBucket[] {
  const today = utcDay();
  return days.map((d) => ({
    at: d,
    label: `${formatDate(d)}, UTC`,
    tick: formatTick(d, "day"),
    partial: d === today,
  }));
}

function markersOf(projects: { tenant: TenantRef; cohort: Cohort }[]) {
  return projects.flatMap((p) =>
    p.cohort.award_date ? [{ at: p.cohort.award_date, label: `${p.tenant.name} awarded` }] : [],
  );
}

// a chart only draws when a complete day has a value: a range whose only
// value is the running day says so, instead of an empty frame
export function chartState(
  buckets: TimeBucket[],
  series: (number | null)[][],
): "data" | "empty" | "no-complete" {
  const has = (i: number) => series.some((s) => s[i] !== null && s[i] !== undefined);
  if (!buckets.some((_, i) => has(i))) return "empty";
  if (!buckets.some((b, i) => !b.partial && has(i))) return "no-complete";
  return "data";
}

// one series per compared project with data, in the project's slot, so a
// project keeps its colour when another one has no data and is left out.
// a range whose data sits in one day draws the chart's first-data state.
type Slotted = { tenant: TenantRef; slot: number };

function CompareChart({
  title,
  description,
  buckets,
  projects,
  values,
  format,
  axisFormat,
  markers,
  ceiling,
  higherIsBetter = true,
  rate = false,
  legend = false,
}: {
  legend?: boolean;
  title: string;
  description?: string;
  buckets: TimeBucket[];
  projects: Slotted[];
  values: (number | null)[][];
  format: (n: number) => string;
  axisFormat: (n: number) => string;
  markers: { at: string; label: string }[];
  ceiling?: { value: number; label: string };
  higherIsBetter?: boolean;
  // a rate's zero is a real reading, so its line starts at it
  rate?: boolean;
}) {
  const st = chartState(buckets, values);
  return (
    <TimeChart
      title={title}
      description={description}
      headingLevel={3}
      kind="line"
      buckets={buckets}
      series={projects.map((p, i) => ({
        key: p.tenant.id,
        label: p.tenant.name,
        tone: SERIES_TONES[p.slot] ?? "series-1",
        values: values[i] ?? [],
      }))}
      format={format}
      axisFormat={axisFormat}
      higherIsBetter={higherIsBetter}
      markers={markers}
      ceiling={ceiling}
      state={st === "empty" ? "empty" : "data"}
      legend={legend}
      leadingZeros={rate ? "keep" : "gap"}
      height={180}
    />
  );
}

// ---- api

// a percentile above 10 s has no number: it is drawn on the 10 s line and
// read as ">10 s"
const OVER = 10.000001;

// the 10 s line, drawn only when a value is above it
const CEILING = { value: 10, label: ">10 s above" };

function pct(n: ApiNumbers, k: "p50" | "p95" | "p99"): number | null {
  if (n[`${k}_over_10s`]) return OVER;
  return n[`${k}_seconds`];
}

function ServicesCard({ project, slot }: { project: ApiCompareProject; slot: number }) {
  const t = project.totals;
  return (
    <section
      aria-label={`Services of ${project.tenant.name}`}
      className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-4"
    >
      <p className="flex min-w-0 items-center gap-2 text-body font-medium text-fg">
        <SeriesSwatch slot={slot} />
        <span className="truncate">{project.tenant.name}</span>
      </p>
      <p className="text-caption text-fg-2">
        <SourceText source={t.source} spanServices={t.span_services} services={t.services} />
        {t.lossy ? ". Some latency buckets are approximate." : null}
      </p>
      {project.services.length === 0 ? (
        <p className="text-small text-fg-2">No requests in this range.</p>
      ) : (
        <ul className="flex flex-col">
          {project.services.map((s) => (
            <li key={s.service} className="border-b border-line-subtle py-2 last:border-0">
              <div className="flex items-baseline justify-between gap-2">
                <span className="min-w-0 truncate font-mono text-caption text-fg">
                  {s.service === "" ? "Service without a name" : s.service}
                </span>
                <span className="shrink-0 text-small text-fg tabular">
                  {formatCount(s.requests)}
                </span>
              </div>
              <p className="text-caption text-fg-2">
                {countOf(s.routes, "route")}, 5xx {rateText(s.rate_5xx)}, p95 {latencyText(s)}
                {s.source === "span" ? ", from traces" : ""}
                {s.lossy ? ", approximate" : ""}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function rateText(v: number | null): string {
  return v === null ? "not measured" : formatPercent(v);
}

function latencyText(n: ApiNumbers): string {
  const v = pct(n, "p95");
  if (v === null) return "not measured";
  return v > 10 ? OPEN_BUCKET : formatLatency(v);
}

// ---- the scorecards

type RowKey = string;

type Cell = {
  // the number the best value is chosen on; null is no data
  n: number | null;
  text?: ReactNode;
  change?: StatChange | null;
  spark?: number[];
  visual?: ReactNode;
};

type RowDef = {
  key: RowKey;
  label: string;
  cell: (i: number) => Cell;
  // which end is best; null compares nothing
  better: "high" | "low" | null;
  // a big row: the value large, its shape under it
  big?: boolean;
  // the row draws a chart above the table, or opens a detail
  chart?: {
    values: (i: number) => (number | null)[];
    format: (n: number) => string;
    axis: (v: (number | null)[][]) => (n: number) => string;
    higherIsBetter?: boolean;
    rate?: boolean;
    ceiling?: boolean;
  };
  detail?: ReactNode;
};

type Group = { key: string; label: string; actions?: ReactNode; rows: RowDef[] };

function change(cur: number, prev: number): StatChange | null {
  return countChange(cur, prev);
}

const compactCount = (n: number) => (n >= 100_000 ? formatCompact(n) : formatCount(n));
const countAxis = () => formatCompact;

function Scorecards({
  web,
  api,
  webError,
  apiError,
  onRetry,
  resolved,
  tenants,
  row,
  onRow,
  onRemove,
}: {
  web: WebCompare | undefined;
  api: ApiCompare | undefined;
  webError: unknown;
  apiError: unknown;
  onRetry: () => void;
  resolved: DayRange | null;
  tenants: string[];
  row: RowKey | null;
  onRow: (k: RowKey | null) => void;
  onRemove: (id: string) => void;
}) {
  const range = resolved ?? { from: "", to: "" };
  const navigate = useNavigate();
  const [dialog, setDialog] = useState<"breakdowns" | "services" | "onchain" | null>(null);
  const details = useQueries({ queries: tenants.map((id) => operatorTenantQuery(id)) });
  const chains = useQueries({
    queries: tenants.map((id) => operatorOnchainQuery(id, range)),
  });
  const reports = useQueries({
    queries: tenants.map((id) => integrityQuery("operator", id, range)),
  });
  const byId = <T extends { tenant: TenantRef }>(list: T[] | undefined) =>
    new Map((list ?? []).map((x) => [x.tenant.id, x]));
  const W = byId(web?.projects);
  const A = byId(api?.projects);
  const cols = tenants.map((id, slot) => {
    const w = W.get(id);
    const a = A.get(id);
    const tenant = w?.tenant ?? a?.tenant ?? details[slot]?.data?.tenant ?? null;
    return { id, slot, w, a, tenant, cohort: w?.cohort ?? a?.cohort ?? null };
  });
  const days = web?.days ?? api?.days ?? [];
  const running = days.at(-1) === utcDay();
  const labels = days.map((d) => formatDate(d));
  const buckets = bucketsOf(days);
  const markers = markersOf(
    cols.flatMap((c) =>
      c.tenant && c.cohort ? [{ tenant: c.tenant as TenantRef, cohort: c.cohort }] : [],
    ),
  );
  const series = (i: number) => SLOT_SERIES[cols[i]?.slot ?? 0] ?? 1;

  const proofs = cols.map((_, i) => {
    const d = details[i]?.data;
    const ch = chains[i];
    return d && (ch?.data || ch?.error) ? proofsOf(d.sites, ch.data) : null;
  });
  const none: Cell = { n: null };
  const webCell = (i: number, f: (w: WebCompareProject) => Cell): Cell => {
    const w = cols[i]?.w;
    return w && (w.totals.pageviews > 0 || w.totals.visits > 0) ? f(w) : none;
  };
  const apiCell = (i: number, f: (a: ApiCompareProject) => Cell): Cell => {
    const a = cols[i]?.a;
    return a && a.totals.requests > 0 ? f(a) : none;
  };
  const chainOf = (i: number) => cols[i]?.w?.onchain ?? cols[i]?.a?.onchain;
  const chainCell = (i: number, f: (c: NonNullable<ReturnType<typeof chainOf>>) => Cell): Cell => {
    const c = chainOf(i);
    return c && c.totals.ids_verified > 0 ? f(c) : none;
  };
  const rate = (v: number | null): Cell => (v === null ? none : { n: v, text: formatPercent(v) });
  const latency = (n: ApiNumbers, k: "p50" | "p95" | "p99"): Cell => {
    const v = pct(n, k);
    return v === null ? none : { n: v, text: v > 10 ? OPEN_BUCKET : formatLatency(v) };
  };
  const award = (k: "program" | "round" | "category" | "award_date"): RowDef => ({
    key: `award-${k}`,
    label: k === "award_date" ? "Award date" : k.charAt(0).toUpperCase() + k.slice(1),
    better: null,
    cell: (i) => {
      const v = cols[i]?.cohort?.[k] ?? null;
      return v ? { n: 0, text: k === "award_date" ? formatDate(v) : v } : none;
    },
  });

  const groups: Group[] = [
    {
      key: "award",
      label: "SCF award",
      rows: [award("program"), award("round"), award("category"), award("award_date")],
    },
    {
      key: "trust",
      label: "Trust",
      rows: [
        {
          key: "ownership",
          label: "Ownership",
          better: "high",
          cell: (i) => {
            const pr = proofs[i];
            if (!pr) return details[i]?.error ? none : { n: null, text: "…" };
            const done = pr.filter((x) => x.done).length;
            return {
              n: pr.length ? done / pr.length : 0,
              text: `${done} of ${pr.length}`,
              visual: <ProofCells total={pr.length} done={done} labels={pr.map((x) => x.label)} />,
            };
          },
        },
        {
          key: "integrity",
          label: "Traffic integrity",
          better: "low",
          cell: (i) => {
            const g = cols[i]?.w?.integrity;
            if (!g || g.grade === null) return none;
            const rep = reports[i]?.data;
            return {
              n: { A: 0, B: 1, C: 2 }[g.grade],
              text: `Grade ${g.grade}`,
              visual: rep ? <GradeCalendar days={dailyGrades(rep, days)} labels={labels} /> : null,
            };
          },
          detail: web ? <CompareIntegrity projects={web.projects} /> : null,
        },
      ],
    },
    {
      key: "web",
      label: "Web",
      actions: web ? (
        <>
          <button type="button" onClick={() => setDialog("breakdowns")} className={LINK}>
            Referrers, countries, devices
          </button>
          {resolved ? (
            <a href={exportPaths.compare("web", tenants, resolved)} download className={LINK}>
              <Download aria-hidden className="size-3.5" />
              CSV<span className="sr-only"> of the web numbers</span>
            </a>
          ) : null}
        </>
      ) : null,
      rows: webError
        ? []
        : [
            {
              key: "visitors",
              label: "Verified visitors",
              better: "high",
              big: true,
              cell: (i) =>
                webCell(i, (w) => ({
                  n: w.totals.visitors,
                  text: compactCount(w.totals.visitors),
                  change: change(w.totals.visitors, w.previous_totals.visitors),
                  spark: w.series.map((x) => x.visitors ?? 0),
                })),
              chart: {
                values: (i) => cols[i]?.w?.series.map((x) => x.visitors) ?? [],
                format: formatCount,
                axis: countAxis,
              },
            },
            {
              key: "pageviews",
              label: "Page views",
              better: "high",
              cell: (i) =>
                webCell(i, (w) => ({
                  n: w.totals.pageviews,
                  text: formatCount(w.totals.pageviews),
                  change: change(w.totals.pageviews, w.previous_totals.pageviews),
                })),
              chart: {
                values: (i) => cols[i]?.w?.series.map((x) => x.pageviews) ?? [],
                format: formatCount,
                axis: countAxis,
              },
            },
            {
              key: "visits",
              label: "Visits",
              better: "high",
              cell: (i) =>
                webCell(i, (w) => ({
                  n: w.totals.visits,
                  text: formatCount(w.totals.visits),
                  change: change(w.totals.visits, w.previous_totals.visits),
                })),
              chart: {
                values: (i) => cols[i]?.w?.series.map((x) => x.visits) ?? [],
                format: formatCount,
                axis: countAxis,
              },
            },
            {
              key: "bounce_rate",
              label: "Bounce rate",
              better: "low",
              cell: (i) => webCell(i, (w) => rate(w.totals.bounce_rate)),
              chart: {
                values: (i) => cols[i]?.w?.series.map((x) => x.bounce_rate) ?? [],
                format: formatPercent,
                axis: percentAxis,
                higherIsBetter: false,
                rate: true,
              },
            },
            {
              key: "avg_visit",
              label: "Average visit",
              better: "high",
              cell: (i) =>
                webCell(i, (w) =>
                  w.totals.avg_visit_duration === null
                    ? none
                    : {
                        n: w.totals.avg_visit_duration,
                        text: formatDuration(w.totals.avg_visit_duration),
                      },
                ),
              chart: {
                values: (i) => cols[i]?.w?.series.map((x) => x.avg_visit_duration) ?? [],
                format: formatDuration,
                axis: () => formatDuration,
              },
            },
            {
              key: "held",
              label: "Held back",
              better: "low",
              cell: (i) =>
                webCell(i, (w) =>
                  w.totals.quarantine_share === null
                    ? none
                    : {
                        n: w.totals.quarantine_share,
                        text: (
                          <span title={`${formatCount(w.totals.quarantine_pageviews)} page views`}>
                            {formatPercent(w.totals.quarantine_share)}
                          </span>
                        ),
                      },
                ),
            },
          ],
    },
    {
      key: "api",
      label: "API",
      actions: api ? (
        <>
          <button type="button" onClick={() => setDialog("services")} className={LINK}>
            Services of each project
          </button>
          {resolved ? (
            <a href={exportPaths.compare("api", tenants, resolved)} download className={LINK}>
              <Download aria-hidden className="size-3.5" />
              CSV<span className="sr-only"> of the API numbers</span>
            </a>
          ) : null}
        </>
      ) : null,
      rows: apiError
        ? []
        : [
            {
              key: "requests",
              label: "Requests",
              better: "high",
              big: true,
              cell: (i) =>
                apiCell(i, (a) => ({
                  n: a.totals.requests,
                  text: compactCount(a.totals.requests),
                  change: change(a.totals.requests, a.previous_totals.requests),
                  spark: a.series.map((x) => x.requests),
                })),
              chart: {
                values: (i) => cols[i]?.a?.series.map((x) => x.requests) ?? [],
                format: formatCount,
                axis: countAxis,
              },
            },
            {
              key: "rate_5xx",
              label: "5xx rate",
              better: "low",
              cell: (i) => apiCell(i, (a) => rate(a.totals.rate_5xx)),
              chart: {
                values: (i) => cols[i]?.a?.series.map((x) => x.rate_5xx) ?? [],
                format: formatPercent,
                axis: percentAxis,
                higherIsBetter: false,
                rate: true,
              },
            },
            {
              key: "rate_4xx",
              label: "4xx rate",
              better: "low",
              cell: (i) => apiCell(i, (a) => rate(a.totals.rate_4xx)),
              chart: {
                values: (i) => cols[i]?.a?.series.map((x) => x.rate_4xx) ?? [],
                format: formatPercent,
                axis: percentAxis,
                higherIsBetter: false,
                rate: true,
              },
            },
            {
              key: "p95",
              label: "p95 latency",
              better: "low",
              cell: (i) => apiCell(i, (a) => latency(a.totals, "p95")),
              chart: {
                values: (i) => cols[i]?.a?.series.map((x) => pct(x, "p95")) ?? [],
                format: formatLatency,
                axis: latencyAxis,
                higherIsBetter: false,
                ceiling: true,
              },
            },
            {
              key: "p50",
              label: "p50 latency",
              better: "low",
              cell: (i) => apiCell(i, (a) => latency(a.totals, "p50")),
            },
            {
              key: "p99",
              label: "p99 latency",
              better: "low",
              cell: (i) => apiCell(i, (a) => latency(a.totals, "p99")),
            },
            {
              key: "infra",
              label: "Infrastructure requests",
              better: null,
              cell: (i) =>
                apiCell(i, (a) => ({
                  n: a.totals.infrastructure_requests,
                  text: formatCount(a.totals.infrastructure_requests),
                })),
            },
            {
              key: "services",
              label: "Services",
              better: null,
              cell: (i) =>
                apiCell(i, (a) => ({ n: a.totals.services, text: formatCount(a.totals.services) })),
            },
            {
              key: "routes",
              label: "Routes",
              better: null,
              cell: (i) =>
                apiCell(i, (a) => ({ n: a.totals.routes, text: formatCount(a.totals.routes) })),
            },
          ],
    },
    {
      key: "onchain",
      label: "On-chain",
      actions: web ? (
        <button type="button" onClick={() => setDialog("onchain")} className={LINK}>
          Weekly activity
        </button>
      ) : null,
      rows: [
        {
          key: "operations",
          label: "Operations",
          better: "high",
          big: true,
          cell: (i) =>
            chainCell(i, (c) => ({
              n: c.totals.operations,
              text: compactCount(c.totals.operations),
              spark: c.series.map((x) => x.operations ?? 0),
            })),
          chart: {
            values: (i) => chainOf(i)?.series.map((x) => x.operations) ?? [],
            format: formatCount,
            axis: countAxis,
          },
        },
        {
          key: "transactions",
          label: "Transactions",
          better: "high",
          cell: (i) =>
            chainCell(i, (c) => ({
              n: c.totals.transactions,
              text: formatCount(c.totals.transactions),
            })),
        },
        {
          key: "ids",
          label: "IDs verified",
          better: null,
          cell: (i) => {
            const c = chainOf(i);
            return c && c.totals.ids > 0
              ? { n: c.totals.ids_verified, text: `${c.totals.ids_verified} of ${c.totals.ids}` }
              : none;
          },
        },
        {
          key: "weekly",
          label: "Weekly active accounts",
          better: "high",
          cell: (i) =>
            chainCell(i, (c) => ({
              n: c.totals.weekly_active_accounts.average,
              text: weeklyActiveText(c.totals.weekly_active_accounts),
            })),
        },
      ],
    },
  ];

  const all = groups.flatMap((g) => g.rows);
  const open = row ? (all.find((r) => r.key === row) ?? null) : null;
  const slotted = cols.map((c) => ({
    tenant: (c.tenant ?? { id: c.id, name: c.id }) as TenantRef,
    slot: c.slot,
  }));
  const periodDays = days.length;

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {webError || apiError ? (
        <ErrorState
          compact
          title={webError ? "The web numbers did not load" : "The API numbers did not load"}
          error={webError ?? apiError}
          onRetry={onRetry}
        />
      ) : null}
      {open && (open.chart || open.detail) ? (
        <section
          aria-label={open.label}
          className="relative min-w-0 rounded-2xl border border-line bg-raised p-5"
        >
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Close the ${open.label.toLowerCase()} chart`}
            onClick={() => onRow(null)}
            className="absolute top-3 right-3 z-10"
          >
            <X aria-hidden />
          </Button>
          {open.chart ? (
            <CompareChart
              title={open.label}
              description="per day, UTC"
              buckets={buckets}
              projects={slotted}
              values={cols.map((_, i) => open.chart?.values(i) ?? [])}
              format={open.chart.format}
              axisFormat={open.chart.axis(cols.map((_, i) => open.chart?.values(i) ?? []))}
              markers={markers}
              higherIsBetter={open.chart.higherIsBetter}
              rate={open.chart.rate}
              legend
              ceiling={
                open.chart.ceiling &&
                cols.some((_, i) => (open.chart?.values(i) ?? []).some((v) => v !== null && v > 10))
                  ? CEILING
                  : undefined
              }
            />
          ) : (
            open.detail
          )}
          {open.chart && running ? (
            <div className="mt-2">
              <PartialKey />
            </div>
          ) : null}
        </section>
      ) : null}

      {/* relative: the visually hidden texts of the cells are positioned
        absolutely, and without a positioned scroller they escape it and
        widen the page on a phone */}
      <div className="relative min-w-0 overflow-x-auto rounded-2xl border border-line bg-raised">
        <table className="w-full min-w-[44rem] border-separate border-spacing-0 text-body tabular">
          <caption className="sr-only">The compared projects, one column each</caption>
          <colgroup>
            <col className="w-[11.5rem]" />
            {cols.map((c) => (
              <col key={c.id} />
            ))}
          </colgroup>
          <thead>
            <tr>
              <th
                scope="col"
                className="border-b border-line bg-sunken px-4 py-3 text-left text-small font-normal text-fg-3"
              >
                {periodDays > 0 ? `Last ${countOf(periodDays, "day")}` : "Range"}
              </th>
              {cols.map((c) => (
                <th
                  key={c.id}
                  scope="col"
                  data-tenant={c.id}
                  className="border-b border-l border-line bg-sunken px-4 py-3 text-left align-middle font-normal"
                >
                  <span className="flex min-w-0 items-start gap-2">
                    <SeriesSwatch slot={c.slot} className="mt-1.5" />
                    <span className="flex min-w-0 flex-1 flex-col gap-1">
                      <Link
                        to={`/operator/projects/${c.id}`}
                        className="line-clamp-2 rounded-sm font-semibold text-fg underline-offset-4 hover:underline focus-ring"
                        title={c.tenant?.name}
                      >
                        {c.tenant?.name ?? "Project"}
                      </Link>
                      {c.tenant && c.tenant.status !== "active" ? (
                        <StatusBadge kind="project" status={c.tenant.status} operator />
                      ) : null}
                    </span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      className="-mt-0.5 shrink-0 max-lg:size-(--vs-control-touch)"
                      aria-label={`Remove ${c.tenant?.name ?? "the project"} from the comparison`}
                      onClick={() => onRemove(c.id)}
                    >
                      <X aria-hidden />
                    </Button>
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          {groups.map((g) => (
            <tbody key={g.key} data-group={g.key}>
              <tr>
                <th
                  scope="colgroup"
                  colSpan={cols.length + 1}
                  className="border-b border-line bg-raised px-4 pt-4 pb-2 text-left font-normal"
                >
                  <span className="flex flex-wrap items-center gap-x-5 gap-y-1">
                    <span className="text-small font-semibold text-fg">{g.label}</span>
                    {g.actions ? (
                      <span className="ml-auto flex flex-wrap items-center gap-x-5">
                        {g.actions}
                      </span>
                    ) : null}
                  </span>
                </th>
              </tr>
              {g.rows.map((r) => (
                <ScoreRow
                  key={r.key}
                  row={r}
                  count={cols.length}
                  series={series}
                  running={running}
                  labels={labels}
                  open={row === r.key}
                  onOpen={
                    r.chart || r.detail ? () => onRow(row === r.key ? null : r.key) : undefined
                  }
                />
              ))}
            </tbody>
          ))}
        </table>
      </div>

      <Dialog
        open={dialog !== null}
        onOpenChange={(o) => {
          if (!o) setDialog(null);
        }}
        title={
          dialog === "breakdowns"
            ? "Referrers, countries, devices, browsers and systems"
            : dialog === "services"
              ? "Services of each project"
              : "On-chain activity"
        }
        className="max-w-[min(64rem,calc(100vw-2rem))]"
      >
        <div className="max-h-[70vh] overflow-y-auto pb-4">
          {dialog === "breakdowns" && web ? (
            <Breakdowns projects={cols.flatMap((c) => (c.w ? [{ ...c.w, slot: c.slot }] : []))} />
          ) : dialog === "services" && api ? (
            <div className="grid gap-4 md:grid-cols-2">
              {cols.flatMap((c) =>
                c.a && c.a.totals.requests > 0
                  ? [<ServicesCard key={c.id} project={c.a} slot={c.slot} />]
                  : [],
              )}
            </div>
          ) : dialog === "onchain" && web ? (
            <CompareOnchainSection projects={web.projects} />
          ) : null}
        </div>
      </Dialog>
      {/* a removed id or a stale link leaves no column behind */}
      {cols.length === 0 ? (
        <Button variant="outline" onClick={() => navigate("/operator/compare")}>
          Pick projects
        </Button>
      ) : null}
    </div>
  );
}

const LINK =
  "inline-flex items-center gap-1.5 rounded-sm text-small text-fg-2 hover:text-fg focus-ring max-lg:min-h-(--vs-control-touch)";

function ScoreRow({
  row,
  count,
  series,
  running,
  labels,
  open,
  onOpen,
}: {
  row: RowDef;
  count: number;
  series: (i: number) => 1 | 2 | 3 | 4 | 5;
  running: boolean;
  labels: string[];
  open: boolean;
  onOpen?: () => void;
}) {
  const cells = Array.from({ length: count }, (_, i) => row.cell(i));
  const values = cells.map((c) => c.n).filter((n): n is number => n !== null);
  const best =
    row.better && values.length > 1
      ? row.better === "high"
        ? Math.max(...values)
        : Math.min(...values)
      : null;
  return (
    <tr data-row={row.key} className={cn(open && "bg-hover")}>
      <th scope="row" className="border-b border-line px-4 py-3 text-left align-middle font-normal">
        {onOpen ? (
          <button
            type="button"
            aria-expanded={open}
            onClick={onOpen}
            className={cn(
              "rounded-sm text-left text-body underline-offset-4 hover:underline focus-ring",
              open ? "font-semibold text-fg" : "text-fg-2",
            )}
          >
            {row.label}
          </button>
        ) : (
          <span className="text-body text-fg-2">{row.label}</span>
        )}
      </th>
      {cells.map((c, i) => {
        const isBest = best !== null && c.n === best;
        return (
          <td
            key={i}
            data-best={isBest ? "" : undefined}
            className="border-b border-l border-line px-4 py-3 align-middle"
          >
            {c.n === null && c.text === undefined ? (
              <span className="text-small text-fg-3">No data</span>
            ) : (
              <span className="flex min-w-0 flex-col gap-2">
                <span className="flex flex-wrap items-baseline gap-x-2">
                  <span
                    className={cn(
                      row.big ? "text-heading leading-7 tracking-normal font-normal" : "text-body",
                      isBest ? "font-semibold text-fg" : "text-fg-2",
                    )}
                  >
                    {c.text}
                  </span>
                  {c.change ? <ChangeLine change={c.change} className="text-caption" /> : null}
                  {isBest ? <span className="sr-only">, the best</span> : null}
                </span>
                {c.spark?.some((v) => v > 0) ? (
                  <MiniBars values={c.spark} series={series(i)} running={running} labels={labels} />
                ) : null}
                {c.visual ?? null}
              </span>
            )}
          </td>
        );
      })}
    </tr>
  );
}

function Breakdowns({ projects }: { projects: (WebCompareProject & { slot: number })[] }) {
  return (
    <div className="flex flex-col gap-6">
      {DIMENSIONS.map((d) => (
        <section key={d.dim} aria-label={d.label} className="flex flex-col gap-3">
          <h3 className="text-title text-fg">{d.label}</h3>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {projects.map((p) => {
              const b = p.breakdowns[d.dim];
              return (
                <div key={p.tenant.id} className="flex min-w-0 flex-col gap-2">
                  <p className="flex min-w-0 items-center gap-2 text-small font-medium text-fg">
                    <SeriesSwatch slot={p.slot} />
                    <span className="truncate">{p.tenant.name}</span>
                  </p>
                  <BreakdownList
                    label={`${d.label} of ${p.tenant.name}`}
                    valueLabel="Visitors"
                    state={b.items.length === 0 ? "empty" : "data"}
                    emptyTitle="None in this range"
                    limit={5}
                    items={b.items.map((x) => {
                      const text = d.dim === "country" ? countryName(x.key) : x.key;
                      return { key: x.key, label: text, text, value: x.value };
                    })}
                  />
                  {b.total_keys > b.items.length ? (
                    <p className="text-caption text-fg-3">
                      {countOf(b.total_keys - b.items.length, "more value")} not shown
                    </p>
                  ) : null}
                </div>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
