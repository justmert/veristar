import { useInfiniteQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { SearchX } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { apiProjectsQuery } from "@/api/queries";
import { type ApiProject, type ApiSort, apiSort } from "@/api/schemas";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatRate } from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";
import {
  EMPTY_FILTERS,
  firstOrder,
  formatPct,
  type ListState,
  readListState,
  writeListState,
} from "@/lib/global";
import { SPAN_NOTE } from "@/pages/api-traffic";
import {
  AGGREGATE_NOTE,
  COHORT_NOTE,
  CohortLine,
  CompareBar,
  FiltersPanel,
  GROWTH_SORT_TEXT,
  InlineSelect,
  KpiCell,
  MagnitudeCell,
  Missing,
  Notes,
  orMissing,
  RangeControl,
  ROW,
  SelectCell,
  SeriesSwatch,
  SortHeader,
  TableScroll,
  TD,
  TH,
  useSelection,
  WithDetail,
} from "./global-shared";
import { ExportListButton, OnchainCell, OnchainHeader } from "./onchain-parts";

const DEFAULT_SORT: ApiSort = "requests";

const SORT_TEXT: Record<ApiSort, string> = {
  name: "Name",
  status: "Status",
  created_at: "Enrollment date",
  sites: "Sites",
  requests: "Requests",
  requests_4xx: "4xx responses",
  requests_5xx: "5xx responses",
  rate_4xx: "4xx rate",
  rate_5xx: "5xx rate",
  p50: "p50 latency",
  p95: "p95 latency",
  p99: "p99 latency",
  services: "Services",
  routes: "Routes",
  ...GROWTH_SORT_TEXT,
};

// the change and percentile columns of contract 19.2 this view shows; the
// error rate is the 5xx rate and its change is in percentage points
const API_KPIS = [
  { sort: "requests_growth", label: "Requests change", key: "requests", kind: "relative" },
  { sort: "error_rate_change", label: "Error rate change", key: "error_rate", kind: "points" },
] as const;

// a percentile above 10 s is measured against the 10 s bound for its bar
const pctSize = (s: number | null, over: boolean) => (over ? 10 : s);

const COLUMNS: { sort: ApiSort; label: string; size: (p: ApiProject) => number | null }[] = [
  { sort: "requests", label: "Requests", size: (p) => p.requests },
  { sort: "rate_4xx", label: "4xx rate", size: (p) => p.rate_4xx },
  { sort: "rate_5xx", label: "5xx rate", size: (p) => p.rate_5xx },
  { sort: "p50", label: "p50", size: (p) => pctSize(p.p50_seconds, p.p50_over_10s) },
  { sort: "p95", label: "p95", size: (p) => pctSize(p.p95_seconds, p.p95_over_10s) },
  { sort: "p99", label: "p99", size: (p) => pctSize(p.p99_seconds, p.p99_over_10s) },
  { sort: "services", label: "Services", size: (p) => p.services },
  { sort: "routes", label: "Routes", size: (p) => p.routes },
];

export const SOURCE_TEXT = {
  metric: "Metrics",
  span: "From traces",
  mixed: "Metrics and traces",
} as const;

export const API_NOTES = [
  "Requests, error rates and percentiles come from the daily rollup of each project's HTTP server duration histograms. Percentiles are computed from summed buckets, never averaged; one above 10 s, the last bucket bound, is shown as >10 s.",
  `For each service the numbers come from its metrics when it sent any in the range, otherwise from its traces. ${SPAN_NOTE}`,
  AGGREGATE_NOTE,
];

export function GlobalApiPage() {
  const [params, setParams] = useSearchParams();
  const read = readListState(params, apiSort.options, DEFAULT_SORT);
  // the api list has no integrity grade, so the filter is never sent
  const state = { ...read, filters: { ...read.filters, integrity: "" as const } };
  const set = (next: ListState<ApiSort>) =>
    setParams(writeListState(next, DEFAULT_SORT), { replace: true });
  const list = useInfiniteQuery(apiProjectsQuery(state));
  const selection = useSelection();
  const first = list.data?.pages[0];
  const resolved = first ? { from: first.from, to: first.to } : null;
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const sized = COLUMNS.find((c) => c.sort === state.sort);
  const max = sized ? Math.max(0, ...items.map((p) => sized.size(p) ?? 0)) : 0;

  const sortBy = (sort: ApiSort) =>
    set({
      ...state,
      sort,
      order: state.sort === sort ? (state.order === "asc" ? "desc" : "asc") : firstOrder(sort),
    });

  return (
    <section aria-labelledby="global-api-h" className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="global-api-h" className="text-section text-fg">
          Global API
        </h2>
        <p className="max-w-prose text-small text-fg-2">
          API traffic of every enrolled project over a range of UTC days. Sort by requests to find
          the lowest and highest usage projects, and select projects to compare them side by side.
          Requests, error rates and latency leave out infrastructure requests: CORS preflights,
          health and probe routes and known health checkers. Each request count names those left out
          below it.
        </p>
      </div>

      <RangeControl resolved={resolved} onChange={(range) => set({ ...state, range })} />

      <div className="flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-raised">
        <div className="flex flex-col gap-2.5 border-b border-line px-4 py-3">
          <FiltersPanel
            filters={state.filters}
            onChange={(filters) => set({ ...state, filters })}
          />
          <div className="grid grid-cols-2 gap-x-3 gap-y-2 sm:flex sm:flex-wrap sm:items-center sm:gap-x-4">
            <InlineSelect
              label="Sort by"
              value={state.sort}
              onChange={(v) => {
                const sort = v as ApiSort;
                set({ ...state, sort, order: firstOrder(sort) });
              }}
            >
              {apiSort.options.map((s) => (
                <option key={s} value={s}>
                  {SORT_TEXT[s]}
                </option>
              ))}
            </InlineSelect>
            <InlineSelect
              label="Order"
              value={state.order}
              onChange={(v) => set({ ...state, order: v as "asc" | "desc" })}
            >
              <option value="desc">Largest first</option>
              <option value="asc">Smallest first</option>
            </InlineSelect>
          </div>
        </div>

        <CompareBar selection={selection} range={resolved} />

        {list.isPending ? <Loading label="Loading projects" className="px-4" rows={6} /> : null}
        {list.error ? (
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} className="m-4" />
        ) : null}
        {list.data ? (
          first?.total === 0 ? (
            <StateBlock
              kind="empty"
              icon={<SearchX aria-hidden />}
              title="No project matches these filters"
              className="m-4"
              action={
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => set({ ...state, filters: EMPTY_FILTERS })}
                >
                  Clear filters
                </Button>
              }
            />
          ) : (
            <TableScroll label="Global API table" dim={list.isPlaceholderData}>
              <table className="w-full min-w-[76rem] border-separate border-spacing-0 text-left text-small tabular">
                <thead>
                  <tr>
                    <th scope="col" className={`${TH} w-10 pl-4`}>
                      <span className="sr-only">Select</span>
                    </th>
                    <SortHeader
                      label="Project"
                      sort="name"
                      current={state.sort}
                      order={state.order}
                      onSort={sortBy}
                    />
                    {COLUMNS.map((c) => (
                      <SortHeader
                        key={c.sort}
                        label={c.label}
                        sort={c.sort}
                        current={state.sort}
                        order={state.order}
                        numeric
                        onSort={sortBy}
                      />
                    ))}
                    {API_KPIS.map((c) => (
                      <SortHeader
                        key={c.sort}
                        label={c.label}
                        sort={c.sort}
                        current={state.sort}
                        order={state.order}
                        numeric
                        onSort={sortBy}
                      />
                    ))}
                    <OnchainHeader />
                    <th scope="col" className={`${TH} pr-4`}>
                      Data source
                    </th>
                  </tr>
                </thead>
                <tbody className="[&>tr:last-child>*]:border-0">
                  {items.map((p) => (
                    <ApiRow
                      key={p.tenant.id}
                      p={p}
                      selection={selection}
                      sort={state.sort}
                      max={max}
                    />
                  ))}
                </tbody>
              </table>
            </TableScroll>
          )
        ) : null}

        {list.data && first?.total !== 0 ? (
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-2.5">
            <p className="text-caption text-fg-3" aria-live="polite">
              {formatCount(first?.total ?? 0)} {first?.total === 1 ? "project" : "projects"} match
            </p>
            <ExportListButton view="api" state={state} resolved={resolved} rows={items.length} />
            {list.hasNextPage ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => list.fetchNextPage()}
                pending={list.isFetchingNextPage}
              >
                {list.isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>

      <Notes items={[...API_NOTES, COHORT_NOTE]} />
    </section>
  );
}

export function SourceBadge({
  source,
  spanServices,
  services,
}: {
  source: ApiProject["source"];
  spanServices?: number;
  services?: number;
}) {
  if (source === null) return <Missing />;
  const traces = source !== "metric";
  return (
    <span
      title={traces ? SPAN_NOTE : undefined}
      className={cn(
        "inline-flex h-6 items-center gap-1.5 rounded-full border px-2 text-caption font-medium whitespace-nowrap text-fg",
        traces ? "border-warning-line" : "border-line-strong",
      )}
    >
      <span
        aria-hidden
        className={cn("size-1.5 rounded-full", traces ? "bg-warning" : "bg-fg-3")}
      />
      {SOURCE_TEXT[source]}
      {source === "mixed" && spanServices !== undefined && services !== undefined
        ? ` (${spanServices} of ${services} from traces)`
        : ""}
    </span>
  );
}

function ApiRow({
  p,
  selection,
  sort,
  max,
}: {
  p: ApiProject;
  selection: ReturnType<typeof useSelection>;
  sort: ApiSort;
  max: number;
}) {
  const t = p.tenant;
  const slot = selection.slot(t.id);
  const rate = (r: number | null, n: number) => {
    const v = formatRate(r);
    return v === null ? (
      <Missing />
    ) : (
      <WithDetail detail={`${formatCount(n)} requests`}>{v}</WithDetail>
    );
  };
  const cell = (s: ApiSort, v: number | null, children: React.ReactNode) => (
    <MagnitudeCell on={sort === s} value={v} max={max}>
      {children}
    </MagnitudeCell>
  );
  return (
    <tr data-tenant={t.id} data-state={slot >= 0 ? "selected" : undefined} className={ROW}>
      <SelectCell id={t.id} name={t.name} selection={selection} />
      <th scope="row" className={`${TD} min-w-48 max-w-64 text-left font-normal`}>
        <span className="flex min-w-0 items-center gap-2">
          <SeriesSwatch slot={slot} />
          <Link
            to={`/operator/compare?tenants=${t.id}`}
            className="truncate rounded-sm text-body font-medium text-fg underline-offset-4 hover:underline focus-ring"
          >
            {t.name}
          </Link>
          {t.status !== "active" ? <StatusPill status={t.status} /> : null}
        </span>
        {p.lossy ? (
          <span className="mt-0.5 block text-caption text-fg-3">
            Some latency buckets are approximate
          </span>
        ) : null}
        <CohortLine cohort={p.cohort} />
      </th>
      {cell(
        "requests",
        p.requests,
        <WithDetail detail={`+${formatCount(p.infrastructure_requests)} infrastructure`}>
          <span data-col="requests">{formatCount(p.requests)}</span>
        </WithDetail>,
      )}
      {cell("rate_4xx", p.rate_4xx, rate(p.rate_4xx, p.requests_4xx))}
      {cell("rate_5xx", p.rate_5xx, rate(p.rate_5xx, p.requests_5xx))}
      {cell(
        "p50",
        pctSize(p.p50_seconds, p.p50_over_10s),
        orMissing(formatPct(p.p50_seconds, p.p50_over_10s)),
      )}
      {cell(
        "p95",
        pctSize(p.p95_seconds, p.p95_over_10s),
        orMissing(formatPct(p.p95_seconds, p.p95_over_10s)),
      )}
      {cell(
        "p99",
        pctSize(p.p99_seconds, p.p99_over_10s),
        orMissing(formatPct(p.p99_seconds, p.p99_over_10s)),
      )}
      {cell("services", p.services, formatCount(p.services))}
      {cell("routes", p.routes, formatCount(p.routes))}
      {API_KPIS.map((c) => (
        <KpiCell
          key={c.sort}
          k={p.kpis[c.key]}
          kind={c.kind}
          on={sort === c.sort}
          label={c.label}
        />
      ))}
      <OnchainCell s={p.onchain} />
      <td className={`${TD} pr-4`}>
        <SourceBadge source={p.source} spanServices={p.span_services} services={p.services} />
      </td>
    </tr>
  );
}
