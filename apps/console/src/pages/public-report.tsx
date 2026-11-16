import { useQuery } from "@tanstack/react-query";
import { CircleAlert, Download, ShieldCheck, ShieldX } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";
import { useParams } from "react-router";
import {
  type PublicReport,
  publicReportQuery,
  type ReportDocument,
  reportDocument,
} from "@/api/reports";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { Fact, Panel } from "@/components/panel";
import { PublicFrame } from "@/components/public-frame";
import { Pill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { isNotFound } from "@/lib/errors";
import { formatCount, formatDateTimeUtc } from "@/lib/format";
import { formatChange, formatDayRange, formatPercentile } from "@/lib/global";
import { type Verdict, verifyReport } from "@/lib/report-verify";
import { downloadFile } from "./reports";

// the public page of a signed usage report (contract 20.5): anyone with the
// link reads the report and this browser checks its signature itself, from
// the canonical bytes of the report and the platform's published keys. no
// account is needed and no number is taken on the server's word.
export function PublicReportPage() {
  const { reportId = "" } = useParams();
  const q = useQuery(publicReportQuery(reportId));
  return (
    <PublicFrame center={false}>
      <div className="mx-auto flex w-full max-w-content flex-col gap-5">
        {q.isPending ? <Loading label="Loading the report" /> : null}
        {isNotFound(q.error) ? (
          <StateBlock
            kind="empty"
            icon={<CircleAlert aria-hidden />}
            title="This report link is not available"
          >
            The link was revoked by the project, the project was removed, or the address is not
            complete. Ask the project for a new link.
          </StateBlock>
        ) : q.error ? (
          <ErrorPanel error={q.error} onRetry={() => q.refetch()} />
        ) : null}
        {q.data ? <ReportView r={q.data} /> : null}
      </div>
    </PublicFrame>
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
  return (
    <>
      <VerdictPanel verdict={verdict} r={r} />
      {parsed.success ? (
        <ReportBody doc={parsed.data} />
      ) : (
        <StateBlock kind="error" title="This report has a layout this page does not know">
          The signature check above still applies to it. Download the JSON to read it.
        </StateBlock>
      )}
    </>
  );
}

function VerdictPanel({ verdict, r }: { verdict: Verdict | null; r: PublicReport }) {
  const name = typeof r.file.report.id === "string" ? r.file.report.id : r.id;
  const download = (
    <Button variant="outline" onClick={() => downloadFile(r.file, `veristar-report-${name}.json`)}>
      <Download aria-hidden />
      Download JSON
    </Button>
  );
  if (verdict === null) {
    return (
      <Panel title="Checking the signature" aria-label="Signature" actions={download}>
        <p role="status" className="text-body text-fg-2">
          This browser is checking the report's signature.
        </p>
      </Panel>
    );
  }
  if (!verdict.ok) {
    return (
      <Panel title="Signature not valid" aria-label="Signature" actions={download}>
        <p role="alert" data-verdict="invalid" className="flex items-start gap-2 text-body text-fg">
          <ShieldX aria-hidden className="mt-0.5 size-4 shrink-0 text-critical" />
          Do not rely on this report: {verdict.reason}.
        </p>
      </Panel>
    );
  }
  const keyText =
    verdict.key === "current"
      ? "Veristar's current report key"
      : verdict.key === "retired"
        ? "an earlier Veristar report key, still published"
        : "a key Veristar does not publish";
  return (
    <Panel title="Signature verified" aria-label="Signature" actions={download}>
      <div className="flex flex-col gap-2">
        <p
          role="status"
          data-verdict={verdict.key === "unknown" ? "unknown-key" : "verified"}
          className="flex items-start gap-2 text-body text-fg"
        >
          {verdict.key === "unknown" ? (
            <ShieldX aria-hidden className="mt-0.5 size-4 shrink-0 text-warning" />
          ) : (
            <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-success" />
          )}
          This browser checked the SEP-53 signature over the report's canonical form. It was made by{" "}
          {keyText}.
        </p>
        <code className="break-all font-mono text-caption text-fg-2">{verdict.signer}</code>
        <p className="text-caption text-fg-3">
          The published keys are in{" "}
          <a href={r.keys.stellar_toml} className="underline underline-offset-2 hover:text-fg">
            stellar.toml
          </a>
          . Verify the downloaded file yourself with any SEP-53 implementation.
        </p>
      </div>
    </Panel>
  );
}

function Grid({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">{children}</dl>;
}

function num(v: number): string {
  return formatCount(v);
}

// a decimal string of the report as a share
function share(v: string | null): string {
  if (v === null) return "n/a";
  const n = Number(v);
  return `${(n * 100).toFixed(2).replace(/\.?0+$/, "")}%`;
}

function seconds(l: { seconds: string | null; over_10s: boolean }): string {
  if (l.over_10s) return ">10 s";
  if (l.seconds === null) return "n/a";
  return `${Number(l.seconds).toFixed(3)} s`;
}

const KPI_LABEL: Record<string, string> = {
  visitors: "Verified visitors",
  pageviews: "Page views",
  requests: "API requests",
  error_rate: "API error rate (5xx)",
  onchain_operations: "On-chain operations",
};

function ReportBody({ doc }: { doc: ReportDocument }) {
  const c = doc.cohort;
  const cohortName = [c.program, c.round, c.category].filter(Boolean).join(", ");
  const population =
    c.percentile_program || c.percentile_round
      ? [c.percentile_program, c.percentile_round].filter(Boolean).join(", ")
      : "every enrolled project";
  return (
    <>
      <div className="flex flex-col gap-1">
        <h1 className="text-page text-fg">{doc.project.name}</h1>
        <p className="text-body text-fg-2">
          Usage report for {formatDayRange(doc.period.from, doc.period.to)}, {doc.period.days} days.
          Generated {formatDateTimeUtc(doc.generated_at)}.
        </p>
      </div>

      <Panel title="Integrity" aria-label="Integrity">
        <div className="flex flex-col gap-2">
          <span className="text-section text-fg" data-testid="report-grade">
            Grade {doc.integrity.grade ?? "not graded"}
          </span>
          <span className="text-small text-fg-2">
            {doc.integrity.graded_days} graded site days: {doc.integrity.days_a} A,{" "}
            {doc.integrity.days_b} B, {doc.integrity.days_c} C
          </span>
          {doc.integrity.reasons.length > 0 ? (
            <ul className="flex flex-col gap-1 text-small text-fg-2">
              {doc.integrity.reasons.map((x) => (
                <li key={x.code}>
                  {x.text} ({x.days} {x.days === 1 ? "day" : "days"})
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </Panel>

      <Panel title="Web" aria-label="Web">
        <Grid>
          <Fact label="Verified visitors">{num(doc.web.visitors)}</Fact>
          <Fact label="Page views">{num(doc.web.pageviews)}</Fact>
          <Fact label="Visits">{num(doc.web.visits)}</Fact>
          <Fact label="Bounce rate">{share(doc.web.bounce_rate)}</Fact>
          <Fact label="Quarantined share">{share(doc.web.quarantine_share)}</Fact>
          <Fact label="Gateway requests">{num(doc.web.gateway_requests)}</Fact>
          <Fact label="Sites">{num(doc.web.sites)}</Fact>
          <Fact label="Verified sites">{num(doc.web.sites_verified)}</Fact>
        </Grid>
      </Panel>

      <Panel title="API" aria-label="API">
        <Grid>
          <Fact label="Requests">{num(doc.api.requests)}</Fact>
          <Fact label="4xx rate">{share(doc.api.rate_4xx)}</Fact>
          <Fact label="5xx rate">{share(doc.api.rate_5xx)}</Fact>
          <Fact label="p95 latency">{seconds(doc.api.p95)}</Fact>
          <Fact label="Services">{num(doc.api.services)}</Fact>
          <Fact label="Routes">{num(doc.api.routes)}</Fact>
        </Grid>
      </Panel>

      <Panel
        title="On-chain"
        aria-label="On-chain"
        actions={
          doc.onchain.two_way_link ? (
            <Pill tone="success">stellar.toml linked both ways</Pill>
          ) : null
        }
      >
        <Grid>
          <Fact label="Operations">{num(doc.onchain.operations)}</Fact>
          <Fact label="Transactions">{num(doc.onchain.transactions)}</Fact>
          <Fact label="Verified ids">{`${doc.onchain.ids_verified} of ${doc.onchain.ids}`}</Fact>
          <Fact label="Days without data">{num(doc.onchain.days_missing)}</Fact>
        </Grid>
      </Panel>

      <Panel
        title="Cohort"
        aria-label="Cohort"
        description={`${cohortName ? `Confirmed cohort: ${cohortName}${c.award_date ? `, awarded ${c.award_date}` : ""}. ` : "No confirmed cohort. "}Percentiles among ${population} (${c.cohort_size} projects): the share with a value at or below this project's.`}
      >
        <table className="w-full text-left text-small tabular" aria-label="Key metrics">
          <thead>
            <tr className="text-caption text-fg-3">
              <th scope="col" className="py-1.5 pr-3 font-medium">
                Metric
              </th>
              <th scope="col" className="py-1.5 pr-3 text-right font-medium">
                Change
              </th>
              <th scope="col" className="py-1.5 text-right font-medium">
                Percentile
              </th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(KPI_LABEL).map(([k, label]) => {
              const v = c.kpis[k];
              const n = (s: string | null | undefined) =>
                s === null || s === undefined ? null : Number(s);
              return (
                <tr key={k} className="border-t border-line-subtle">
                  <th scope="row" className="py-1.5 pr-3 font-normal text-fg-2">
                    {label}
                  </th>
                  <td className="py-1.5 pr-3 text-right font-mono text-fg">
                    {formatChange(n(v?.change), k === "error_rate" ? "points" : "relative") ??
                      "n/a"}
                  </td>
                  <td className="py-1.5 text-right font-mono text-fg">
                    {formatPercentile(n(v?.percentile)) ?? "n/a"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Panel>

      {doc.rules_met.length > 0 ? (
        <Panel title="SCF rules met" aria-label="SCF rules met">
          <ul className="flex flex-col gap-1 text-small text-fg-2">
            {doc.rules_met.map((x) => (
              <li key={x.name}>
                <span className="font-medium text-fg">{x.name}</span>, {x.from} to {x.to}
              </li>
            ))}
          </ul>
        </Panel>
      ) : null}

      <Panel title="How it was made" aria-label="Methods">
        <Grid>
          <Fact label="Report format">{doc.methods.format}</Fact>
          <Fact label="Web metrics">{doc.methods.web_metrics}</Fact>
          <Fact label="API metrics">{doc.methods.api_metrics}</Fact>
          <Fact label="On-chain">{doc.methods.onchain}</Fact>
          <Fact label="Cohort method">{doc.methods.cohort}</Fact>
          <Fact label="Integrity rules">{doc.methods.integrity_rules}</Fact>
          <Fact label="Route normaliser">{doc.methods.route_normaliser}</Fact>
          <Fact label="Platform">{doc.methods.platform}</Fact>
        </Grid>
        <p className="mt-3 text-caption text-fg-3">
          The report holds aggregates only: daily and period totals, no visitor, address or user
          agent.
        </p>
      </Panel>
    </>
  );
}
