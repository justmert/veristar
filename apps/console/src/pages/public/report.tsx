// the public page of a signed usage report (contract 20.5). anyone with the
// link reads it, and this browser checks the signature itself, from the
// canonical bytes of the report, against the keys this console publishes.
// no account is needed, and no figure shows before the signature holds.
// the standing leads (seal, project, period, cohort and the signature), then
// the five figures, then three short fact lists; how the report was built
// is the footer.

import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { CircleAlert, ShieldCheck, ShieldX } from "lucide-react";
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
import { Disclosure } from "@/components/disclosure";
import { LabeledFacts } from "@/components/labeled-facts";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { PublicFrame } from "@/components/public-frame";
import { StatCard, type StatChange, StatGrid } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, MessagePage, Skeleton } from "@/components/states";
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
import { GradeRing } from "./grade-ring";

export function PublicReportPage() {
  const { reportId = "" } = useParams();
  const q = useQuery(publicReportQuery(reportId));
  let body: ReactNode;
  let report = false;
  if (q.isPending)
    body = (
      <LoadingState label="Loading the report">
        <Skeleton.StatRow count={5} />
        <Skeleton.Table rows={4} columns={2} />
      </LoadingState>
    );
  else if (q.error instanceof ApiError && q.error.status === 410)
    // a revoked link answers 410 report_revoked, apart from an unknown id
    body = (
      <MessagePage code="410" title="This report link was revoked" actions={<AboutLink />}>
        <p>The project withdrew this link. Ask the project for a new one.</p>
      </MessagePage>
    );
  else if (isNotFound(q.error))
    // a removed project and an unknown id answer the same 404
    body = (
      <MessagePage code="404" title="This report link is not available" actions={<AboutLink />}>
        <p>The project was removed, or the address is not complete.</p>
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
  else {
    report = true;
    body = <ReportView r={q.data} />;
  }
  return (
    <PublicFrame center={!report && !q.isPending} header={<AboutLink quiet />} footer={!report}>
      {report || q.isPending ? (
        <div className="mx-auto flex w-full max-w-230 min-w-0 flex-col gap-6 print:max-w-none">
          {body}
        </div>
      ) : (
        body
      )}
    </PublicFrame>
  );
}

// a reader who followed a link learns what Veristar is from the docs
function AboutLink({ quiet = false }: { quiet?: boolean }) {
  const docs = siblingUrl(window.location.hostname, "docs");
  if (!docs) return null;
  if (quiet)
    return (
      <a
        href={docs}
        className="rounded-sm text-small text-fg-2 transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring print:hidden"
      >
        What is Veristar
      </a>
    );
  return (
    <Button asChild>
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
  // shows no figure: the reader would take it for a signed one
  if (verdict?.ok && parsed.success) return <ReportBody doc={parsed.data} signature={signature} />;
  return (
    <>
      {signature}
      {verdict?.ok ? (
        <EmptyState title="This page cannot show this report, download the JSON file to read it" />
      ) : null}
    </>
  );
}

// the signature card: whether it holds, checked in this browser, the json
// and the details (the standard, the key, how to check the file again)
function Signature({ verdict, r }: { verdict: Verdict | null; r: PublicReport }) {
  const name = typeof r.file.report.id === "string" ? r.file.report.id : r.id;
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
        <span className="font-semibold">Signature not valid.</span> Do not rely on this report:{" "}
        {verdict.reason}.
      </p>
    );
  } else if (verdict.key === "unknown") {
    tone = "warn";
    line = (
      <p role="alert" data-verdict="unknown-key" className="text-body text-fg">
        <span className="font-semibold">Not signed by Veristar.</span> The signature holds, but
        Veristar does not publish its key. Do not treat these figures as Veristar's.
      </p>
    );
  } else {
    tone = "ok";
    line = (
      <p role="status" data-verdict="verified" className="flex flex-col gap-1.5">
        <span className="text-[14px] font-semibold text-fg">Signed by Veristar</span>
        <span className="text-small text-fg-2">Checked in this browser</span>
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
      className={cn(
        "flex min-w-0 flex-col gap-3 rounded-2xl border bg-ground px-4.5 py-4 print:border-line-strong",
        tone === "bad"
          ? "border-critical-line"
          : tone === "warn"
            ? "border-warning-line"
            : "border-line",
      )}
    >
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 flex shrink-0 [&_svg]:size-4">{icon}</span>
        <div className="min-w-0">{line}</div>
      </div>
      <Disclosure
        variant="inline"
        summary="Details"
        className="flex flex-col text-small text-fg-2 [&>summary]:order-1 [&>summary]:mt-0"
        summaryClassName="hidden"
        bodyClassName="order-2 flex flex-col gap-2 pt-3"
      >
        {verdict?.ok ? (
          <>
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
          </>
        ) : (
          <p>The figures stay hidden while the signature does not hold.</p>
        )}
      </Disclosure>
      <div className="flex flex-wrap items-center gap-2 print:hidden">
        <Button
          variant="outline"
          size="sm"
          className="max-lg:h-(--vs-control-touch)"
          onClick={() => downloadFile(r.file, `veristar-report-${name}.json`)}
        >
          Download JSON
        </Button>
        <DetailsToggle />
      </div>
    </section>
  );
}

// the details sit in a <details> above the buttons; this button opens and
// closes it, so the toggle can sit beside Download JSON as on the canvas
function DetailsToggle() {
  const [open, setOpen] = useState(false);
  return (
    <Button
      variant="ghost"
      size="sm"
      aria-expanded={open}
      className="max-lg:h-(--vs-control-touch)"
      onClick={(e) => {
        const d = e.currentTarget.closest("section")?.querySelector("details");
        if (!d) return;
        d.open = !d.open;
        setOpen(d.open);
      }}
    >
      Details
    </Button>
  );
}

// a decimal string of the report ("0.200000") as a number, null kept
function num(s: string | null | undefined): number | null {
  return s === null || s === undefined ? null : Number(s);
}

// one fact list: a small caps title over label and value rows
type Row = { key: string; label: ReactNode; value: ReactNode; title?: string } | null;

function FactList({ title, rows }: { title: string; rows: Row[] }) {
  const shown = rows.filter((x): x is NonNullable<Row> => x !== null);
  if (shown.length === 0) return null;
  return (
    <section aria-label={title} className="flex min-w-0 break-inside-avoid flex-col">
      <h2 className="border-b border-line pb-2.5 text-caption font-semibold tracking-[0.06em] text-fg-2 uppercase">
        {title}
      </h2>
      <dl className="flex flex-col">
        {shown.map((x) => (
          <div
            key={x.key}
            data-row={x.key}
            className="flex items-baseline justify-between gap-4 border-b border-line-subtle py-2.5 text-small"
          >
            <dt className="min-w-0 text-fg-2" title={x.title}>
              {x.label}
            </dt>
            <dd className="shrink-0 text-right text-fg tabular">{x.value}</dd>
          </div>
        ))}
      </dl>
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

const row = (key: string, label: ReactNode, value: ReactNode | null, title?: string): Row =>
  value === null || value === undefined ? null : { key, label, value, title };
const pct = (s: string | null) => {
  const v = num(s);
  return v === null ? null : formatPercent(v);
};

function changeOf(r: number | null, points = false): StatChange | null {
  if (r === null) return null;
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: points ? formatPoints(r) : formatChange(r),
    direction,
    // an error rate that goes down is the good move
    good: direction === "flat" ? null : points ? direction === "down" : direction === "up",
    period: "previous period",
  };
}

function ReportBody({ doc, signature }: { doc: ReportDocument; signature: ReactNode }) {
  // version 2 adds coverage, has_data, lossy, infrastructure requests and
  // weekly active accounts, version 3 the calls per contract function; a
  // stored older report shows without them
  const v2 = doc.version === 2 || doc.version === 3 ? doc : null;
  const v3 = doc.version === 3 ? doc : null;
  const c = doc.cohort;
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
  const chainOk = chainGap(doc.onchain.ids_verified, doc.onchain.series) === null;
  const webData = v2 ? v2.web.has_data : doc.web.pageviews > 0;
  const apiData = v2 ? v2.api.has_data : doc.api.requests > 0;
  const kpi = (k: string) => c.kpis[k];

  // the five figures are always shown: 0 is 0, a figure the report has no
  // value for is the missing dash
  const rate5xx = apiData ? num(doc.api.rate_5xx) : null;
  const figures: {
    key: string;
    value: number | string | null;
    change: StatChange | null;
    reason: string;
  }[] = [
    {
      key: "visitors",
      value: doc.web.visitors,
      change: changeOf(num(kpi("visitors")?.change)),
      reason: "",
    },
    {
      key: "pageviews",
      value: doc.web.pageviews,
      change: changeOf(num(kpi("pageviews")?.change)),
      reason: "",
    },
    {
      key: "requests",
      value: doc.api.requests,
      change: changeOf(num(kpi("requests")?.change)),
      reason: "",
    },
    {
      key: "error_rate",
      value: rate5xx === null ? null : formatPercent(rate5xx),
      change: rate5xx === null ? null : changeOf(num(kpi("error_rate")?.change), true),
      reason: "No API requests in this period",
    },
    {
      key: "onchain_operations",
      value: chainOk && doc.onchain.ids_verified > 0 ? (doc.onchain.operations ?? 0) : null,
      change: chainOk ? changeOf(num(kpi("onchain_operations")?.change)) : null,
      reason: "No proven account or contract was read in this period",
    },
  ];

  const places = hidden
    ? []
    : Object.entries(KPI_LABEL)
        .map(([k, label]) => ({ k, label, p: num(c.kpis[k]?.percentile) }))
        .filter((x) => x.p !== null);

  const cohortFacts = [
    { label: "Program", value: c.program },
    { label: "Round", value: c.round },
    { label: "Category", value: c.category },
    { label: "Awarded", value: c.award_date ? formatDate(c.award_date) : null },
    { label: "Cohort", value: countOf(c.cohort_size, "project") },
  ].filter((f): f is { label: string; value: string } => f.value !== null && f.value !== "");

  const docs = siblingUrl(window.location.hostname, "docs");
  const rules = [doc.methods.integrity_rules].flat();

  return (
    <article className="flex min-w-0 flex-col gap-6">
      <header className="grid min-w-0 items-center gap-6 rounded-[20px] border border-line bg-raised p-5 sm:p-7 lg:grid-cols-[auto_minmax(0,1fr)_minmax(15rem,16rem)]">
        <GradeRing row={g} size="lg" className="max-sm:size-28" />
        <div className="flex min-w-0 flex-col gap-1.5">
          <h1 className="text-[32px] leading-10 font-semibold tracking-[-0.025em] break-words text-fg">
            {doc.project.name}
          </h1>
          <p className="text-body text-fg-2">
            {formatDateRange(doc.period.from, doc.period.to)}, {countOf(doc.period.days, "day")},
            UTC
          </p>
          <section aria-label="Cohort" data-cohort="" className="mt-3">
            <LabeledFacts items={cohortFacts} />
          </section>
        </div>
        {signature}
      </header>

      <StatGrid min="10rem" aria-label="Key figures">
        {figures.map((f) => (
          <StatCard
            key={f.key}
            data-hero={f.key}
            label={KPI_LABEL[f.key] ?? f.key}
            value={f.value}
            change={f.change}
            missingReason={f.reason}
          />
        ))}
      </StatGrid>

      <div className="grid gap-x-7 gap-y-6 md:grid-cols-3">
        <FactList
          title="Integrity"
          rows={[
            g.graded_days > 0
              ? row("a", "Site days graded A", formatCount(g.days_a))
              : row("graded", "Graded site days", "0"),
            g.graded_days > 0 ? row("b", "Site days graded B", formatCount(g.days_b)) : null,
            g.graded_days > 0 ? row("c", "Site days graded C", formatCount(g.days_c)) : null,
            ...g.reasons.map((x) => row(`reason-${x.code}`, x.text, countOf(x.days, "day"))),
            v2 && num(v2.web.coverage.unmeasured_share) !== null
              ? row(
                  "unmeasured",
                  "Not measured, privacy signals",
                  formatPercent(num(v2.web.coverage.unmeasured_share) ?? 0),
                  `${formatCount(v2.web.coverage.gpc_requests)} of ${formatCount(v2.web.coverage.script_requests)} loads of the tracker carried Global Privacy Control`,
                )
              : null,
          ]}
        />
        <FactList
          title="Usage"
          rows={[
            row("visits", "Visits", formatCount(doc.web.visits)),
            webData ? row("bounce", "Bounce rate", pct(doc.web.bounce_rate)) : null,
            webData && num(doc.web.avg_visit_duration_seconds) !== null
              ? row(
                  "visit",
                  "Average visit",
                  formatDuration(num(doc.web.avg_visit_duration_seconds) ?? 0),
                )
              : null,
            webData ? row("quarantine", "Quarantined share", pct(doc.web.quarantine_share)) : null,
            doc.web.gateway_requests > 0
              ? row("gateway", "Gateway requests", formatCount(doc.web.gateway_requests))
              : null,
            doc.web.sites > 0
              ? row(
                  "sites",
                  "Verified sites",
                  `${formatCount(doc.web.sites_verified)} of ${formatCount(doc.web.sites)}`,
                )
              : null,
            apiData ? row("4xx", "API 4xx rate", pct(doc.api.rate_4xx)) : null,
            apiData
              ? doc.api.p95.over_10s
                ? row("p95", "API p95 latency", OPEN_BUCKET)
                : row(
                    "p95",
                    "API p95 latency",
                    num(doc.api.p95.seconds) === null
                      ? null
                      : formatLatency(num(doc.api.p95.seconds) ?? 0),
                  )
              : null,
            apiData ? row("services", "API services", formatCount(doc.api.services)) : null,
            row("routes", "Distinct routes", formatCount(doc.api.routes)),
            v2 && v2.api.infrastructure_requests > 0
              ? row(
                  "infra",
                  "Infrastructure requests",
                  formatCount(v2.api.infrastructure_requests),
                  "Preflights, health checks and probes, left out of the figures",
                )
              : null,
          ]}
        />
        <FactList
          title="On-chain"
          rows={[
            row(
              "ids",
              "Verified IDs",
              `${formatCount(doc.onchain.ids_verified)} of ${formatCount(doc.onchain.ids)}`,
            ),
            chainOk && doc.onchain.transactions !== null
              ? row("tx", "Transactions", formatCount(doc.onchain.transactions))
              : null,
            waa && waa.average !== null
              ? row("waa", "Weekly active accounts", weeklyActiveText(waa))
              : null,
            chainOk && doc.onchain.days_missing !== null && doc.onchain.days_missing > 0
              ? row(
                  "missing",
                  "Days without data",
                  formatCount(doc.onchain.days_missing),
                  DAYS_WITHOUT_DATA,
                )
              : null,
            doc.onchain.two_way_link ? row("toml", "stellar.toml linked both ways", "Yes") : null,
            doc.rules_met.length > 0
              ? row(
                  "rules",
                  "SCF rules met",
                  formatCount(doc.rules_met.length),
                  doc.rules_met
                    .map((x) => `${x.name}, ${formatDateRange(x.from, x.to)}`)
                    .join("\n"),
                )
              : null,
          ]}
        />
      </div>

      {places.length > 0 ? (
        <FactList
          title="Place in cohort"
          rows={places.map((x) => row(`kpi-${x.k}`, x.label, formatCohortPercentile(x.p ?? 0)))}
        />
      ) : null}

      {v3 && v3.onchain.contracts.length > 0 ? (
        <section aria-label="Calls per function" className="flex min-w-0 flex-col gap-4">
          {v3.onchain.contracts.map((k) => (
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
        </section>
      ) : null}

      <footer className="mt-6 flex flex-col gap-3 border-t border-line pt-4 text-small text-fg-2">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
          <span>Generated {formatDateTimeUtc(doc.generated_at)}</span>
          <span>Report format {doc.methods.format}</span>
          <span className="inline-flex items-center gap-1">
            Platform build <PlatformBuild value={doc.methods.platform} />
          </span>
          {docs ? (
            <a
              href={`${docs}/how-it-works`}
              className="rounded-sm transition-colors duration-(--vs-duration-press) hover:text-fg focus-ring sm:ml-auto"
            >
              How the figures are made
            </a>
          ) : null}
        </div>
        <Disclosure
          variant="inline"
          summary="Methods"
          className="text-caption"
          bodyClassName="flex flex-col gap-2"
        >
          <dl className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2">
            <Build label="Report">{doc.id}</Build>
            <Build label="Web metrics">{doc.methods.web_metrics}</Build>
            <Build label="API metrics">{doc.methods.api_metrics}</Build>
            <Build label="On-chain">{doc.methods.onchain}</Build>
            <Build label="Cohort method">{doc.methods.cohort}</Build>
            {rules.length > 0 ? <Build label="Integrity rules">{rules.join(", ")}</Build> : null}
            <Build label="Route rules">{doc.methods.route_normaliser}</Build>
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
