import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ExternalLink, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { integrityQuery, operatorTenantQuery } from "@/api/queries";
import type {
  IntegrityDay,
  IntegrityGrade,
  IntegrityRangeReason,
  IntegrityReport,
  IntegrityRow,
  IntegritySite,
  TenantRef,
} from "@/api/schemas";
import { CoverageNote } from "@/components/coverage";
import { Panel } from "@/components/panel";
import { ErrorState, LoadingState, Missing, Skeleton } from "@/components/states";
import { Pill, type Tone } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { appealUrl } from "@/lib/appeal";
import { formatCount, formatDate, formatDateRange, formatPercent, MISSING } from "@/lib/format";
import type { DayRange } from "@/lib/global";

// the integrity grade (contract 17): a colour never carries the grade alone,
// the letter is always written out
const TONE: Record<IntegrityGrade, Tone> = { A: "success", B: "warning", C: "critical" };

export function gradeText(grade: IntegrityGrade | null): string {
  return grade === null ? "Not graded" : `Grade ${grade}`;
}

export function GradeBadge({
  grade,
  className,
}: {
  grade: IntegrityGrade | null;
  className?: string;
}) {
  return (
    <Pill tone={grade === null ? "info" : TONE[grade]} className={className}>
      <span data-grade={grade ?? "none"}>{gradeText(grade)}</span>
    </Pill>
  );
}

// what a range grade means, in one sentence (docs/security/bot-and-inflation.md)
export function gradeMeaning(grade: IntegrityGrade | null): string {
  switch (grade) {
    case "A":
      return "Fewer than a quarter of the graded site days were lowered to B or C.";
    case "B":
      return "A quarter or more of the graded site days were lowered to B or C.";
    case "C":
      return "A quarter or more of the graded site days were lowered to C.";
    default:
      return "No site day had enough traffic for a grade. A day needs 50 accepted events.";
  }
}

// "10 graded site days: 7 A, 2 B, 1 C"; the counts behind a range grade
export function dayCountsText(s: IntegrityRow): string {
  if (s.graded_days === 0) return "No site day with enough traffic for a grade in this range.";
  const days = s.graded_days === 1 ? "graded site day" : "graded site days";
  return `${formatCount(s.graded_days)} ${days}: ${formatCount(s.days_a)} A, ${formatCount(s.days_b)} B, ${formatCount(s.days_c)} C.`;
}

// the evidence of one day as label and value pairs, platform observations
// first, then the page-reported page views
// a value that was not measured is the missing glyph; `reason` says why in
// its tooltip
export function evidenceFacts(
  d: IntegrityDay,
): { label: string; value: string; reason?: string }[] {
  const e = d.evidence;
  const classified =
    e.net.access + e.net.organisation + e.net.hosting + e.net.anonymiser + e.net.unknown;
  const shareOf = (n: number) => (classified > 0 ? formatPercent(n / classified) : MISSING);
  return [
    { label: "Accepted events", value: formatCount(e.events) },
    { label: "Access networks", value: shareOf(e.net.access) },
    { label: "Organisation networks", value: shareOf(e.net.organisation) },
    { label: "Hosting networks", value: shareOf(e.net.hosting) },
    { label: "Anonymisers", value: shareOf(e.net.anonymiser) },
    { label: "Distinct networks", value: formatCount(e.networks) },
    {
      label: "Quarantined",
      value: e.quarantine_share === null ? MISSING : formatPercent(e.quarantine_share),
    },
    {
      label: "Proof of work passed",
      value: e.proof_pass_rate === null ? MISSING : formatPercent(e.proof_pass_rate),
    },
    { label: "Visitors", value: formatCount(e.visitors) },
    {
      label: "Baseline visitors",
      value:
        e.baseline_visitors === null
          ? MISSING
          : `${formatCount(e.baseline_visitors)} (median of ${e.baseline_days} days)`,
      reason: "No history yet",
    },
    { label: "Page views (page-reported)", value: formatCount(e.pageviews) },
  ];
}

function RangeReasons({ reasons }: { reasons: IntegrityRangeReason[] }) {
  if (reasons.length === 0) {
    return <p className="text-small text-fg-2">No rule lowered the grade on any day.</p>;
  }
  return (
    <ul className="flex flex-col gap-1" aria-label="Reasons over the range">
      {reasons.map((r) => (
        <li key={r.code} data-reason={r.code} className="flex flex-wrap items-baseline gap-x-2">
          {r.level === "B" || r.level === "C" ? <GradeBadge grade={r.level} /> : null}
          <span className="text-small text-fg">{r.text}</span>
          <span className="text-caption text-fg-3">
            {formatCount(r.days)} {r.days === 1 ? "day" : "days"}
          </span>
        </li>
      ))}
    </ul>
  );
}

function SiteDays({ site, name }: { site: IntegritySite; name: string }) {
  // newest day first: the day in progress is what a reader checks first
  const days = [...site.days].reverse();
  return (
    <details
      className="group rounded-md border border-line bg-sunken"
      data-site={site.site_id}
      open={false}
    >
      <summary className="flex cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 focus-ring">
        <span className="min-w-0 truncate text-small font-medium text-fg">{name}</span>
        <GradeBadge grade={site.grade} />
        <span className="text-caption text-fg-3">{dayCountsText(site)}</span>
      </summary>
      <ol className="flex flex-col divide-y divide-line border-t border-line">
        {days.map((d) => (
          <li key={d.day} className="flex flex-col gap-2 px-3 py-2" data-day={d.day}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="text-small font-medium text-fg tabular">
                {formatDate(`${d.day}T00:00:00Z`)}
              </span>
              <GradeBadge grade={d.grade} />
              {d.closed ? null : <span className="text-caption text-fg-3">day in progress</span>}
            </div>
            {d.reasons.length > 0 ? (
              <ul className="flex flex-col gap-1">
                {d.reasons.map((r) => (
                  <li key={r.code} className="text-small text-fg-2" data-reason={r.code}>
                    {r.text}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-small text-fg-2">No rule lowered this day.</p>
            )}
            <dl className="grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-3">
              {evidenceFacts(d).map((f) => (
                <div key={f.label} className="flex min-w-0 flex-col">
                  <dt className="text-caption text-fg-3">{f.label}</dt>
                  <dd className="text-small text-fg tabular">
                    {f.value === MISSING ? (
                      <Missing reason={f.reason ?? "Not measured"} />
                    ) : (
                      f.value
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ol>
    </details>
  );
}

// the open data behind the network classes and their licences (db-ip's cc
// by 4.0 asks for a link wherever its results are used)
export function NetworkAttribution({ className }: { className?: string }) {
  const link =
    "rounded-sm underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:text-fg hover:decoration-fg focus-ring";
  return (
    <p className={cn("text-caption font-normal text-fg-3", className)}>
      Network data:{" "}
      <a href="https://db-ip.com" className={link} rel="noopener">
        IP to ASN data by DB-IP
      </a>
      , AS categories by{" "}
      <a href="https://github.com/ipverse/as-metadata" className={link} rel="noopener">
        ipverse
      </a>
      , Tor exit addresses by the{" "}
      <a href="https://metrics.torproject.org" className={link} rel="noopener">
        Tor Project
      </a>
      .
    </p>
  );
}

// what a project needs to appeal its grade: its slug and its sites'
// domains, the only names the issue carries
export type AppealContext = { slug: string; siteDomains: Record<string, string> };

// the link that opens a prefilled appeal issue; the grade itself stays
// automatic, operators read the appeal and decide
export function AppealLink({
  report,
  appeal,
  className,
}: {
  report: IntegrityReport;
  appeal: AppealContext;
  className?: string;
}) {
  // nothing to appeal without a grade, nor for the best one
  if (report.grade === null || report.grade === "A") return null;
  return (
    <a
      href={appealUrl(appeal.slug, report, appeal.siteDomains)}
      target="_blank"
      rel="noopener noreferrer"
      data-testid="integrity-appeal"
      className={cn(
        "inline-flex items-center gap-1 rounded-sm text-small text-fg-2 underline decoration-line-strong underline-offset-2 transition-colors duration-(--vs-duration-press) hover:text-fg hover:decoration-fg focus-ring",
        className,
      )}
    >
      Appeal this grade
      <ExternalLink aria-hidden className="size-3" />
    </a>
  );
}

// the whole report: the range grade, its reasons, and every site's days
export function IntegrityPanel({
  report,
  siteNames,
  intro,
  title = "Integrity",
  appeal,
  className,
}: {
  report: IntegrityReport;
  siteNames?: Record<string, string>;
  intro?: React.ReactNode;
  title?: string;
  appeal?: AppealContext;
  className?: string;
}) {
  return (
    <Panel
      title={
        <span className="inline-flex items-center gap-2">
          <ShieldCheck aria-hidden className="size-4 text-fg-3" />
          {title}
        </span>
      }
      description={`${formatDateRange(report.from, report.to)}, UTC. ${
        report.rule_versions.length > 1
          ? `Graded with rule sets ${report.rule_versions.join(" and ")}`
          : `Graded with rule set ${report.rule_versions[0] ?? report.rule_version}`
      }.`}
      actions={
        <span className="inline-flex flex-wrap items-center gap-3">
          <GradeBadge grade={report.grade} />
          {appeal ? <AppealLink report={report} appeal={appeal} /> : null}
        </span>
      }
      className={className}
      aria-label={title}
      data-integrity={report.tenant_id}
    >
      <div className="flex flex-col gap-3">
        {intro ? <div className="max-w-prose text-small text-fg-2">{intro}</div> : null}
        <p className="text-small text-fg-2">{dayCountsText(report)}</p>
        <RangeReasons reasons={report.reasons} />
        <CoverageNote coverage={report.coverage} />
        {report.sites.length > 0 ? (
          <div className="flex flex-col gap-2">
            {report.sites.map((s) => (
              <SiteDays
                key={s.site_id}
                site={s}
                name={siteNames?.[s.site_id] ?? `Site ${s.site_id.slice(0, 8)}`}
              />
            ))}
          </div>
        ) : null}
        <p className="max-w-prose text-caption text-fg-3">
          The grade rests on what the platform observes at its edge: the class of each visitor's
          network, the distinct networks, the proof of work and the quarantine band, checked against
          the site's own baseline. What the page itself reports can lower a grade, never raise it.
        </p>
        <NetworkAttribution />
      </div>
    </Panel>
  );
}

// loads and shows one project's report, for the project or for operators
export function IntegrityReportView({
  scope,
  tenantId,
  range,
  siteNames,
  intro,
  appeal,
  className,
}: {
  scope: "tenant" | "operator";
  tenantId: string;
  range: DayRange;
  siteNames?: Record<string, string>;
  intro?: React.ReactNode;
  appeal?: AppealContext;
  className?: string;
}) {
  const q = useQuery(integrityQuery(scope, tenantId, range));
  if (q.isPending)
    return (
      <LoadingState label="Loading the integrity report" className={className}>
        <Skeleton.Table rows={3} columns={3} />
      </LoadingState>
    );
  if (q.error)
    return (
      <ErrorState
        title="The integrity report did not load"
        error={q.error}
        onRetry={() => q.refetch()}
        className={className}
      />
    );
  return (
    <IntegrityPanel
      report={q.data}
      siteNames={siteNames}
      intro={intro}
      appeal={appeal}
      className={className}
    />
  );
}

// the operator's report with the site names of the operator detail
function OperatorReport({ tenantId, range }: { tenantId: string; range: DayRange }) {
  const detail = useQuery(operatorTenantQuery(tenantId));
  const names = Object.fromEntries((detail.data?.sites ?? []).map((s) => [s.id, s.domain]));
  return (
    <IntegrityReportView scope="operator" tenantId={tenantId} range={range} siteNames={names} />
  );
}

// a grade in a table cell that opens the reasons panel
export function IntegrityCell({
  tenant,
  integrity,
  range,
}: {
  tenant: TenantRef;
  integrity: IntegrityRow;
  range: DayRange;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-full focus-ring"
        aria-label={`${gradeText(integrity.grade)}, reasons for ${tenant.name}`}
        title={dayCountsText(integrity)}
        data-col="integrity"
      >
        <GradeBadge grade={integrity.grade} />
      </button>
      <Dialog
        open={open}
        onOpenChange={setOpen}
        title={`Integrity of ${tenant.name}`}
        className="max-w-content"
        footer={
          <Button variant="outline" onClick={() => setOpen(false)}>
            Close
          </Button>
        }
      >
        {open ? <OperatorReport tenantId={tenant.id} range={range} /> : null}
      </Dialog>
    </>
  );
}

// the compare page's row of grades, one per project, with the reasons
export function CompareIntegrity({
  projects,
}: {
  projects: { tenant: TenantRef; integrity: IntegrityRow & { reasons: IntegrityRangeReason[] } }[];
}) {
  return (
    <Panel
      title="Integrity"
      description="The grade over the range; a quarter of graded site days at a grade or worse sets it."
    >
      <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label="Integrity per project">
        {projects.map((p) => (
          <li key={p.tenant.id} className="flex min-w-0 flex-col gap-2" data-tenant={p.tenant.id}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate text-body font-medium text-fg">{p.tenant.name}</span>
              <GradeBadge grade={p.integrity.grade} />
            </div>
            <p className="text-caption text-fg-3">{dayCountsText(p.integrity)}</p>
            <RangeReasons reasons={p.integrity.reasons} />
          </li>
        ))}
      </ul>
      <NetworkAttribution className="mt-3" />
    </Panel>
  );
}
