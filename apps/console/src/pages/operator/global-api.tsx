import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { apiProjectsQuery } from "@/api/queries";
import { type ApiProject, type ApiSort, apiSort } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { SelectField } from "@/components/form-field";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatRate } from "@/lib/api-metrics";
import { formatCount } from "@/lib/format";
import { firstOrder, formatPct, type ListState, readListState, writeListState } from "@/lib/global";
import { SPAN_NOTE } from "@/pages/api-traffic";
import {
  AGGREGATE_NOTE,
  CompareBar,
  FiltersPanel,
  Missing,
  Notes,
  Num,
  orMissing,
  RangeControl,
  SelectCell,
  SortHeader,
  useSelection,
} from "./global-shared";

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
};

const COLUMNS: { sort: ApiSort; label: string }[] = [
  { sort: "requests", label: "Requests" },
  { sort: "rate_4xx", label: "4xx rate" },
  { sort: "rate_5xx", label: "5xx rate" },
  { sort: "p50", label: "p50" },
  { sort: "p95", label: "p95" },
  { sort: "p99", label: "p99" },
  { sort: "services", label: "Services" },
  { sort: "routes", label: "Routes" },
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
  const state = readListState(params, apiSort.options, DEFAULT_SORT);
  const set = (next: ListState<ApiSort>) =>
    setParams(writeListState(next, DEFAULT_SORT), { replace: true });
  const list = useInfiniteQuery(apiProjectsQuery(state));
  const selection = useSelection();
  const first = list.data?.pages[0];
  const resolved = first ? { from: first.from, to: first.to } : null;

  const sortBy = (sort: ApiSort) =>
    set({
      ...state,
      sort,
      order: state.sort === sort ? (state.order === "asc" ? "desc" : "asc") : firstOrder(sort),
    });

  return (
    <section className="space-y-5">
      <header>
        <h2 className="text-xl font-semibold tracking-tight">Global API</h2>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          API traffic of every enrolled project over a range of UTC days. Sort by requests to find
          the lowest and highest usage projects, and select projects to compare them side by side.
        </p>
      </header>

      <div className="space-y-5 rounded-xl border bg-card p-4 sm:p-5">
        <RangeControl resolved={resolved} onChange={(range) => set({ ...state, range })} />
        <FiltersPanel filters={state.filters} onChange={(filters) => set({ ...state, filters })} />
        <div className="flex flex-wrap gap-3">
          <SelectField
            label="Sort by"
            value={state.sort}
            onChange={(e) => {
              const sort = e.target.value as ApiSort;
              set({ ...state, sort, order: firstOrder(sort) });
            }}
          >
            {apiSort.options.map((s) => (
              <option key={s} value={s}>
                {SORT_TEXT[s]}
              </option>
            ))}
          </SelectField>
          <SelectField
            label="Order"
            value={state.order}
            onChange={(e) => set({ ...state, order: e.target.value as "asc" | "desc" })}
          >
            <option value="desc">Largest first</option>
            <option value="asc">Smallest first</option>
          </SelectField>
        </div>
      </div>

      <CompareBar selection={selection} range={resolved} />

      {list.isPending ? <Loading label="Loading projects" /> : null}
      {list.error ? <ErrorPanel error={list.error} onRetry={() => list.refetch()} /> : null}
      {list.data ? (
        <section aria-labelledby="global-api-table" className="rounded-xl border bg-card">
          <div className="flex flex-wrap items-baseline justify-between gap-2 p-4 pb-2 sm:px-5">
            <h3 id="global-api-table" className="font-semibold">
              Projects
            </h3>
            <p className="text-xs text-muted-foreground" aria-live="polite">
              {formatCount(first?.total ?? 0)} {first?.total === 1 ? "project" : "projects"} match
            </p>
          </div>
          {first?.total === 0 ? (
            <p className="px-4 pb-5 text-muted-foreground sm:px-5">
              No project matches these filters.
            </p>
          ) : (
            <section
              aria-label="Global API table"
              // biome-ignore lint/a11y/noNoninteractiveTabindex: a scrollable region needs focus (wcag 2.1.1)
              tabIndex={0}
              className={`relative overflow-x-auto transition-opacity ${list.isPlaceholderData ? "opacity-60" : ""}`}
            >
              <table className="w-full min-w-[64rem] text-left text-sm">
                <thead className="border-y bg-muted/50 text-xs text-muted-foreground">
                  <tr>
                    <th scope="col" className="py-1 pr-1 pl-4 sm:pl-5">
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
                    <th scope="col" className="px-3 py-1 pr-4 font-medium sm:pr-5">
                      Data source
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {list.data.pages
                    .flatMap((p) => p.items)
                    .map((p) => (
                      <ApiRow key={p.tenant.id} p={p} selection={selection} />
                    ))}
                </tbody>
              </table>
            </section>
          )}
          {list.hasNextPage ? (
            <div className="border-t p-3 sm:px-5">
              <Button
                variant="outline"
                size="sm"
                onClick={() => list.fetchNextPage()}
                disabled={list.isFetchingNextPage}
              >
                {list.isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            </div>
          ) : null}
        </section>
      ) : null}

      <Notes items={API_NOTES} />
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
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium whitespace-nowrap ${traces ? "border-warn/50 bg-warn/10" : "bg-muted"}`}
    >
      {SOURCE_TEXT[source]}
      {source === "mixed" && spanServices !== undefined && services !== undefined
        ? ` (${spanServices} of ${services} from traces)`
        : ""}
    </span>
  );
}

function ApiRow({ p, selection }: { p: ApiProject; selection: ReturnType<typeof useSelection> }) {
  const t = p.tenant;
  const rate = (r: number | null, n: number) => {
    const v = formatRate(r);
    return v === null ? <Missing /> : <span title={`${formatCount(n)} requests`}>{v}</span>;
  };
  return (
    <tr data-tenant={t.id}>
      <SelectCell id={t.id} name={t.name} selection={selection} />
      <th scope="row" className="max-w-[16rem] px-2 py-2 text-left font-normal">
        <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <Link
            to={`/operator/compare?tenants=${t.id}`}
            className="font-medium underline-offset-2 hover:underline"
          >
            {t.name}
          </Link>
          <StatusPill status={t.status} />
        </span>
        {p.lossy ? (
          <span className="mt-0.5 block text-xs text-muted-foreground">
            Some latency buckets are approximate
          </span>
        ) : null}
      </th>
      <Num>
        <span data-col="requests">{formatCount(p.requests)}</span>
      </Num>
      <Num>{rate(p.rate_4xx, p.requests_4xx)}</Num>
      <Num>{rate(p.rate_5xx, p.requests_5xx)}</Num>
      <Num>{orMissing(formatPct(p.p50_seconds, p.p50_over_10s))}</Num>
      <Num>{orMissing(formatPct(p.p95_seconds, p.p95_over_10s))}</Num>
      <Num>{orMissing(formatPct(p.p99_seconds, p.p99_over_10s))}</Num>
      <Num>{formatCount(p.services)}</Num>
      <Num>{formatCount(p.routes)}</Num>
      <td className="px-3 py-2 pr-4 sm:pr-5">
        <SourceBadge source={p.source} spanServices={p.span_services} services={p.services} />
      </td>
    </tr>
  );
}
