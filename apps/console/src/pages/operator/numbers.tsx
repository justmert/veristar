// the numbers of one project next to a decision: web, api and on-chain
// figures and the integrity grade, read from the comparison endpoint for one
// project over its default range. the review page and the project page show
// the same cards.

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight } from "lucide-react";
import { Link } from "react-router";
import { apiCompareQuery, webCompareQuery } from "@/api/queries";
import { StatCard } from "@/components/stat-card";
import { ErrorState } from "@/components/states";
import { formatCount, formatDateRange, formatPercent } from "@/lib/format";
import { compareSearch } from "@/lib/global";

const DEFAULT_RANGE = { from: "", to: "" };

export function ProjectNumbers({ tenantId, className }: { tenantId: string; className?: string }) {
  const web = useQuery(webCompareQuery([tenantId], DEFAULT_RANGE));
  const api = useQuery(apiCompareQuery([tenantId], DEFAULT_RANGE));
  const w = web.data?.projects[0];
  const a = api.data?.projects[0];
  const onchain = w?.onchain ?? a?.onchain;
  const range = web.data ?? api.data;
  const webState = web.isPending ? "loading" : web.error ? "error" : "data";
  const apiState = api.isPending ? "loading" : api.error ? "error" : "data";
  const grade = w?.integrity.grade ?? null;

  return (
    <section
      aria-labelledby={`numbers-${tenantId}`}
      className={cn("flex flex-col gap-3", className)}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 id={`numbers-${tenantId}`} className="text-title text-fg">
          Numbers
        </h2>
        <span className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-small text-fg-2">
          {range ? <span>{formatDateRange(range.from, range.to)}, UTC</span> : null}
          <Link
            to={`/operator/compare?${compareSearch([tenantId], DEFAULT_RANGE)}`}
            className="inline-flex items-center gap-1 rounded-sm text-fg underline-offset-4 hover:underline focus-ring"
          >
            Open in Compare
            <ArrowRight aria-hidden className="size-3.5" />
          </Link>
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        <StatCard
          label="Page views"
          definition="Page views of the counted traffic in the range."
          value={w ? w.totals.pageviews : null}
          state={webState}
          data-stat="pageviews"
        />
        <StatCard
          label="Visitors"
          definition="Visitors are unique per UTC day and site. The range total is the sum of the days."
          value={w ? w.totals.visitors : null}
          state={webState}
          data-stat="visitors"
        />
        <StatCard
          label="Integrity grade"
          definition="The grade of the web traffic in the range. C when a quarter of the graded site days are C."
          value={grade}
          missingReason="No day had enough traffic for a grade"
          state={webState}
          data-stat="grade"
        />
        <StatCard
          label="API requests"
          definition="Requests of every service, without health checks and probes."
          value={a ? a.totals.requests : null}
          state={apiState}
          data-stat="requests"
        />
        <StatCard
          label="5xx rate"
          definition="Requests answered with a 5xx status, as a share."
          value={a && a.totals.rate_5xx !== null ? formatPercent(a.totals.rate_5xx) : null}
          missingReason="No requests in this range"
          state={apiState}
          data-stat="rate_5xx"
        />
        <StatCard
          label="On-chain operations"
          definition="Operations of the declared accounts and contracts in the range."
          value={onchain && onchain.totals.ids > 0 ? onchain.totals.operations : null}
          format={formatCount}
          missingReason="No accounts or contracts declared"
          state={webState === "data" || apiState === "data" ? "data" : webState}
          data-stat="onchain"
        />
      </div>
      {web.error && api.error ? (
        <ErrorState
          compact
          title="The numbers did not load"
          error={web.error}
          onRetry={() => {
            web.refetch();
            api.refetch();
          }}
        />
      ) : null}
    </section>
  );
}
