import { cn } from "@veristar/brand/cn";
import type { CoverageEstimate } from "@/api/schemas";
import { formatCount } from "@/lib/format";
import { formatShare } from "@/lib/global";

// the estimate of visits not measured because of privacy signals (contract
// 17.5). visitors whose browser sends global privacy control are not
// measured by design; the edge counts the requests for the tracker file with
// and without the signal, and the share of the second is the estimate. it is
// null below 100 requests, where one visitor more or less moves it by points.
export const COVERAGE_MIN_REQUESTS = 100;

export function coverageText(c: CoverageEstimate): string {
  if (c.unmeasured_share !== null) {
    return `About ${formatShare(c.unmeasured_share)} of visits are not measured because of privacy signals.`;
  }
  if (c.script_requests === 0) {
    return "No loads of the tracker file were attributed to this range yet, so the share of visits not measured because of privacy signals is unknown.";
  }
  return `Too few loads of the tracker file (${formatCount(c.script_requests)} of ${COVERAGE_MIN_REQUESTS}) to estimate the share of visits not measured because of privacy signals.`;
}

// the sentence and how it is measured, for the project's pages, the
// operator's project view and what SCF sees
export function CoverageNote({
  coverage,
  className,
}: {
  coverage: CoverageEstimate;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-1", className)} data-testid="coverage">
      <p className="text-small text-fg">{coverageText(coverage)}</p>
      <p className="max-w-prose text-caption text-fg-3">
        Browsers with Global Privacy Control (Brave, DuckDuckGo, Firefox private windows) load the
        tracker but are not measured, by design. Of {formatCount(coverage.script_requests)} loads of
        the tracker file, {formatCount(coverage.gpc_requests)} carried the signal. Visitors who
        opted out on the site itself are not part of this estimate.
      </p>
    </div>
  );
}

// the list column: the share, or a dash with the counts in the tooltip
export function CoverageCell({ coverage }: { coverage: CoverageEstimate }) {
  const title = `${formatCount(coverage.gpc_requests)} of ${formatCount(coverage.script_requests)} tracker loads with Global Privacy Control`;
  if (coverage.unmeasured_share === null) {
    return (
      <span className="text-fg-3" title={title} data-col="unmeasured_share">
        –
      </span>
    );
  }
  return (
    <span title={title} data-col="unmeasured_share">
      {formatShare(coverage.unmeasured_share)}
    </span>
  );
}
