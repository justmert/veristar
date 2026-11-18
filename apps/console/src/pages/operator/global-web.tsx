import { useInfiniteQuery } from "@tanstack/react-query";
import { SearchX } from "lucide-react";
import { Link, useSearchParams } from "react-router";
import { webProjectsQuery } from "@/api/queries";
import { type WebProject, type WebSort, webSort } from "@/api/schemas";
import { CoverageCell } from "@/components/coverage";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { IntegrityCell } from "@/components/integrity";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { countryName, formatCount } from "@/lib/format";
import {
  activeFilterCount,
  type DayRange,
  dayPairError,
  EMPTY_FILTERS,
  firstOrder,
  formatDuration,
  formatShare,
  type ListState,
  readListState,
  writeListState,
} from "@/lib/global";
import {
  AGGREGATE_NOTE,
  COHORT_NOTE,
  COVERAGE_NOTE,
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
  QUARANTINE_NOTE,
  RangeControl,
  REJECTED_NOTE,
  ROW,
  SelectCell,
  SeriesSwatch,
  SortHeader,
  TableScroll,
  TD,
  TH,
  useSelection,
  VISITORS_NOTE,
  WithDetail,
} from "./global-shared";
import { ExportListButton, OnchainCell, OnchainHeader } from "./onchain-parts";

const DEFAULT_SORT: WebSort = "pageviews";

// every sort of contract 8.3, for the select that also works on a phone
const SORT_TEXT: Record<WebSort, string> = {
  name: "Name",
  status: "Status",
  created_at: "Enrollment date",
  sites: "Sites",
  sites_verified: "Verified sites",
  sites_with_stellar_toml: "Sites with stellar.toml",
  pageviews: "Page views",
  visitors: "Visitors",
  visits: "Visits",
  bounces: "Bounces",
  totaltime: "Total visit time",
  bounce_rate: "Bounce rate",
  avg_visit_duration: "Average visit duration",
  quarantine_pageviews: "Quarantined page views",
  quarantine_visitors: "Quarantined visitors",
  quarantine_share: "Quarantined share",
  ingest_requests: "Gateway requests",
  ingest_rejected: "Rejected requests",
  rejected_share: "Rejected share",
  integrity: "Integrity grade",
  ...GROWTH_SORT_TEXT,
  unmeasured_share: "Not measured (privacy signals)",
};

// the columns; `size` is what the magnitude bar measures when the table
// is sorted by the column
// the change and percentile columns of contract 19.2 this view shows
const WEB_KPIS = [
  { sort: "visitors_growth", label: "Visitors change", key: "visitors" },
  { sort: "pageviews_growth", label: "Page views change", key: "pageviews" },
  { sort: "onchain_operations_growth", label: "On-chain change", key: "onchain_operations" },
] as const;

const COLUMNS: { sort: WebSort; label: string; size: (p: WebProject) => number | null }[] = [
  { sort: "pageviews", label: "Page views", size: (p) => p.pageviews },
  { sort: "visitors", label: "Visitors", size: (p) => p.visitors },
  { sort: "visits", label: "Visits", size: (p) => p.visits },
  { sort: "bounce_rate", label: "Bounce rate", size: (p) => p.bounce_rate },
  { sort: "avg_visit_duration", label: "Avg. visit", size: (p) => p.avg_visit_duration },
  { sort: "quarantine_share", label: "Quarantined", size: (p) => p.quarantine_share },
  { sort: "rejected_share", label: "Rejected", size: (p) => p.rejected_share },
];

export function GlobalWebPage() {
  const [params, setParams] = useSearchParams();
  const state = readListState(params, webSort.options, DEFAULT_SORT);
  const set = (next: ListState<WebSort>) =>
    setParams(writeListState(next, DEFAULT_SORT), { replace: true });
  // a day pair that ends before it starts is refused by the server; the
  // filters say so and nothing is asked
  const badDays = dayPairError(state.filters) !== null;
  const list = useInfiniteQuery({ ...webProjectsQuery(state), enabled: !badDays });
  const selection = useSelection();
  const filtered = activeFilterCount(state.filters) > 0;
  const first = list.data?.pages[0];
  const resolved = first ? { from: first.from, to: first.to } : null;
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const sized = COLUMNS.find((c) => c.sort === state.sort);
  const max = sized ? Math.max(0, ...items.map((p) => sized.size(p) ?? 0)) : 0;

  const sortBy = (sort: WebSort) =>
    set({
      ...state,
      sort,
      order: state.sort === sort ? (state.order === "asc" ? "desc" : "asc") : firstOrder(sort),
    });

  return (
    <section aria-labelledby="global-web-h" className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="global-web-h" className="text-section text-fg">
          Global web
        </h2>
        <p className="max-w-prose text-small text-fg-2">
          Web numbers of every enrolled project over a range of UTC days, from the daily aggregates.
          Filter, sort any column, and select projects to compare them side by side.
        </p>
      </div>

      <RangeControl resolved={resolved} onChange={(range) => set({ ...state, range })} />

      <div className="flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-raised">
        <div className="flex flex-col gap-2 border-b border-line px-4 py-3">
          <FiltersPanel
            filters={state.filters}
            onChange={(filters) => set({ ...state, filters })}
            integrity
          />
          <div className="grid grid-cols-2 gap-x-3 gap-y-2 sm:flex sm:flex-wrap sm:items-center sm:gap-x-4">
            <InlineSelect
              label="Sort by"
              value={state.sort}
              onChange={(v) => {
                const sort = v as WebSort;
                set({ ...state, sort, order: firstOrder(sort) });
              }}
            >
              {webSort.options.map((s) => (
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

        {list.isPending && !badDays ? (
          <Loading label="Loading projects" className="px-4" rows={6} />
        ) : null}
        {list.error ? (
          <ErrorPanel error={list.error} onRetry={() => list.refetch()} className="m-4" />
        ) : null}
        {list.data ? (
          first?.total === 0 ? (
            <StateBlock
              kind="empty"
              icon={<SearchX aria-hidden />}
              title={filtered ? "No project matches these filters" : "No project enrolled yet"}
              className="m-4"
              action={
                filtered ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => set({ ...state, filters: EMPTY_FILTERS })}
                  >
                    Clear filters
                  </Button>
                ) : null
              }
            />
          ) : (
            <TableScroll label="Global web table" dim={list.isPlaceholderData}>
              <table className="w-full min-w-[80rem] border-separate border-spacing-0 text-left text-small tabular">
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
                    <SortHeader
                      label="Integrity"
                      sort="integrity"
                      current={state.sort}
                      order={state.order}
                      onSort={sortBy}
                    />
                    <SortHeader
                      label="Not measured"
                      sort="unmeasured_share"
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
                    {WEB_KPIS.map((c) => (
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
                      Top countries
                    </th>
                  </tr>
                </thead>
                <tbody className="[&>tr:last-child>*]:border-0">
                  {items.map((p) => (
                    <WebRow
                      key={p.tenant.id}
                      p={p}
                      selection={selection}
                      sort={state.sort}
                      max={max}
                      range={resolved ?? state.range}
                    />
                  ))}
                </tbody>
              </table>
            </TableScroll>
          )
        ) : null}

        {list.data && first?.total !== 0 ? (
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-2">
            <p className="text-caption text-fg-3" aria-live="polite">
              {formatCount(first?.total ?? 0)} {first?.total === 1 ? "project" : "projects"} match
            </p>
            <ExportListButton view="web" state={state} resolved={resolved} rows={items.length} />
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

      <Notes
        items={[
          VISITORS_NOTE,
          QUARANTINE_NOTE,
          REJECTED_NOTE,
          COVERAGE_NOTE,
          COHORT_NOTE,
          AGGREGATE_NOTE,
        ]}
      >
        <DbIpAttribution className="mt-1" />
      </Notes>
    </section>
  );
}

function WebRow({
  p,
  selection,
  sort,
  max,
  range,
}: {
  p: WebProject;
  selection: ReturnType<typeof useSelection>;
  sort: WebSort;
  max: number;
  range: DayRange;
}) {
  const t = p.tenant;
  const slot = selection.slot(t.id);
  const cell = (s: WebSort, v: number | null, children: React.ReactNode) => (
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
        <span className="mt-1 block text-caption text-fg-3">
          {p.sites} {p.sites === 1 ? "site" : "sites"}, {p.sites_verified} verified,{" "}
          {p.sites_with_stellar_toml} with stellar.toml
        </span>
        <CohortLine cohort={p.cohort} />
      </th>
      <td className={TD}>
        <IntegrityCell tenant={t} integrity={p.integrity} range={range} />
      </td>
      <td className={TD}>
        <CoverageCell coverage={p.coverage} />
      </td>
      {cell("pageviews", p.pageviews, <span data-col="pageviews">{formatCount(p.pageviews)}</span>)}
      {cell("visitors", p.visitors, <span data-col="visitors">{formatCount(p.visitors)}</span>)}
      {cell("visits", p.visits, formatCount(p.visits))}
      {cell("bounce_rate", p.bounce_rate, orMissing(formatShare(p.bounce_rate)))}
      {cell(
        "avg_visit_duration",
        p.avg_visit_duration,
        orMissing(formatDuration(p.avg_visit_duration)),
      )}
      {cell(
        "quarantine_share",
        p.quarantine_share,
        p.quarantine_share === null ? (
          <Missing />
        ) : (
          <WithDetail detail={`${formatCount(p.quarantine_pageviews)} page views`}>
            {formatShare(p.quarantine_share)}
          </WithDetail>
        ),
      )}
      {cell(
        "rejected_share",
        p.rejected_share,
        p.rejected_share === null ? (
          <Missing />
        ) : (
          <WithDetail
            detail={`${formatCount(p.ingest_rejected)} of ${formatCount(p.ingest_requests)} requests`}
          >
            {formatShare(p.rejected_share)}
          </WithDetail>
        ),
      )}
      {WEB_KPIS.map((c) => (
        <KpiCell
          key={c.sort}
          k={p.kpis[c.key]}
          kind="relative"
          on={sort === c.sort}
          label={c.label}
        />
      ))}
      <OnchainCell s={p.onchain} />
      <td className={`${TD} pr-4 text-caption`}>
        {p.top.country.length === 0 ? (
          <Missing />
        ) : (
          <ul className="flex flex-col gap-1">
            {p.top.country.slice(0, 3).map((c) => (
              <li key={c.key} className="whitespace-nowrap text-fg-2">
                {countryName(c.key)}{" "}
                <span className="font-mono text-fg-3 tabular">{formatCount(c.value)}</span>
              </li>
            ))}
          </ul>
        )}
      </td>
    </tr>
  );
}
