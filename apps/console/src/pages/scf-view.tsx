import { useQuery } from "@tanstack/react-query";
import { Download } from "lucide-react";
import type { ReactNode } from "react";
import type { Kpi, KpiKey } from "@/api/cohorts";
import { exportPaths, type SCFView, scfViewQuery } from "@/api/onchain";
import { integrityQuery, sitesQuery } from "@/api/queries";
import type { IntegrityReport } from "@/api/schemas";
import { OwnCohortPanel } from "@/components/cohort";
import { CoverageNote } from "@/components/coverage";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { type AppealContext, AppealLink } from "@/components/integrity";
import { projectTabs } from "@/components/nav/nav-items";
import { NavTabs } from "@/components/nav/nav-tabs";
import { DayStrip } from "@/components/onchain";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { Fact, PageHeader, Panel } from "@/components/panel";
import { Pill, statusText } from "@/components/status";
import { Button } from "@/components/ui/button";
import { countOf, formatCount } from "@/lib/format";
import {
  formatChange,
  formatDay,
  formatDayRange,
  formatDuration,
  formatPct,
  formatPercentile,
  formatShare,
} from "@/lib/global";
import { weeklyActiveText } from "@/lib/onchain";
import { useProject } from "./project-layout";

const RANGE = {};

// "what scf sees": the figures the operator views show about this project,
// read through the operator views' own queries for the same range, so the
// numbers and ranges are the ones reviewers see. no score is added here.
export function SCFViewPage() {
  const { tenant, isOwner } = useProject();
  const q = useQuery(scfViewQuery(tenant.id, RANGE));
  if (q.isPending) return <Loading label="Loading what SCF sees" />;
  if (q.error) return <ErrorPanel error={q.error} onRetry={() => q.refetch()} />;
  const v = q.data;
  return (
    <div className="flex flex-col gap-5">
      <NavTabs label="On-chain" items={projectTabs(tenant.id, "onchain") ?? []} />
      <PageHeader
        title="What SCF sees"
        description={`The figures the SCF operator views show about this project for ${formatDayRange(v.from, v.to)}, from the same queries. Nothing here is hidden from you, and nothing is scored beyond what is shown.`}
        actions={
          <Button asChild variant="outline">
            <a href={exportPaths.overview(tenant.id, { from: v.from, to: v.to })} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        }
      />
      <OwnCohortPanel tenantId={tenant.id} record={v.cohort} isOwner={isOwner} />
      {!v.in_operator_views ? (
        <StateBlock kind="empty" title="Not listed in the operator views">
          This project is internal to the platform, so the SCF views do not list it.
        </StateBlock>
      ) : (
        <SCFRows v={v} />
      )}
    </div>
  );
}

// the integrity grade slot: it renders only when the control plane sends
// the field, and shows exactly what it sends. appeal, when given, is the
// project's own report for the same range, which the appeal link needs for
// its reasons and rule set version.
export function IntegritySlot({
  value,
  appeal,
}: {
  value: SCFView["integrity"];
  appeal?: { report: IntegrityReport; context: AppealContext };
}) {
  if (!value) return null;
  return (
    <Panel
      title="Integrity grade"
      description="The grade of your project's row in the operators' Global web view for this range."
      actions={appeal ? <AppealLink report={appeal.report} appeal={appeal.context} /> : undefined}
    >
      {value.grade === null ? (
        <p className="text-body text-fg-2">No site day of this range has a grade yet.</p>
      ) : (
        <div className="flex flex-col gap-1">
          <span className="text-page text-fg" data-testid="integrity-grade">
            {value.grade}
          </span>
          <span className="text-small text-fg-2">
            {countOf(value.graded_days, "graded site day")}: {value.days_a} A, {value.days_b} B,{" "}
            {value.days_c} C
          </span>
        </div>
      )}
    </Panel>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Fact label={label}>
      <span className="tabular-nums">{children}</span>
    </Fact>
  );
}

const KPI_ROWS: { key: KpiKey; label: string; kind: "count" | "rate" }[] = [
  { key: "visitors", label: "Visitors", kind: "count" },
  { key: "pageviews", label: "Page views", kind: "count" },
  { key: "requests", label: "API requests", kind: "count" },
  { key: "error_rate", label: "API error rate (5xx)", kind: "rate" },
  { key: "onchain_operations", label: "On-chain operations", kind: "count" },
];

function kpiValue(k: Kpi, kind: "count" | "rate", which: "value" | "previous"): string {
  const v = k[which];
  if (v === null) return "n/a";
  return kind === "rate" ? (formatShare(v) ?? "n/a") : formatCount(v);
}

// 19.2 and 19.3: the project's change against the previous period and its
// percentile in its cohort, exactly as the operator lists compute them
function CohortPlace({ v }: { v: SCFView }) {
  const c = v.comparison;
  if (!c) return null;
  const pc = v.percentile_cohort;
  const population =
    pc && (pc.program || pc.round)
      ? `the projects confirmed in ${[pc.program, pc.round].filter(Boolean).join(", ")}`
      : "every listed project, because no cohort is confirmed for yours";
  return (
    <Panel
      title="Change and place in the cohort"
      description={`Each metric over this range against the previous range of equal length, and the share of ${population} (${c.cohort_size} active or suspended) with a value at or below yours.`}
    >
      <table className="w-full text-left text-small tabular" aria-label="Key metrics">
        <thead>
          <tr className="text-caption text-fg-3">
            <th scope="col" className="py-1.5 pr-3 font-medium">
              Metric
            </th>
            <th scope="col" className="py-1.5 pr-3 text-right font-medium">
              This range
            </th>
            <th scope="col" className="py-1.5 pr-3 text-right font-medium">
              Previous range
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
          {KPI_ROWS.map((r) => {
            const k = c.kpis[r.key];
            return (
              <tr key={r.key} data-kpi={r.key} className="border-t border-line-subtle">
                <th scope="row" className="py-1.5 pr-3 font-normal text-fg-2">
                  {r.label}
                </th>
                <td className="py-1.5 pr-3 text-right font-mono text-fg">
                  {kpiValue(k, r.kind, "value")}
                </td>
                <td className="py-1.5 pr-3 text-right font-mono text-fg-2">
                  {kpiValue(k, r.kind, "previous")}
                </td>
                <td className="py-1.5 pr-3 text-right font-mono text-fg">
                  {formatChange(k.change, r.kind === "rate" ? "points" : "relative") ?? "n/a"}
                </td>
                <td className="py-1.5 text-right font-mono text-fg">
                  {formatPercentile(k.percentile) ?? "n/a"}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </Panel>
  );
}

const METRIC_TEXT: Record<string, string> = {
  visitors: "verified visitors",
  requests: "API requests",
  onchain_operations: "on-chain operations",
};

// 19.4 the operators' saved rules this project meets today, with the
// qualifying days that meet them
function RulesMet({ v }: { v: SCFView }) {
  return (
    <Panel
      title="Rules you meet"
      description="The traction rules SCF operators saved that your project meets today, over the complete calendar months each rule looks at."
    >
      {v.rules_met.length === 0 ? (
        <p className="text-body text-fg-2">Your project meets none of the saved rules today.</p>
      ) : (
        <ul className="flex flex-col gap-3" aria-label="Rules you meet">
          {v.rules_met.map(({ rule, match }) => (
            <li key={rule.id} data-rule={rule.id} className="flex flex-col gap-1.5">
              <span className="text-body font-medium text-fg">{rule.name}</span>
              <span className="text-small text-fg-2">
                {METRIC_TEXT[rule.metric] ?? rule.metric} of at least{" "}
                {formatCount(rule.min_per_day)} a day on {rule.min_days} or more days in each of the
                last {rule.months} {rule.months === 1 ? "month" : "months"}
                {rule.min_grade ? `, integrity grade ${rule.min_grade} or better` : ""}.
              </span>
              <ul className="flex flex-wrap gap-x-4 gap-y-1 text-caption text-fg-3">
                {match.months.map((m) => (
                  <li key={m.month}>
                    {m.month}: {countOf(m.qualifying_days, "qualifying day")}
                  </li>
                ))}
                <li>Integrity grade {match.integrity.grade ?? "not graded"}</li>
              </ul>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function SCFRows({ v }: { v: SCFView }) {
  const { tenant } = useProject();
  const web = v.web;
  const api = v.api;
  const chain = v.onchain;
  const award = v.comparison?.cohort.award_date ?? null;
  const marker = award ? { day: award, label: `Award date ${formatDay(award)}` } : null;
  // the project's own integrity report and sites for the same range, for
  // the appeal link's reasons, rule set version and site domains
  const report = useQuery({
    ...integrityQuery("tenant", tenant.id, { from: v.from, to: v.to }),
    enabled: Boolean(v.integrity?.grade),
  });
  const sites = useQuery({ ...sitesQuery(tenant.id), enabled: Boolean(v.integrity?.grade) });
  const appeal =
    report.data && sites.data
      ? {
          report: report.data,
          context: {
            slug: tenant.slug,
            siteDomains: Object.fromEntries(sites.data.map((s) => [s.id, s.domain])),
          },
        }
      : undefined;
  return (
    <>
      <IntegritySlot value={v.integrity} appeal={appeal} />
      <CohortPlace v={v} />
      <RulesMet v={v} />
      {web ? (
        <Panel
          title="Visits not measured"
          aria-label="Visits not measured"
          description="Visitors with Global Privacy Control are not measured, by design. This is the estimated share of them for this range."
        >
          <CoverageNote coverage={web.coverage} />
        </Panel>
      ) : null}
      <Panel title="Project" description="As the operator list shows it.">
        <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Row label="Status">{v.tenant ? statusText(v.tenant.status) : "n/a"}</Row>
          <Row label="Sites">{v.sites ? formatCount(v.sites.sites) : "n/a"}</Row>
          <Row label="Verified sites">{v.sites ? formatCount(v.sites.sites_verified) : "n/a"}</Row>
          <Row label="Sites with stellar.toml">
            {v.sites ? formatCount(v.sites.sites_with_stellar_toml) : "n/a"}
          </Row>
        </dl>
      </Panel>
      {web ? (
        <Panel title="Web" description="The project's row of the Global web view.">
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Row label="Page views">{formatCount(web.pageviews)}</Row>
            <Row label="Visitors">{formatCount(web.visitors)}</Row>
            <Row label="Visits">{formatCount(web.visits)}</Row>
            <Row label="Bounce rate">
              {web.bounce_rate === null ? "n/a" : formatShare(web.bounce_rate)}
            </Row>
            <Row label="Average visit">
              {web.avg_visit_duration === null ? "n/a" : formatDuration(web.avg_visit_duration)}
            </Row>
            <Row label="Quarantined share">
              {web.quarantine_share === null ? "n/a" : formatShare(web.quarantine_share)}
            </Row>
            <Row label="Gateway requests">{formatCount(web.ingest_requests)}</Row>
            <Row label="Rejected share">
              {web.rejected_share === null ? "n/a" : formatShare(web.rejected_share)}
            </Row>
          </dl>
          <div className="mt-4">
            {marker && v.days.includes(marker.day) ? (
              <p data-award className="mb-1.5 text-caption text-fg-3">
                The dashed line marks your award date, {formatDay(marker.day)}.
              </p>
            ) : null}
            <DayStrip
              marker={marker}
              label="Daily page views"
              points={v.web_series.map((p) => ({
                day: p.day,
                value: p.aggregated ? p.pageviews : null,
                missing: p.aggregated ? null : "not_read_yet",
              }))}
            />
          </div>
        </Panel>
      ) : null}
      {api ? (
        <Panel title="API" description="The project's row of the Global API view.">
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Row label="Requests">{formatCount(api.requests)}</Row>
            <Row label="4xx rate">{api.rate_4xx === null ? "n/a" : formatShare(api.rate_4xx)}</Row>
            <Row label="5xx rate">{api.rate_5xx === null ? "n/a" : formatShare(api.rate_5xx)}</Row>
            <Row label="p95">{formatPct(api.p95_seconds, api.p95_over_10s) ?? "n/a"}</Row>
            <Row label="Services">{formatCount(api.services)}</Row>
            <Row label="Routes">{formatCount(api.routes)}</Row>
          </dl>
        </Panel>
      ) : null}
      {chain ? (
        <Panel
          title="On-chain"
          description="The project's on-chain column."
          actions={
            v.link_two_way ? <Pill tone="success">stellar.toml linked both ways</Pill> : null
          }
        >
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Row label="Operations">{formatCount(chain.operations)}</Row>
            <Row label="Transactions">{formatCount(chain.transactions)}</Row>
            <Row label="Verified ids">{`${chain.ids_verified} of ${chain.ids}`}</Row>
            <Row label="Days without data">{formatCount(chain.days_missing)}</Row>
            <Row label="Weekly active accounts">
              {weeklyActiveText(chain.weekly_active_accounts)}
            </Row>
          </dl>
          <div className="mt-4">
            <DayStrip
              marker={marker}
              label="Daily operations"
              points={v.onchain_series.map((p) => ({
                day: p.day,
                value: p.operations,
                missing: null,
              }))}
            />
          </div>
        </Panel>
      ) : null}
      {v.contract_functions.length ? (
        <Panel
          title="Contract calls"
          description="Calls into each verified contract per function over the range, as the operator detail shows them."
        >
          <div className="flex flex-col gap-4">
            {v.contract_functions.map((c) => (
              <div key={c.strkey} className="flex flex-col gap-1.5" data-contract={c.strkey}>
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <code className="min-w-0 break-all text-small text-fg">{c.strkey}</code>
                  <span className="text-caption text-fg-3">{functionsDaysText(c.functions)}</span>
                </div>
                <FunctionsTable summary={c.functions} label={`Calls per function of ${c.strkey}`} />
              </div>
            ))}
          </div>
        </Panel>
      ) : null}
    </>
  );
}
