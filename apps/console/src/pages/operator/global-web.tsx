import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { webProjectsQuery } from "@/api/queries";
import { type WebProject, type WebSort, webSort } from "@/api/schemas";
import { DbIpAttribution } from "@/components/dbip-attribution";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { SelectField } from "@/components/form-field";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import {
  countryName,
  firstOrder,
  formatDuration,
  formatShare,
  type ListState,
  readListState,
  writeListState,
} from "@/lib/global";
import {
  AGGREGATE_NOTE,
  CompareBar,
  FiltersPanel,
  Missing,
  Notes,
  Num,
  orMissing,
  QUARANTINE_NOTE,
  RangeControl,
  REJECTED_NOTE,
  SelectCell,
  SortHeader,
  useSelection,
  VISITORS_NOTE,
} from "./global-shared";

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
};

const COLUMNS: { sort: WebSort; label: string }[] = [
  { sort: "pageviews", label: "Page views" },
  { sort: "visitors", label: "Visitors" },
  { sort: "visits", label: "Visits" },
  { sort: "bounce_rate", label: "Bounce rate" },
  { sort: "avg_visit_duration", label: "Avg. visit" },
  { sort: "quarantine_share", label: "Quarantined" },
  { sort: "rejected_share", label: "Rejected" },
];

export function GlobalWebPage() {
  const [params, setParams] = useSearchParams();
  const state = readListState(params, webSort.options, DEFAULT_SORT);
  const set = (next: ListState<WebSort>) =>
    setParams(writeListState(next, DEFAULT_SORT), { replace: true });
  const list = useInfiniteQuery(webProjectsQuery(state));
  const selection = useSelection();
  const first = list.data?.pages[0];
  const resolved = first ? { from: first.from, to: first.to } : null;

  const sortBy = (sort: WebSort) =>
    set({
      ...state,
      sort,
      order: state.sort === sort ? (state.order === "asc" ? "desc" : "asc") : firstOrder(sort),
    });

  return (
    <section className="space-y-5">
      <header>
        <h2 className="text-xl font-semibold tracking-tight">Global web</h2>
        <p className="mt-1 max-w-prose text-sm text-muted-foreground">
          Web numbers of every enrolled project over a range of UTC days, from the daily aggregates.
          Filter, sort any column, and select projects to compare them side by side.
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
              const sort = e.target.value as WebSort;
              set({ ...state, sort, order: firstOrder(sort) });
            }}
          >
            {webSort.options.map((s) => (
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
        <section aria-labelledby="global-web-table" className="rounded-xl border bg-card">
          <div className="flex flex-wrap items-baseline justify-between gap-2 p-4 pb-2 sm:px-5">
            <h3 id="global-web-table" className="font-semibold">
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
              aria-label="Global web table"
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
                      Top countries
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y">
                  {list.data.pages
                    .flatMap((p) => p.items)
                    .map((p) => (
                      <WebRow key={p.tenant.id} p={p} selection={selection} />
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

      <Notes items={[VISITORS_NOTE, QUARANTINE_NOTE, REJECTED_NOTE, AGGREGATE_NOTE]}>
        <DbIpAttribution className="mt-3" />
      </Notes>
    </section>
  );
}

function WebRow({ p, selection }: { p: WebProject; selection: ReturnType<typeof useSelection> }) {
  const t = p.tenant;
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
        <span className="mt-0.5 block text-xs text-muted-foreground">
          {p.sites} {p.sites === 1 ? "site" : "sites"}, {p.sites_verified} verified,{" "}
          {p.sites_with_stellar_toml} with stellar.toml
        </span>
      </th>
      <Num>
        <span data-col="pageviews">{formatCount(p.pageviews)}</span>
      </Num>
      <Num>
        <span data-col="visitors">{formatCount(p.visitors)}</span>
      </Num>
      <Num>{formatCount(p.visits)}</Num>
      <Num>{orMissing(formatShare(p.bounce_rate))}</Num>
      <Num>{orMissing(formatDuration(p.avg_visit_duration))}</Num>
      <Num>
        {p.quarantine_share === null ? (
          <Missing />
        ) : (
          <span title={`${formatCount(p.quarantine_pageviews)} quarantined page views`}>
            {formatShare(p.quarantine_share)}
          </span>
        )}
      </Num>
      <Num>
        {p.rejected_share === null ? (
          <Missing />
        ) : (
          <span
            title={`${formatCount(p.ingest_rejected)} of ${formatCount(p.ingest_requests)} requests`}
          >
            {formatShare(p.rejected_share)}
          </span>
        )}
      </Num>
      <td className="px-3 py-2 pr-4 text-xs sm:pr-5">
        {p.top.country.length === 0 ? (
          <Missing />
        ) : (
          <ul className="space-y-0.5">
            {p.top.country.slice(0, 3).map((c) => (
              <li key={c.key} className="whitespace-nowrap">
                {countryName(c.key)}{" "}
                <span className="tabular text-muted-foreground">{formatCount(c.value)}</span>
              </li>
            ))}
          </ul>
        )}
      </td>
    </tr>
  );
}
