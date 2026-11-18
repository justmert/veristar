import { cn } from "@veristar/brand/cn";
import type { CoverageEstimate } from "@/api/schemas";
import { formatCount, formatPercent } from "@/lib/format";

// the estimate of visits not measured because of privacy signals (contract
// 17.5). visitors whose browser sends global privacy control are not
// measured by design; the edge counts the requests for the tracker file with
// and without the signal, and the share of the second is the estimate. it is
// null below 100 requests, where one visitor more or less moves it by points.
export const COVERAGE_MIN_REQUESTS = 100;

export function coverageText(c: CoverageEstimate): string {
  if (c.unmeasured_share !== null) {
    return `About ${formatPercent(c.unmeasured_share)} of visits are not measured because of privacy signals.`;
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

// the list column: the share, or a dash when there are too few loads, with
// the counts behind it on a second line. shown, not left to a hover title,
// so keyboard and screen reader users read the meaning too
export function CoverageCell({ coverage }: { coverage: CoverageEstimate }) {
  const loads = `${formatCount(coverage.gpc_requests)} of ${formatCount(coverage.script_requests)} loads with GPC`;
  const share = coverage.unmeasured_share;
  return (
    <span className="inline-flex flex-col items-end">
      <span className={share === null ? "text-fg-3" : undefined} data-col="unmeasured_share">
        {share === null ? (
          <>
            <span aria-hidden>–</span>
            <span className="sr-only">no estimate</span>
          </>
        ) : (
          formatPercent(share)
        )}
      </span>
      <span className="font-sans text-caption text-fg-3" data-testid="coverage-detail">
        {share === null ? `too few loads, ${loads}` : loads}
      </span>
    </span>
  );
}
