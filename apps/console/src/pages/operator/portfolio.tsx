// the portfolio line above the project list: how many projects are in each
// status, how many have web or api data in the range, and how many wait for
// a review. each count is a filter of the list, one click away. the counts
// are the totals the list endpoint answers for the same filters, so a count
// and the rows it filters to always agree.

import { useInfiniteQuery, useQueries } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { globalApi } from "@/api/endpoints";
import { keys, operatorTenantsQuery } from "@/api/queries";
import { type TenantStatus, tenantStatus, type WebSort } from "@/api/schemas";
import { statusText } from "@/components/status";
import { formatCount } from "@/lib/format";
import { type DayRange, EMPTY_FILTERS, type ListState, type ProjectFilters } from "@/lib/global";
import { REVIEW_QUEUE } from "./review";

// a segment of the portfolio: what it counts and the filter it applies
export type Segment =
  | { kind: "all" }
  | { kind: "status"; status: TenantStatus }
  | { kind: "web" }
  | { kind: "api" }
  | { kind: "none" }
  | { kind: "review" };

function filtersOf(s: Segment): ProjectFilters {
  switch (s.kind) {
    case "status":
      return { ...EMPTY_FILTERS, statuses: [s.status] };
    case "web":
      return { ...EMPTY_FILTERS, has_web_data: "true" };
    case "api":
      return { ...EMPTY_FILTERS, has_api_data: "true" };
    case "none":
      return { ...EMPTY_FILTERS, has_web_data: "false", has_api_data: "false" };
    default:
      return EMPTY_FILTERS;
  }
}

const COUNTED: Segment[] = [
  { kind: "all" },
  ...tenantStatus.options.map((status) => ({ kind: "status" as const, status })),
  { kind: "web" },
  { kind: "api" },
  { kind: "none" },
];

export function segmentKey(s: Segment): string {
  return s.kind === "status" ? `status:${s.status}` : s.kind;
}

// the total of the web list for one segment; the list answers its total
// with the first page
function countState(s: Segment, range: DayRange): ListState<WebSort> {
  return { range, filters: filtersOf(s), sort: "name", order: "asc" };
}

export function usePortfolio(range: DayRange) {
  const counts = useQueries({
    queries: COUNTED.map((s) => {
      const state = countState(s, range);
      return {
        queryKey: keys.opGlobal("portfolio", state),
        queryFn: ({ signal }: { signal: AbortSignal }) =>
          globalApi.webProjects(state, null, signal).then((p) => p.total),
        staleTime: 60_000,
      };
    }),
  });
  const review = useInfiniteQuery(operatorTenantsQuery(REVIEW_QUEUE));
  const byKey = new Map<string, number | null>(
    COUNTED.map((s, i) => [segmentKey(s), counts[i]?.data ?? null]),
  );
  const reviewItems = review.data?.pages.flatMap((p) => p.items).length;
  byKey.set("review", reviewItems === undefined ? null : reviewItems);
  return {
    count: (s: Segment) => byKey.get(segmentKey(s)) ?? null,
    more: (s: Segment) => s.kind === "review" && review.hasNextPage,
    error: counts.every((c) => c.error) ? counts[0]?.error : null,
  };
}

const LABEL: Record<Exclude<Segment["kind"], "status">, string> = {
  all: "All",
  web: "Web data",
  api: "API data",
  none: "No data",
  review: "Needs review",
};

function labelOf(s: Segment): string {
  return s.kind === "status" ? statusText(s.status) : LABEL[s.kind];
}

// the counts in three groups: status, data, attention. a status nobody
// holds is left out; the data and attention groups always show, a zero
// there is a fact
export function PortfolioBar({
  portfolio,
  current,
  onPick,
}: {
  portfolio: ReturnType<typeof usePortfolio>;
  // the segment the list is filtered to now, if it matches one exactly
  current: string | null;
  onPick: (s: Segment) => void;
}) {
  if (portfolio.error) return null;
  const groups: { label: string; segments: Segment[] }[] = [
    {
      label: "Status",
      segments: COUNTED.filter(
        (s) => s.kind === "all" || (s.kind === "status" && portfolio.count(s) !== 0),
      ),
    },
    { label: "Data", segments: [{ kind: "web" }, { kind: "api" }, { kind: "none" }] },
    { label: "Attention", segments: [{ kind: "review" }] },
  ];
  return (
    <nav
      aria-label="Portfolio"
      className="flex min-w-0 flex-wrap items-stretch gap-x-6 gap-y-3"
      data-portfolio=""
    >
      {groups.map((g) => (
        <ul
          key={g.label}
          aria-label={g.label}
          className="flex min-w-0 flex-wrap items-stretch gap-1 border-line not-first:border-l not-first:pl-6"
        >
          {g.segments.map((s) => {
            const n = portfolio.count(s);
            const key = segmentKey(s);
            const on = current === key;
            const attention = s.kind === "review" && (n ?? 0) > 0;
            return (
              <li key={key}>
                <button
                  type="button"
                  aria-pressed={on}
                  data-segment={key}
                  onClick={() => onPick(on ? { kind: "all" } : s)}
                  className={cn(
                    "flex min-w-16 flex-col items-start rounded-md px-3 py-2 text-left transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-(--vs-control-touch)",
                    on ? "bg-selected" : "hover:bg-hover",
                  )}
                >
                  <span
                    className={cn("text-title tabular", attention ? "text-warning" : "text-fg")}
                    data-count={n ?? ""}
                  >
                    {n === null ? (
                      <span aria-hidden className="text-fg-3">
                        ·
                      </span>
                    ) : (
                      `${formatCount(n)}${portfolio.more(s) ? "+" : ""}`
                    )}
                  </span>
                  <span className="text-caption whitespace-nowrap text-fg-2">{labelOf(s)}</span>
                </button>
              </li>
            );
          })}
        </ul>
      ))}
    </nav>
  );
}
