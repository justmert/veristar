// the numbers of one project next to a decision: web, api and on-chain
// figures and the integrity grade, read from the comparison endpoint for one
// project over its default range. the review page and the project page show
// the same line. a figure shows only when it holds data, and the whole line
// is left out when none does: for a new project the evidence carries the
// decision, and a row of zeros says nothing.

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ArrowRight } from "lucide-react";
import { Link } from "react-router";
import { apiCompareQuery, webCompareQuery } from "@/api/queries";
import type { EvidenceItem } from "@/components/evidence-list";
import { ChangeLine, countChange, type StatChange } from "@/components/stat-card";
import { ErrorState } from "@/components/states";
import { formatCount, formatDateRange, formatPercent } from "@/lib/format";
import { compareSearch } from "@/lib/global";

const DEFAULT_RANGE = { from: "", to: "" };

type Figure = { key: string; label: string; value: string; change?: StatChange | null };

// the figures of one project, the ones that hold data only
export function useProjectNumbers(tenantId: string, enabled = true) {
  const web = useQuery({ ...webCompareQuery([tenantId], DEFAULT_RANGE), enabled });
  const api = useQuery({ ...apiCompareQuery([tenantId], DEFAULT_RANGE), enabled });
  const w = web.data?.projects[0];
  const a = api.data?.projects[0];
  const onchain = w?.onchain ?? a?.onchain;
  const range = web.data ?? api.data;
  const figures: Figure[] = [];
  if (w && w.totals.pageviews > 0) {
    figures.push({
      key: "pageviews",
      label: "Page views",
      value: formatCount(w.totals.pageviews),
      change: countChange(w.totals.pageviews, w.previous_totals.pageviews),
    });
    figures.push({
      key: "visitors",
      label: "Visitors",
      value: formatCount(w.totals.visitors),
      change: countChange(w.totals.visitors, w.previous_totals.visitors),
    });
  }
  if (w?.integrity.grade)
    figures.push({ key: "grade", label: "Integrity grade", value: w.integrity.grade });
  if (a && a.totals.requests > 0) {
    figures.push({
      key: "requests",
      label: "API requests",
      value: formatCount(a.totals.requests),
      change: countChange(a.totals.requests, a.previous_totals.requests),
    });
    if (a.totals.rate_5xx !== null)
      figures.push({ key: "rate_5xx", label: "5xx rate", value: formatPercent(a.totals.rate_5xx) });
  }
  // nothing is measured without a verified id
  if (onchain && onchain.totals.ids_verified > 0 && onchain.totals.operations > 0)
    figures.push({
      key: "onchain",
      label: "On-chain operations",
      value: formatCount(onchain.totals.operations),
    });
  return {
    figures,
    range: range ? { from: range.from, to: range.to } : null,
    // only when both failed: one source still gives figures
    error: web.error && api.error ? web.error : null,
    retry: () => {
      web.refetch();
      api.refetch();
    },
    hasData: figures.length > 0,
  };
}

export type ProjectNumbersData = ReturnType<typeof useProjectNumbers>;

// the evidence next to the numbers, in one window. the events fact counts
// the last 30 full days and the numbers count the range up to today, so the
// two never stand side by side: with figures in view they carry the
// traffic; without them the events fact does.
export function withNumbers(items: EvidenceItem[], numbers: ProjectNumbersData): EvidenceItem[] {
  return numbers.hasData ? items.filter((e) => e.key !== "events") : items;
}

// the figures as one line, with their window as the heading. nothing
// renders while they load or when none holds data.
export function ProjectNumbers({
  tenantId,
  numbers,
  className,
}: {
  tenantId: string;
  numbers: ProjectNumbersData;
  className?: string;
}) {
  if (numbers.error)
    return (
      <ErrorState
        compact
        title="The numbers did not load"
        error={numbers.error}
        onRetry={numbers.retry}
      />
    );
  if (!numbers.hasData) return null;
  return (
    <section
      aria-labelledby={`numbers-${tenantId}`}
      data-numbers=""
      className={cn("flex min-w-0 flex-col gap-3", className)}
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 id={`numbers-${tenantId}`} className="text-title text-fg">
          {numbers.range
            ? `${formatDateRange(numbers.range.from, numbers.range.to)}, UTC`
            : "Numbers"}
        </h2>
        <Link
          to={`/operator/compare?${compareSearch([tenantId], DEFAULT_RANGE)}`}
          className="inline-flex items-center gap-1 rounded-sm text-small text-fg underline-offset-4 hover:underline focus-ring"
        >
          Open in Compare
          <ArrowRight aria-hidden className="size-3.5" />
        </Link>
      </div>
      <dl className="flex flex-wrap gap-x-8 gap-y-3">
        {numbers.figures.map((f) => (
          <div key={f.key} data-stat={f.key} className="flex min-w-0 flex-col-reverse gap-1">
            <dt className="text-caption text-fg-2">{f.label}</dt>
            <dd className="flex flex-wrap items-baseline gap-x-2 text-heading text-fg tabular">
              {f.value}
              {f.change ? <ChangeLine change={f.change} className="self-center" /> : null}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
