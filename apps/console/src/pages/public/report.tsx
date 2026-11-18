// the public page of a signed usage report (contract 20.5). anyone with the
// link reads it, and this browser checks the signature itself, from the
// canonical bytes of the report, against the keys this console publishes.
// no account is needed, and no figure shows before the signature holds.
// the figures lead; how the report was built is the footer.

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { CircleAlert, Download, FileX, ShieldCheck, ShieldX } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { useParams } from "react-router";
import { ApiError } from "@/api/client";
import {
  type PublicReport,
  publicReportQuery,
  type ReportDocument,
  reportDocument,
} from "@/api/reports";
import { percentileHiddenReason } from "@/components/cohort";
import { CopyButton } from "@/components/copy-button";
import { CoverageNote } from "@/components/coverage";
import { Disclosure } from "@/components/disclosure";
import { GradeBadge } from "@/components/integrity";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { PublicFrame } from "@/components/public-frame";
import { Sparkline, StatCard, type StatChange } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, MessagePage, Skeleton } from "@/components/states";
import { Pill } from "@/components/status";
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
import { chainGap, DAYS_WITHOUT_DATA, shortKey, weeklyActiveText } from "@/lib/onchain";
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
  else if (isNotFound(q.error) || (q.error instanceof ApiError && q.error.status === 410))
    // a removed project and an unknown id answer 404 and a revoked link
    // 410 report_revoked; this page names all three in one message
    body = (
      <MessagePage
        icon={<FileX aria-hidden />}
        title="This report link is not available"
        actions={<AboutLink />}
      >
        <p>
          The project revoked the link, the project was removed, or the address is not complete. Ask
          the project for a new link.
        </p>
      </MessagePage>
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
      variant="ghost"
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
      <p role="status" data-verdict="verified" className="text-small text-fg-2">
        <span className="font-medium text-fg">Signed by Veristar.</span> This browser checked the
        signature.
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
      // a holding signature is a compact trust mark under the title; one
      // that does not hold is a block the reader cannot miss
      className={cn(
        "flex min-w-0 flex-col gap-1",
        (tone === "bad" || tone === "warn") &&
          "rounded-lg border border-line bg-raised p-3 sm:p-4 print:border-line-strong",
      )}
    >
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="flex shrink-0 [&_svg]:size-4">{icon}</span>
        <div className="min-w-0">{line}</div>
        {download}
      </div>
      {verdict?.ok ? (
        <Disclosure
          variant="inline"
          summary="Signature details"
          className="text-small text-fg-2"
          bodyClassName="flex flex-col gap-2"
        >
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
        </Disclosure>
      ) : null}
    </section>
  );
}

// a decimal string of the report ("0.200000") as a number, null kept
function num(s: string | null | undefined): number | null {
  return s === null || s === undefined ? null : Number(s);
}

// a figure of the report: its label over its value; a figure the report
// has no value for is left out, never a dash
type Fig = { label: string; value: ReactNode; title?: string } | null;

function Figures({ items }: { items: Fig[] }) {
  const shown = items.filter((f): f is NonNullable<Fig> => f !== null);
  if (shown.length === 0) return null;
  return (
    <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3">
      {shown.map((f) => (
        <div key={f.label} className="flex min-w-0 flex-col gap-1">
          <dt className="text-caption text-fg-2" title={f.title}>
            {f.label}
          </dt>
          <dd className="min-w-0 text-body text-fg tabular">{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Section({
  title,
  meta,
  children,
}: {
  title: string;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section
      aria-label={title}
      className="flex min-w-0 break-inside-avoid flex-col gap-3 border-t border-line pt-6"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-title text-fg">{title}</h2>
        {meta}
      </div>
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

const fig = (label: string, value: ReactNode | null, title?: string): Fig =>
  value === null || value === undefined ? null : { label, value, title };
const pct = (s: string | null) => {
  const v = num(s);
  return v === null ? null : formatPercent(v);
};
const cnt = (v: number | null) => (v === null ? null : formatCount(v));

function changeOf(r: number | null): StatChange | null {
  if (r === null) return null;
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: formatChange(r),
    direction,
    good: direction === "flat" ? null : direction === "up",
    period: "previous period",
  };
}

function shaped(values: number[]): number[] | null {
  if (values.length < 2) return null;
  return Math.max(...values) === Math.min(...values) ? null : values;
}

function ReportBody({ doc, signature }: { doc: ReportDocument; signature: ReactNode }) {
  // version 2 adds coverage, has_data, lossy, infrastructure requests and
  // weekly active accounts, version 3 the calls per contract function; a
  // stored older report shows without them
  const v2 = doc.version === 2 || doc.version === 3 ? doc : null;
  const v3 = doc.version === 3 ? doc : null;
  const c = doc.cohort;
  const cohortName = [c.program, c.round, c.category].filter(Boolean).join(", ");
  const hidden = percentileHiddenReason(c.cohort_size);
  const waa = v2
    ? {
        ...v2.onchain.weekly_active_accounts,
        average: num(v2.onchain.weekly_active_accounts.average),
      }
    : null;
  const g = doc.integrity;
  // a figure no ledger read backs is null in format 3; older formats carry
  // 0, so the report reads both with the same missing-value rule as the console
  const chainMissing = chainGap(doc.onchain.ids_verified, doc.onchain.series);
  const chainOk = chainMissing === null;

  // the hero: the counts the project has, each with its change and its days
  const hero = [
    {
      key: "visitors",
      value: doc.web.visitors,
      spark: doc.web.series.map((p) => p.visitors ?? 0),
    },
    {
      key: "pageviews",
      value: doc.web.pageviews,
      spark: doc.web.series.map((p) => p.pageviews ?? 0),
    },
    { key: "requests", value: doc.api.requests, spark: doc.api.series.map((p) => p.requests) },
    {
      key: "onchain_operations",
      value: chainOk ? (doc.onchain.operations ?? 0) : 0,
      spark: doc.onchain.series.map((p) => p.operations ?? 0),
    },
  ].filter((h) => h.value > 0);

  // the cohort table keeps the rows and the columns that hold a value
  const rows = Object.entries(KPI_LABEL)
    .map(([k, label]) => ({
      k,
      label,
      change: num(c.kpis[k]?.change),
      pct: hidden ? null : num(c.kpis[k]?.percentile),
    }))
    .filter((r) => r.change !== null || r.pct !== null);
  const anyChange = rows.some((r) => r.change !== null);
  const anyPct = rows.some((r) => r.pct !== null);

  const webData = v2 ? v2.web.has_data : doc.web.pageviews > 0;
  const apiData = v2 ? v2.api.has_data : doc.api.requests > 0;

  return (
    <article className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-heading break-words text-fg">{doc.project.name}</h1>
          <p className="text-body text-fg-2">
            {formatDateRange(doc.period.from, doc.period.to)}, {countOf(doc.period.days, "day")},
            UTC
          </p>
        </div>
        {signature}
      </header>

      {hero.length > 0 ? (
        <section aria-label="Key figures" className="grid grid-cols-2 gap-3">
          {hero.map((h) => {
            const kpi = c.kpis[h.key];
            const p = hidden ? null : num(kpi?.percentile);
            const line = shaped(h.spark);
            return (
              <StatCard
                key={h.key}
                data-hero={h.key}
                label={KPI_LABEL[h.key] ?? h.key}
                value={h.value}
                change={changeOf(num(kpi?.change))}
                spark={line ? <Sparkline values={line} /> : undefined}
                footer={
                  p !== null ? (
                    <span className="text-caption text-fg-2 tabular">
                      {formatCohortPercentile(p)} in cohort
                    </span>
                  ) : null
                }
              />
            );
          })}
        </section>
      ) : null}

      {g.graded_days > 0 ? (
        <Section title="Integrity" meta={<GradeBadge grade={g.grade} />}>
          <div className="flex flex-col gap-2" data-testid="report-grade">
            {g.reasons.length > 0 ? (
              <ul className="flex flex-col gap-1 text-small">
                {g.reasons.map((x) => (
                  <li key={x.code} data-reason={x.code} className="flex gap-2">
                    <span className="min-w-0 flex-1 text-fg">{x.text}</span>
                    <span className="text-fg-2 tabular">{countOf(x.days, "day")}</span>
                  </li>
                ))}
              </ul>
            ) : null}
            <p className="text-caption text-fg-2 tabular">
              {countOf(g.graded_days, "graded site day")}: {formatCount(g.days_a)} A ·{" "}
              {formatCount(g.days_b)} B · {formatCount(g.days_c)} C
            </p>
          </div>
        </Section>
      ) : null}

      {webData ? (
        <Section title="Web">
          <Figures
            items={[
              doc.web.visits > 0 ? fig("Visits", formatCount(doc.web.visits)) : null,
              fig("Bounce rate", pct(doc.web.bounce_rate)),
              fig(
                "Average visit",
                num(doc.web.avg_visit_duration_seconds) === null
                  ? null
                  : formatDuration(num(doc.web.avg_visit_duration_seconds) ?? 0),
              ),
              fig("Quarantined share", pct(doc.web.quarantine_share)),
              fig("Gateway requests", formatCount(doc.web.gateway_requests)),
              doc.web.sites > 0
                ? fig(
                    "Verified sites",
                    `${formatCount(doc.web.sites_verified)} of ${formatCount(doc.web.sites)}`,
                  )
                : null,
            ]}
          />
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
      ) : null}

      {apiData ? (
        <Section title="API">
          <Figures
            items={[
              fig("4xx rate", pct(doc.api.rate_4xx)),
              fig("5xx rate", pct(doc.api.rate_5xx)),
              doc.api.p95.over_10s
                ? fig("p95 latency", OPEN_BUCKET)
                : fig(
                    "p95 latency",
                    num(doc.api.p95.seconds) === null
                      ? null
                      : formatLatency(num(doc.api.p95.seconds) ?? 0),
                  ),
              fig("Services", formatCount(doc.api.services)),
              fig("Distinct routes", formatCount(doc.api.routes)),
              v2 && v2.api.infrastructure_requests > 0
                ? fig(
                    "Infrastructure requests",
                    formatCount(v2.api.infrastructure_requests),
                    "Preflights, health checks and probes, left out of the figures",
                  )
                : null,
            ]}
          />
        </Section>
      ) : null}

      {doc.onchain.ids > 0 ? (
        <Section
          title="On-chain"
          meta={
            doc.onchain.two_way_link ? (
              <Pill tone="success">stellar.toml linked both ways</Pill>
            ) : null
          }
        >
          <Figures
            items={[
              chainOk ? fig("Transactions", cnt(doc.onchain.transactions)) : null,
              fig(
                "Verified IDs",
                `${formatCount(doc.onchain.ids_verified)} of ${formatCount(doc.onchain.ids)}`,
              ),
              chainOk
                ? fig("Days without data", cnt(doc.onchain.days_missing), DAYS_WITHOUT_DATA)
                : null,
              waa && waa.average !== null
                ? fig("Weekly active accounts", weeklyActiveText(waa))
                : null,
            ]}
          />
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
      ) : null}

      {cohortName || rows.length > 0 ? (
        <Section
          title="Cohort"
          meta={
            <span className="text-small text-fg-2 tabular">
              {countOf(c.cohort_size, "project")}
            </span>
          }
        >
          {cohortName ? (
            <p className="text-small text-fg" data-cohort="">
              {cohortName}
              {c.award_date ? (
                <span className="text-fg-2"> · awarded {formatDate(c.award_date)}</span>
              ) : null}
            </p>
          ) : null}
          {rows.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-body" aria-label="Key metrics">
                <thead>
                  <tr className="text-caption text-fg-2">
                    <th scope="col" className="py-2 pr-3 font-normal">
                      Metric
                    </th>
                    {anyChange ? (
                      <th scope="col" className="py-2 pr-3 text-right font-normal">
                        Change
                      </th>
                    ) : null}
                    {anyPct ? (
                      <th scope="col" className="py-2 text-right font-normal">
                        Percentile
                      </th>
                    ) : null}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr key={r.k} data-kpi={r.k} className="border-t border-line-subtle">
                      <th scope="row" className="py-2 pr-3 font-normal text-fg-2">
                        {r.label}
                      </th>
                      {anyChange ? (
                        <td className="py-2 pr-3 text-right text-fg tabular">
                          {r.change === null
                            ? null
                            : r.k === "error_rate"
                              ? formatPoints(r.change)
                              : formatChange(r.change)}
                        </td>
                      ) : null}
                      {anyPct ? (
                        <td className="py-2 text-right text-fg tabular">
                          {r.pct === null ? null : formatCohortPercentile(r.pct)}
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </Section>
      ) : null}

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

      <footer className="flex flex-col gap-2 border-t border-line pt-4 text-caption text-fg-2">
        <Disclosure
          variant="inline"
          summary={`Generated ${formatDateTimeUtc(doc.generated_at)}`}
          className="text-caption"
          bodyClassName="flex flex-col gap-2"
        >
          <p>Report {doc.id}</p>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4">
            <Build label="Report format">{doc.methods.format}</Build>
            <Build label="Web metrics">{doc.methods.web_metrics}</Build>
            <Build label="API metrics">{doc.methods.api_metrics}</Build>
            <Build label="On-chain">{doc.methods.onchain}</Build>
            <Build label="Cohort method">{doc.methods.cohort}</Build>
            {[doc.methods.integrity_rules].flat().length > 0 ? (
              <Build label="Integrity rules">
                {[doc.methods.integrity_rules].flat().join(", ")}
              </Build>
            ) : null}
            <Build label="Route rules">{doc.methods.route_normaliser}</Build>
            <Build label="Platform">
              <PlatformBuild value={doc.methods.platform} />
            </Build>
          </dl>
        </Disclosure>
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
