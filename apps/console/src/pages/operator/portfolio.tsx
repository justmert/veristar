// the portfolio line above the project list: how many projects are in each
// status and how many have web or api data in the range. each count is a filter of the list, one click away. the counts
// are the totals the list endpoint answers for the same filters, so a count
// and the rows it filters to always agree.

import { useQueries } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { globalApi } from "@/api/endpoints";
import { keys } from "@/api/queries";
import { type TenantStatus, tenantStatus, type WebSort } from "@/api/schemas";
import { statusEntry, statusText, type Tone } from "@/components/status";
import { formatCount } from "@/lib/format";
import { type DayRange, EMPTY_FILTERS, type ListState, type ProjectFilters } from "@/lib/global";

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
  const byKey = new Map<string, number | null>(
    COUNTED.map((s, i) => [segmentKey(s), counts[i]?.data ?? null]),
  );
  return {
    count: (s: Segment) => byKey.get(segmentKey(s)) ?? null,
    more: (_s: Segment) => false,
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

const DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  info: "bg-info",
  neutral: "bg-fg-3",
};

// the dot of a status, the series colour of a data group
function dotOf(s: Segment): string | null {
  if (s.kind === "status")
    return DOT[statusEntry({ kind: "project", status: s.status, operator: true }).tone];
  if (s.kind === "web") return "bg-series-1";
  if (s.kind === "api") return "bg-series-3";
  return null;
}

// one status bar: a stacked line of the statuses, then the counts, which
// are the filter buttons. the data coverage is a second group. a zero shows
// quiet; a count still loading shows a quiet mark.
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
  const statuses = COUNTED.filter((s) => s.kind === "status");
  const total = statuses.reduce((a, s) => a + (portfolio.count(s) ?? 0), 0);
  const groups: { label: string; segments: Segment[] }[] = [
    { label: "Status", segments: [{ kind: "all" }, ...statuses] },
    { label: "Data", segments: [{ kind: "web" }, { kind: "api" }, { kind: "none" }] },
  ];
  return (
    <nav aria-label="Portfolio" className="flex min-w-0 flex-col gap-3" data-portfolio="">
      {total > 0 ? (
        <span aria-hidden className="flex h-2 w-full gap-[3px] overflow-hidden rounded-full">
          {statuses.map((s) => {
            const n = portfolio.count(s) ?? 0;
            if (n === 0) return null;
            return (
              <i
                key={segmentKey(s)}
                className={cn("block h-full rounded-full opacity-80", dotOf(s))}
                style={{ flexGrow: n, flexBasis: 0 }}
              />
            );
          })}
        </span>
      ) : null}
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
        {groups.map((g) => (
          <ul
            key={g.label}
            aria-label={g.label}
            className="flex min-w-0 flex-wrap items-center gap-1 border-line not-first:border-l not-first:pl-4"
          >
            {g.segments.map((s) => {
              const n = portfolio.count(s);
              const key = segmentKey(s);
              const on = current === key;
              const zero = n === 0;
              const dot = dotOf(s);
              return (
                <li key={key}>
                  <button
                    type="button"
                    aria-pressed={on}
                    data-segment={key}
                    onClick={() => onPick(on ? { kind: "all" } : s)}
                    className={cn(
                      "flex h-8 items-center gap-2 rounded-lg px-3 text-body whitespace-nowrap transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-(--vs-control-touch)",
                      on ? "bg-selected text-fg" : "hover:bg-hover",
                      zero ? "text-fg-3" : "text-fg",
                    )}
                  >
                    {dot ? (
                      <span
                        aria-hidden
                        className={cn("size-[7px] rounded-full", dot, zero && "opacity-40")}
                      />
                    ) : null}
                    <span className="font-semibold tabular" data-count={n ?? ""}>
                      {n === null ? (
                        <span aria-hidden className="text-fg-3">
                          ·
                        </span>
                      ) : (
                        `${formatCount(n)}${portfolio.more(s) ? "+" : ""}`
                      )}
                    </span>
                    <span className={zero ? "text-fg-3" : "text-fg-2"}>{labelOf(s)}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        ))}
      </div>
    </nav>
  );
}
