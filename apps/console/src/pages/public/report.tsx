// the public page of a signed usage report (contract 20.5). anyone with the
// link reads it, and this browser checks the signature itself, from the
// canonical bytes of the report, against the keys this console publishes.
// no account is needed, and no figure shows before the signature holds.
// the figures lead; how the report was built is the footer.

import { useQuery } from "@tanstack/react-query";
import { ChevronRight, CircleAlert, Download, FileX, ShieldCheck, ShieldX } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { useParams } from "react-router";
import {
  type PublicReport,
  publicReportQuery,
  type ReportDocument,
  reportDocument,
} from "@/api/reports";
import { percentileHiddenReason } from "@/components/cohort";
import { CopyButton } from "@/components/copy-button";
import { CoverageNote } from "@/components/coverage";
import { gradeMeaning } from "@/components/integrity";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { PublicFrame } from "@/components/public-frame";
import { EmptyState, ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import {
  countOf,
  formatChange,
  formatCohortPercentile,
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatDuration,
  formatLatency,
  formatPercent,
  formatPoints,
  OPEN_BUCKET,
} from "@/lib/format";
import { siblingUrl } from "@/lib/hosts";
import {
  chainGap,
  DAYS_WITHOUT_DATA,
  NO_VERIFIED_IDS,
  shortKey,
  weeklyActiveText,
} from "@/lib/onchain";
import { type Verdict, verifyReport } from "@/lib/report-verify";
import { downloadFile } from "@/lib/reports";

export function PublicReportPage() {
  const { reportId = "" } = useParams();
  const q = useQuery(publicReportQuery(reportId));
  let body: ReactNode;
  if (q.isPending)
    body = (
      <LoadingState label="Loading the report">
        <Skeleton.StatRow count={4} />
        <Skeleton.Table rows={4} columns={2} />
      </LoadingState>
    );
  else if (isNotFound(q.error))
    // a revoked link, a removed project and an unknown id answer the same
    // 404, so the page cannot tell them apart and names all three
    body = (
      <EmptyState
        icon={<FileX aria-hidden strokeWidth={1.75} />}
        title="This report link is not available"
        className="w-full"
        action={<AboutLink />}
      >
        The project revoked the link, the project was removed, or the address is not complete. Ask
        the project for a new link.
      </EmptyState>
    );
  else if (q.error || !q.data)
    body = (
      <ErrorState
        title="The report did not load"
        error={q.error}
        onRetry={() => q.refetch()}
        className="w-full"
      />
    );
  else body = <ReportView r={q.data} />;
  return (
    <PublicFrame center={false}>
      <div className="mx-auto flex w-full max-w-prose min-w-0 flex-col gap-8 print:max-w-none">
        {body}
      </div>
    </PublicFrame>
  );
}

// a reader who followed a dead link learns what Veristar is from the docs
function AboutLink() {
  const docs = siblingUrl(window.location.hostname, "docs");
  if (!docs) return null;
  return (
    <Button asChild variant="outline" size="sm">
      <a href={docs}>What is Veristar</a>
    </Button>
  );
}

function useVerdict(r: PublicReport): Verdict | null {
  const [v, setV] = useState<Verdict | null>(null);
  useEffect(() => {
    let live = true;
    setV(null);
    verifyReport(r.file, r.keys).then((x) => {
      if (live) setV(x);
    });
    return () => {
      live = false;
    };
  }, [r]);
  return v;
}

export function ReportView({ r }: { r: PublicReport }) {
  const verdict = useVerdict(r);
  const parsed = reportDocument.safeParse(r.file.report);
  const signature = <Signature verdict={verdict} r={r} />;
  // a report whose signature does not hold, or is still being checked,
  // shows no figure: the reader would take it for a signed one. a signed
  // report leads with its title and period, the signature right under them
  if (verdict?.ok && parsed.success) return <ReportBody doc={parsed.data} signature={signature} />;
  return (
    <>
      {signature}
      {verdict?.ok ? (
        <EmptyState icon={<CircleAlert aria-hidden />} title="This page cannot show this report">
          The report has a layout this page does not know. The signature check above still holds.
          Download the JSON file to read it.
        </EmptyState>
      ) : null}
    </>
  );
}

// the signature in one plain line first; the standard, the key and how to
// check the file again sit under "Signature details"
function Signature({ verdict, r }: { verdict: Verdict | null; r: PublicReport }) {
  const name = typeof r.file.report.id === "string" ? r.file.report.id : r.id;
  const download = (
    <Button
      variant="outline"
      size="sm"
      className="print:hidden max-lg:h-(--vs-control-touch)"
      onClick={() => downloadFile(r.file, `veristar-report-${name}.json`)}
    >
      <Download aria-hidden />
      Download JSON
    </Button>
  );
  let line: ReactNode;
  let tone: "ok" | "warn" | "bad" | "wait";
  if (verdict === null) {
    tone = "wait";
    line = (
      <p role="status" className="text-body text-fg-2">
        This browser checks the signature.
      </p>
    );
  } else if (!verdict.ok) {
    tone = "bad";
    line = (
      <p role="alert" data-verdict="invalid" className="text-body text-fg">
        <span className="font-medium">Signature not valid.</span> Do not rely on this report:{" "}
        {verdict.reason}.
      </p>
    );
  } else if (verdict.key === "unknown") {
    tone = "warn";
    line = (
      <p role="alert" data-verdict="unknown-key" className="text-body text-fg">
        <span className="font-medium">Not signed by Veristar.</span> The signature holds, but
        Veristar does not publish its key. Do not treat these figures as Veristar's.
      </p>
    );
  } else {
    tone = "ok";
    line = (
      <p role="status" data-verdict="verified" className="text-body text-fg">
        <span className="font-medium">Signed by Veristar.</span> This browser checked the signature.
      </p>
    );
  }
  const icon =
    tone === "ok" ? (
      <ShieldCheck aria-hidden className="text-success" />
    ) : tone === "bad" ? (
      <ShieldX aria-hidden className="text-critical" />
    ) : tone === "warn" ? (
      <CircleAlert aria-hidden className="text-warning" />
    ) : (
      <ShieldCheck aria-hidden className="animate-skeleton text-fg-3" />
    );
  const keyText =
    verdict?.ok && verdict.key === "current"
      ? "Veristar's current report key"
      : verdict?.ok && verdict.key === "retired"
        ? "an earlier Veristar report key that Veristar still publishes"
        : "a key that Veristar does not publish";
  return (
    <section
      aria-label="Signature"
      className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-raised p-3 sm:p-4 print:border-line-strong"
    >
      <div className="flex flex-wrap items-start gap-3">
        <span className="mt-1 flex shrink-0 [&_svg]:size-5">{icon}</span>
        <div className="min-w-0 flex-1">{line}</div>
        {download}
      </div>
      {verdict?.ok ? (
        <details className="group text-small text-fg-2">
          <summary className="flex w-fit cursor-pointer list-none items-center gap-1 rounded-sm text-fg-2 transition-colors duration-(--vs-duration-press) ease-standard hover:text-fg focus-ring max-lg:min-h-(--vs-control-touch) [&::-webkit-details-marker]:hidden">
            <ChevronRight
              aria-hidden
              className="size-4 transition-transform duration-(--vs-duration-press) ease-standard group-open:rotate-90"
            />
            Signature details
          </summary>
          <div className="mt-2 flex flex-col gap-2">
            <p>
              The signature follows SEP-53, the Stellar standard for signed messages. It covers the
              canonical form of the report. It was made by {keyText}:
            </p>
            <code className="font-mono text-caption break-all text-fg">{verdict.signer}</code>
            <p>
              Veristar publishes its report keys in its{" "}
              <a
                href={r.keys.stellar_toml}
                className="rounded-sm text-fg underline decoration-line-strong underline-offset-2 hover:decoration-fg focus-ring"
              >
                stellar.toml file
              </a>
              . Check the downloaded JSON file again with any SEP-53 tool.
            </p>
          </div>
        </details>
      ) : null}
    </section>
  );
}

// a decimal string of the report ("0.200000") as a number, null kept
function num(s: string | null | undefined): number | null {
  return s === null || s === undefined ? null : Number(s);
}

function shareOrMissing(s: string | null, why: string): ReactNode {
  const v = num(s);
  return v === null ? <Missing reason={why} /> : formatPercent(v);
}

function latency(l: { seconds: string | null; over_10s: boolean }): ReactNode {
  if (l.over_10s) return OPEN_BUCKET;
  const v = num(l.seconds);
  return v === null ? <Missing reason="No API requests in this period" /> : formatLatency(v);
}

function Hero({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-lg border border-line bg-raised p-4 print:border-line-strong">
      <dt className="text-caption text-fg-2">{label}</dt>
      <dd className="min-w-0 font-mono text-metric text-fg tabular">{children}</dd>
    </div>
  );
}

function Figure({
  label,
  title,
  children,
}: {
  label: string;
  title?: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <dt className="text-caption text-fg-2" title={title}>
        {label}
      </dt>
      <dd className="min-w-0 font-mono text-body text-fg tabular">{children}</dd>
    </div>
  );
}

function Section({
  title,
  meta,
  note,
  children,
}: {
  title: string;
  meta?: ReactNode;
  note?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className="flex min-w-0 break-inside-avoid flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-section text-fg">{title}</h2>
        {meta}
      </div>
      {note ? <p className="text-small text-fg-2">{note}</p> : null}
      {children}
    </section>
  );
}

const KPI_LABEL: Record<string, string> = {
  visitors: "Verified visitors",
  pageviews: "Page views",
  requests: "API requests",
  error_rate: "API 5xx rate",
  onchain_operations: "On-chain operations",
};

function ReportBody({ doc, signature }: { doc: ReportDocument; signature: ReactNode }) {
  // version 2 adds coverage, has_data, lossy, infrastructure requests and
  // weekly active accounts, version 3 the calls per contract function; a
  // stored older report shows without them
  const v2 = doc.version === 2 || doc.version === 3 ? doc : null;
  const v3 = doc.version === 3 ? doc : null;
  const c = doc.cohort;
  const cohortName = [c.program, c.round, c.category].filter(Boolean).join(", ");
  const population =
    c.percentile_program || c.percentile_round
      ? [c.percentile_program, c.percentile_round].filter(Boolean).join(", ")
      : "every enrolled project";
  const hidden = percentileHiddenReason(c.cohort_size);
  // a table of dashes says nothing; one line says it instead
  const noCohortValue = Object.keys(KPI_LABEL).every((k) => {
    const v = c.kpis[k];
    return num(v?.change) === null && (hidden !== null || num(v?.percentile) === null);
  });
  const waa = v2
    ? {
        ...v2.onchain.weekly_active_accounts,
        average: num(v2.onchain.weekly_active_accounts.average),
      }
    : null;
  const g = doc.integrity;
  // the signed figures carry 0 where no verified id was read; the report
  // reads them with the same missing-value rule as the console
  const chainMissing = chainGap(doc.onchain.ids_verified, doc.onchain.series);
  return (
    <article className="flex min-w-0 flex-col gap-8">
      <header className="flex flex-col gap-1">
        <h1 className="text-heading break-words text-fg">{doc.project.name}</h1>
        <p className="text-body text-fg-2">
          Usage report for {formatDateRange(doc.period.from, doc.period.to)},{" "}
          {countOf(doc.period.days, "day")}, UTC.
        </p>
      </header>

      {signature}

      <dl aria-label="Key figures" className="grid grid-cols-2 gap-3">
        <Hero label="Verified visitors">{formatCount(doc.web.visitors)}</Hero>
        <Hero label="Page views">{formatCount(doc.web.pageviews)}</Hero>
        <Hero label="API requests">{formatCount(doc.api.requests)}</Hero>
        <Hero label="On-chain operations">
          {chainMissing ? <Missing reason={chainMissing} /> : formatCount(doc.onchain.operations)}
        </Hero>
      </dl>

      <Section title="Integrity">
        <div className="flex flex-col gap-2">
          <span data-testid="report-grade" className="w-fit">
            <StatusBadge kind="grade" grade={g.grade} />
          </span>
          <p className="text-small text-fg">{gradeMeaning(g.grade)}</p>
          <p className="text-caption text-fg-2">
            {g.graded_days === 0
              ? "No graded site day in this period."
              : `${countOf(g.graded_days, "graded site day")}: ${formatCount(g.days_a)} A, ${formatCount(g.days_b)} B, ${formatCount(g.days_c)} C.`}
          </p>
          {g.reasons.length > 0 ? (
            <ul className="flex flex-col gap-1 text-small text-fg-2">
              {g.reasons.map((x) => (
                <li key={x.code} data-reason={x.code}>
                  {x.text} ({countOf(x.days, "day")})
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </Section>

      <Section
        title="Web"
        note={v2 && !v2.web.has_data ? "No web traffic was recorded in this period." : undefined}
      >
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          <Figure label="Visits">{formatCount(doc.web.visits)}</Figure>
          <Figure label="Bounce rate">{shareOrMissing(doc.web.bounce_rate, "No visits")}</Figure>
          <Figure label="Average visit">
            {num(doc.web.avg_visit_duration_seconds) === null ? (
              <Missing reason="No visits" />
            ) : (
              formatDuration(num(doc.web.avg_visit_duration_seconds) ?? 0)
            )}
          </Figure>
          <Figure label="Quarantined share">
            {shareOrMissing(doc.web.quarantine_share, "No page views")}
          </Figure>
          <Figure label="Gateway requests">{formatCount(doc.web.gateway_requests)}</Figure>
          <Figure label="Verified sites">
            {formatCount(doc.web.sites_verified)} of {formatCount(doc.web.sites)}
          </Figure>
        </dl>
        {v2 ? (
          <CoverageNote
            coverage={{
              script_requests: v2.web.coverage.script_requests,
              gpc_requests: v2.web.coverage.gpc_requests,
              unmeasured_share: num(v2.web.coverage.unmeasured_share),
            }}
          />
        ) : null}
      </Section>

      <Section
        title="API"
        note={v2 && !v2.api.has_data ? "No API traffic was recorded in this period." : undefined}
      >
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          <Figure label="4xx rate">{shareOrMissing(doc.api.rate_4xx, "No API requests")}</Figure>
          <Figure label="5xx rate">{shareOrMissing(doc.api.rate_5xx, "No API requests")}</Figure>
          <Figure label="p95 latency">{latency(doc.api.p95)}</Figure>
          <Figure label="Services">{formatCount(doc.api.services)}</Figure>
          <Figure label="Distinct routes">{formatCount(doc.api.routes)}</Figure>
          {v2 ? (
            <Figure label="Infrastructure requests">
              {formatCount(v2.api.infrastructure_requests)}
            </Figure>
          ) : null}
        </dl>
        {v2 ? (
          <p className="text-caption text-fg-2">
            Requests, rates and latency count application requests only. Preflights, health checks
            and probes are left out.
            {v2.api.lossy ? " Some latency buckets are approximate." : ""}
          </p>
        ) : null}
      </Section>

      <Section
        title="On-chain"
        meta={
          doc.onchain.two_way_link ? (
            <Pill tone="success">stellar.toml linked both ways</Pill>
          ) : null
        }
      >
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          <Figure label="Transactions">
            {chainMissing ? (
              <Missing reason={chainMissing} />
            ) : (
              formatCount(doc.onchain.transactions)
            )}
          </Figure>
          <Figure label="Verified ids">
            {formatCount(doc.onchain.ids_verified)} of {formatCount(doc.onchain.ids)}
          </Figure>
          <Figure label="Days without data" title={DAYS_WITHOUT_DATA}>
            {doc.onchain.ids_verified === 0 ? (
              <Missing reason={NO_VERIFIED_IDS} />
            ) : (
              formatCount(doc.onchain.days_missing)
            )}
          </Figure>
          {waa ? (
            <div className="col-span-2 flex min-w-0 flex-col gap-1 sm:col-span-3">
              <dt className="text-caption text-fg-2">Weekly active accounts</dt>
              <dd className="text-body text-fg">{weeklyActiveText(waa)}</dd>
            </div>
          ) : null}
        </dl>
        {v3?.onchain.contracts.map((k) => (
          <div key={k.strkey} className="flex min-w-0 flex-col gap-2" data-contract={k.strkey}>
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <span className="text-small text-fg">
                Contract{" "}
                <code className="font-mono" title={k.strkey}>
                  {shortKey(k.strkey)}
                </code>
              </span>
              <span className="text-caption text-fg-2">{functionsDaysText(k)}</span>
            </div>
            <FunctionsTable summary={k} label={`Calls per function of ${k.strkey}`} />
          </div>
        ))}
      </Section>

      <Section
        title="Cohort"
        note={`${
          cohortName
            ? `Confirmed cohort: ${cohortName}${c.award_date ? `, awarded ${formatDate(c.award_date)}` : ""}.`
            : "No confirmed cohort."
        } Percentiles place the project among ${population}: ${countOf(c.cohort_size, "project")}.`}
      >
        {noCohortValue ? (
          <p className="text-small text-fg" data-testid="cohort-no-values">
            No metric has a change or a percentile in this period.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-small" aria-label="Key metrics">
              <thead>
                <tr className="text-caption text-fg-2">
                  <th scope="col" className="py-2 pr-3 font-normal">
                    Metric
                  </th>
                  <th scope="col" className="py-2 pr-3 text-right font-normal">
                    Change
                  </th>
                  <th scope="col" className="py-2 text-right font-normal">
                    Percentile
                  </th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(KPI_LABEL).map(([k, label]) => {
                  const v = c.kpis[k];
                  const change = num(v?.change);
                  const pct = num(v?.percentile);
                  return (
                    <tr key={k} data-kpi={k} className="border-t border-line-subtle">
                      <th scope="row" className="py-2 pr-3 font-normal text-fg-2">
                        {label}
                      </th>
                      <td className="py-2 pr-3 text-right font-mono text-fg tabular">
                        {change === null ? (
                          <Missing reason="No value in the previous period" />
                        ) : k === "error_rate" ? (
                          formatPoints(change)
                        ) : (
                          formatChange(change)
                        )}
                      </td>
                      <td className="py-2 text-right font-mono text-fg tabular">
                        {hidden ? (
                          <Missing reason={hidden} />
                        ) : pct === null ? (
                          <Missing reason="No value in this period" />
                        ) : (
                          formatCohortPercentile(pct)
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
        {hidden ? (
          <p className="text-caption text-fg-2" data-testid="percentile-hidden">
            Percentiles show from a cohort of 5 projects. This cohort has{" "}
            {countOf(c.cohort_size, "project")}. The JSON file keeps them.
          </p>
        ) : null}
      </Section>

      {doc.rules_met.length > 0 ? (
        <Section title="SCF rules met">
          <ul className="flex flex-col gap-1 text-small text-fg-2">
            {doc.rules_met.map((x) => (
              <li key={x.name}>
                <span className="font-medium text-fg">{x.name}</span>,{" "}
                {formatDateRange(x.from, x.to)}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      <footer className="flex flex-col gap-3 border-t border-line pt-4 text-caption text-fg-2">
        <p>
          Generated {formatDateTimeUtc(doc.generated_at)}. Report {doc.id}. The report holds daily
          and period totals only, with no visitor, address or user agent.
        </p>
        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4">
          <Build label="Report format">{doc.methods.format}</Build>
          <Build label="Web metrics">{doc.methods.web_metrics}</Build>
          <Build label="API metrics">{doc.methods.api_metrics}</Build>
          <Build label="On-chain">{doc.methods.onchain}</Build>
          <Build label="Cohort method">{doc.methods.cohort}</Build>
          <Build label="Integrity rules">
            {[doc.methods.integrity_rules].flat().join(", ") || <Missing reason="Not graded" />}
          </Build>
          <Build label="Route rules">{doc.methods.route_normaliser}</Build>
          <Build label="Platform">
            <PlatformBuild value={doc.methods.platform} />
          </Build>
        </dl>
      </footer>
    </article>
  );
}

// the platform build is a name and a commit hash; the hash shows short,
// with the full value in the tooltip and on the copy button
function PlatformBuild({ value }: { value: string }) {
  const parts = value.split(" ");
  const hash = parts.find((p) => /^[0-9a-f]{12,}$/.test(p));
  if (!hash) return <span className="break-all">{value}</span>;
  const short = parts.map((p) => (p === hash ? p.slice(0, 7) : p)).join(" ");
  return (
    <span className="inline-flex min-w-0 items-center gap-1">
      <span title={value}>{short}</span>
      <span className="print:hidden">
        <CopyButton
          text={value}
          label="Copy the platform build"
          iconOnly
          size="xs"
          variant="ghost"
        />
      </span>
    </span>
  );
}

function Build({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 gap-1">
      <dt>{label}</dt>
      <dd className="font-mono text-fg">{children}</dd>
    </div>
  );
}
