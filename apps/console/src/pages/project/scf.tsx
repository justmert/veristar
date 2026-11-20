// scf review: what scf reviewers see about the project, read through the
// operator views' own queries for the same range (contract 16.3), and the
// signed reports that carry the same figures to anyone (contract 20). like a
// credit report: the answer first (the standing with the integrity seal and
// the proof for the application), then what reviewers check as five tiles,
// the evidence of the selected check under them, and the signed reports.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { Ban, Check, Download, ExternalLink, FileSignature, Minus } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { Link } from "react-router";
import type { KpiKey } from "@/api/cohorts";
import {
  type ChainId,
  exportPaths,
  type OnchainView,
  onchainQuery,
  type SCFView,
  scfViewQuery,
} from "@/api/onchain";
import { integrityQuery, sitesQuery } from "@/api/queries";
import { type ReportSummary, reportApi, reportKeysOf, reportsQuery } from "@/api/reports";
import type { IntegrityGrade, IntegrityReport, Site } from "@/api/schemas";
import { type TimeBucket, TimeChart } from "@/components/charts";
import { type CheckItem, ChecksList } from "@/components/checks-list";
import { OwnCohortPanel, percentileHiddenReason } from "@/components/cohort";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { CopyButton } from "@/components/copy-button";
import { coverageText } from "@/components/coverage";
import { type Column, DataTable } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { GradeRing } from "@/components/grade-ring";
import { AppealLink, SiteList } from "@/components/integrity";
import { LabeledFacts } from "@/components/labeled-facts";
import { FunctionsTable, functionsDaysText } from "@/components/onchain-functions";
import { PageHeader } from "@/components/page-header";
import { DateField } from "@/components/range-control";
import { GradeCalendar, ScorecardTile, type ScorecardVisual } from "@/components/scorecard-tile";
import type { StatChange } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill, type Tone } from "@/components/status";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { errorMessage } from "@/lib/errors";
import {
  countOf,
  formatChange,
  formatCohortPercentile,
  formatCompact,
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatDuration,
  formatLatency,
  formatPercent,
  formatPoints,
  formatTick,
  OPEN_BUCKET,
  utcDay,
} from "@/lib/format";
import { DAYS_WITHOUT_DATA, shortKey, weeklyActiveText } from "@/lib/onchain";
import {
  closedDays,
  downloadFile,
  lastFinalDay,
  MAX_REPORT_DAYS,
  validatePeriod,
} from "@/lib/reports";
import { siteSetupPath } from "@/lib/site-setup";
import { useProject } from "@/pages/project-layout";
import { PAGE_HEADERS, ScfSkeleton } from "./page-frame";

const RANGE = {};

type CheckKey = "ownership" | "integrity" | "web" | "api" | "onchain";

export function ScfReviewPage() {
  const { tenant, isOwner, readOnly } = useProject();
  const q = useQuery(scfViewQuery(tenant.id, RANGE));
  const v = q.data;
  const [signing, setSigning] = useState(false);

  const header = (
    <PageHeader
      title={PAGE_HEADERS.scf.title}
      items={v ? [`${formatDateRange(v.from, v.to)}, UTC`] : []}
      secondary={
        v && !readOnly ? (
          <Button asChild variant="outline">
            <a href={exportPaths.overview(tenant.id, { from: v.from, to: v.to })} download>
              <Download aria-hidden />
              Export CSV
            </a>
          </Button>
        ) : null
      }
    />
  );

  let body: ReactNode;
  if (q.isPending) body = <ScfSkeleton />;
  else if (q.error || !v)
    body = (
      <ErrorState
        title="What SCF reviewers see did not load"
        error={q.error}
        onRetry={() => q.refetch()}
      />
    );
  else if (!v.in_operator_views)
    body = (
      <>
        <EmptyState title="This project is internal to the platform, SCF reviewers do not see it" />
        <OwnCohortPanel tenantId={tenant.id} record={v.cohort} isOwner={isOwner} />
        <SignedReports open={signing} onOpenChange={setSigning} />
      </>
    );
  else
    body = (
      <>
        <Standing v={v} onSign={() => setSigning(true)} />
        <Review v={v} />
        <OwnCohortPanel tenantId={tenant.id} record={v.cohort} isOwner={isOwner} />
        <SignedReports open={signing} onOpenChange={setSigning} />
      </>
    );

  return (
    <div className="flex min-w-0 flex-col gap-6">
      {header}
      {body}
    </div>
  );
}

// ---- the standing: the seal, the project, its cohort and the rules it
// meets; on the right the proof for the application

const METRIC_TEXT: Record<string, string> = {
  visitors: "Verified visitors",
  requests: "API requests",
  onchain_operations: "On-chain operations",
};

function ruleText(v: SCFView["rules_met"][number]): string {
  const { rule, match } = v;
  const base = `${METRIC_TEXT[rule.metric] ?? rule.metric} at least ${formatCount(rule.min_per_day)} a day, on ${countOf(rule.min_days, "day")} in each of ${countOf(rule.months, "month")}${rule.min_grade ? `, grade ${rule.min_grade} or better` : ""}.`;
  const months = match.months
    .map(
      (m) =>
        `${formatTick(`${m.month}-01`, "month")}: ${countOf(m.qualifying_days, "qualifying day")}`,
    )
    .join(". ");
  return months ? `${base} ${months}.` : base;
}

function Standing({ v, onSign }: { v: SCFView; onSign: () => void }) {
  const { tenant, readOnly } = useProject();
  const reports = useQuery(reportsQuery(tenant.id));
  const c = v.comparison;
  const cohort = v.cohort.confirmed;
  const pc = v.percentile_cohort;
  const population =
    pc && (pc.program || pc.round)
      ? [pc.program, pc.round].filter(Boolean).join(", ")
      : "Every listed project";
  const facts = [
    cohort?.program ? { label: "Program", value: cohort.program } : null,
    cohort?.round ? { label: "Round", value: cohort.round } : null,
    cohort?.category ? { label: "Category", value: cohort.category } : null,
    c
      ? {
          label: "Cohort",
          value: (
            <span title={population} data-testid="cohort-population">
              {countOf(c.cohort_size, "project")}
            </span>
          ),
        }
      : null,
  ].filter((f) => f !== null);
  const items = reports.data?.items ?? [];
  const latest = items.find((r) => r.revoked_at === null) ?? null;
  const graded = v.integrity && v.integrity.graded_days > 0 ? v.integrity : null;

  return (
    <section
      aria-label="Standing"
      data-testid="verdict"
      className="grid min-w-0 items-center gap-6 rounded-2xl border border-line bg-raised p-6 md:grid-cols-[auto_minmax(0,1fr)] xl:grid-cols-[auto_minmax(0,1fr)_minmax(0,20rem)]"
    >
      <GradeRing
        row={graded}
        size="xl"
        data-testid="standing-grade"
        className="justify-self-center"
      />
      <div className="flex min-w-0 flex-col gap-3">
        <h2 className="text-[30px] leading-9 font-semibold tracking-[-0.02em] break-words text-fg">
          {v.tenant?.name ?? tenant.name}
        </h2>
        {facts.length ? <LabeledFacts items={facts} /> : null}
        <ul aria-label="Cohort rules met" className="flex flex-wrap gap-2">
          {v.rules_met.length ? (
            v.rules_met.map((m) => (
              <li key={m.rule.id} data-rule-met={m.rule.id} data-rule={m.rule.id}>
                <Tooltip content={ruleText(m)}>
                  {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the rule's definition opens on focus too */}
                  <span tabIndex={0} className="inline-flex rounded-full focus-ring">
                    <Pill tone="neutral">
                      <Check aria-hidden className="size-3 text-success" />
                      Meets {m.rule.name}
                    </Pill>
                    <span className="sr-only">. {ruleText(m)}</span>
                  </span>
                </Tooltip>
              </li>
            ))
          ) : (
            <li>
              <Pill tone="neutral">
                <Minus aria-hidden className="size-3" />
                No cohort rule met yet
              </Pill>
            </li>
          )}
        </ul>
      </div>
      <section
        aria-labelledby="proof-h"
        className="flex min-w-0 flex-col gap-2 rounded-xl border border-line bg-sunken p-5 md:col-span-2 xl:col-span-1"
      >
        <h3 id="proof-h" className="text-caption font-medium tracking-[0.08em] text-fg-3 uppercase">
          Proof for your application
        </h3>
        {reports.isPending ? (
          <Skeleton.Table rows={1} columns={1} />
        ) : latest ? (
          <>
            <span className="text-body font-semibold text-fg" data-latest-report={latest.id}>
              {formatDateRange(latest.from, latest.to)}
            </span>
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-small text-fg-2">
              <Pill tone="success">Active</Pill>
              <span className="tabular">{countOf(latest.view_count, "view")}</span>
              <a
                href="#signed-reports-h"
                className="rounded-sm text-fg underline underline-offset-[3px] focus-ring"
              >
                {countOf(items.length, "report")}
              </a>
            </span>
          </>
        ) : (
          <span className="text-body font-semibold text-fg" data-reports="none">
            No signed report yet
          </span>
        )}
        <span className="mt-2 flex flex-wrap gap-2 empty:hidden">
          {readOnly ? null : (
            <Button onClick={onSign}>
              <FileSignature aria-hidden />
              Sign a report
            </Button>
          )}
          {latest ? (
            <CopyButton
              text={latest.url}
              label="Copy link"
              accessibleLabel={`Copy link of the report for ${formatDateRange(latest.from, latest.to)}`}
            />
          ) : null}
        </span>
      </section>
    </section>
  );
}

// ---- what reviewers check: five tiles and the evidence of the selected one

function changeOf(r: number | null, period = "previous range"): StatChange | null {
  if (r === null) return null;
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: formatChange(r),
    direction,
    good: direction === "flat" ? null : direction === "up",
    period,
  };
}

type Proof = CheckItem & { done: boolean; label: string };

// every proof of ownership, with its date or the action that adds it. a
// reader gets the proofs without the actions
function proofsOf(
  tenantId: string,
  sites: Site[],
  chain: OnchainView | undefined,
  readOnly: boolean,
): Proof[] {
  const out: Proof[] = [];
  const live = sites.filter((s) => s.status !== "removed");
  const when = (t: string | null) =>
    t ? (utcDay(new Date(t)) === utcDay() ? "today" : formatDate(t)) : null;
  const quiet = (t: string | null) =>
    t ? <span className="text-caption text-fg-3">{when(t)}</span> : undefined;
  const action = (to: string, label: string) =>
    readOnly ? undefined : (
      <Button asChild size="sm" variant="outline">
        <Link to={to}>{label}</Link>
      </Button>
    );
  const mono = (s: string) => <span className="font-mono text-caption text-fg-3">{s}</span>;
  for (const s of live) {
    const ok = s.verification === "verified";
    out.push({
      key: `domain-${s.id}`,
      label: ok ? "Domain verified" : "Domain not verified",
      done: ok,
      status: ok ? "passed" : "warning",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {ok ? "Domain verified" : "Domain not verified"}
          {mono(s.domain)}
        </span>
      ),
      aside: ok ? quiet(s.verified_at) : action(siteSetupPath(s), "Verify the domain"),
    });
  }
  const first = live[0];
  if (first) {
    const found = live.find((s) => s.stellar_toml?.status === "found");
    out.push({
      key: "toml",
      label: found ? "stellar.toml found" : "stellar.toml not found",
      done: !!found,
      status: found ? "passed" : "missing",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {found ? "stellar.toml found" : "stellar.toml not found"}
          {mono((found ?? first).domain)}
        </span>
      ),
      aside: found
        ? quiet(found.stellar_toml_checked_at)
        : action(siteSetupPath(first), "How to publish it"),
    });
  }
  const ids = chain?.ids ?? [];
  const accounts = ids.filter((i) => i.kind === "account");
  const contracts = ids.filter((i) => i.kind === "contract");
  const onchain = `/projects/${tenantId}/onchain`;
  const proven = (i: ChainId): Proof => {
    const ok = i.verification.status === "verified";
    const what = i.kind === "account" ? "Account" : "Contract";
    return {
      key: `proof-${i.id}`,
      label: ok ? `${what} proven` : `${what} not proven`,
      done: ok,
      status: ok ? "passed" : "warning",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {ok ? `${what} proven` : `${what} not proven`}
          <span title={i.strkey}>{mono(shortKey(i.strkey))}</span>
        </span>
      ),
      aside: ok ? quiet(i.verification.verified_at) : action(onchain, "Prove control"),
    };
  };
  for (const a of accounts) {
    const home = a.read.home_domain;
    out.push({
      key: `home-${a.id}`,
      label: home ? "Home domain set" : "Home domain not set",
      done: !!home,
      status: home ? "passed" : "missing",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {home ? "Home domain set" : "Home domain not set"}
          <span title={a.strkey}>{mono(home ?? shortKey(a.strkey))}</span>
        </span>
      ),
    });
    out.push(proven(a));
  }
  if (accounts.length === 0)
    out.push({
      key: "account",
      label: "No account added",
      done: false,
      status: "missing",
      children: "No account added",
      aside: action(onchain, "Add an account"),
    });
  for (const c of contracts) out.push(proven(c));
  if (contracts.length === 0)
    out.push({
      key: "contract",
      label: "No contract added",
      done: false,
      status: "missing",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          No contract added
          {mono("optional")}
        </span>
      ),
      aside: action(onchain, "Add a contract"),
    });
  return out;
}

// the grade of the project on each day: the lowest grade of its sites
function dailyGrades(
  report: IntegrityReport | undefined,
  days: string[],
): (IntegrityGrade | null)[] {
  const rank: Record<IntegrityGrade, number> = { A: 0, B: 1, C: 2 };
  return days.map((d) => {
    let worst: IntegrityGrade | null = null;
    for (const s of report?.sites ?? []) {
      const g = s.days.find((x) => x.day === d)?.grade ?? null;
      if (g && (worst === null || rank[g] > rank[worst])) worst = g;
    }
    return worst;
  });
}

const kpiPlace = (v: SCFView, k: KpiKey): string | null => {
  const c = v.comparison;
  if (!c || percentileHiddenReason(c.cohort_size)) return null;
  const p = c.kpis[k].percentile;
  return p === null ? null : `${formatCohortPercentile(p)} in cohort`;
};

function Review({ v }: { v: SCFView }) {
  const { tenant, readOnly } = useProject();
  const sites = useQuery(sitesQuery(tenant.id));
  const chain = useQuery(onchainQuery(tenant.id, { from: v.from, to: v.to }));
  const report = useQuery({
    ...integrityQuery("tenant", tenant.id, { from: v.from, to: v.to }),
    enabled: (v.sites?.sites ?? 0) > 0,
  });
  const proofs =
    sites.data && chain.data ? proofsOf(tenant.id, sites.data, chain.data, readOnly) : null;
  const proofsDone = proofs?.filter((p) => p.done).length ?? 0;
  const g = v.integrity && v.integrity.graded_days > 0 ? v.integrity : null;
  const kpi = v.comparison?.kpis;
  const webOk = (v.web?.visitors ?? 0) > 0;
  const apiOk = (v.api?.requests ?? 0) > 0;
  const chainOk = (v.onchain?.operations ?? null) !== null && (v.onchain?.ids_verified ?? 0) > 0;
  const today = utcDay();
  const running = v.to >= today;

  // the page opens on the weakest check, so it leads with what to fix
  const weakest: CheckKey =
    proofs && proofsDone < proofs.length
      ? "ownership"
      : g?.grade && g.grade !== "A"
        ? "integrity"
        : !webOk
          ? "web"
          : !apiOk
            ? "api"
            : !chainOk
              ? "onchain"
              : "integrity";
  const [picked, setPicked] = useState<CheckKey | null>(null);
  const open = picked ?? weakest;

  const gradeTone = (x: IntegrityGrade | null | undefined): Tone =>
    x === "A" ? "success" : x === "B" ? "warning" : x === "C" ? "critical" : "neutral";
  const spark = (values: number[], series: 1 | 2 | 3): ScorecardVisual =>
    values.some((x) => x > 0) ? { kind: "spark", values, series, running } : { kind: "none" };
  const labels = v.days.map((d) => formatDate(d));

  const tiles: {
    key: CheckKey;
    name: string;
    tone: Tone;
    value: ReactNode;
    sub: ReactNode;
    change?: StatChange | null;
    visual: ScorecardVisual;
  }[] = [
    {
      key: "ownership",
      name: "Ownership",
      tone: !proofs ? "neutral" : proofsDone === proofs.length ? "success" : "warning",
      value: proofs ? `${proofsDone} of ${proofs.length}` : "–",
      sub: "proofs in place",
      visual: proofs
        ? {
            kind: "proofs",
            total: proofs.length,
            done: proofsDone,
            labels: proofs.map((p) => p.label),
          }
        : { kind: "none" },
    },
    {
      key: "integrity",
      name: "Traffic integrity",
      tone: gradeTone(g?.grade),
      value: g?.grade ? `Grade ${g.grade}` : "–",
      sub: g ? countOf(g.graded_days, "graded site day") : "not graded",
      visual:
        report.data && g
          ? { kind: "calendar", days: dailyGrades(report.data, v.days), labels }
          : { kind: "none" },
    },
    {
      key: "web",
      name: "Web",
      tone: webOk ? "success" : "neutral",
      value: webOk ? formatCompactCount(v.web?.visitors ?? 0) : "–",
      change: webOk ? changeOf(kpi?.visitors.change ?? null) : null,
      sub: (
        <>
          <span>verified visitors</span>
          {kpiPlace(v, "visitors") ? <span className="ml-2">{kpiPlace(v, "visitors")}</span> : null}
        </>
      ),
      visual: spark(
        v.web_series.map((p) => p.visitors ?? 0),
        1,
      ),
    },
    {
      key: "api",
      name: "API",
      tone: apiOk ? "success" : "neutral",
      value: apiOk ? formatCompactCount(v.api?.requests ?? 0) : "–",
      change: apiOk ? changeOf(kpi?.requests.change ?? null) : null,
      sub: (
        <>
          <span>requests</span>
          {kpiPlace(v, "requests") ? <span className="ml-2">{kpiPlace(v, "requests")}</span> : null}
        </>
      ),
      visual: spark(
        v.api_series.map((p) => p.requests),
        3,
      ),
    },
    {
      key: "onchain",
      name: "On-chain",
      tone: chainOk
        ? (kpi?.onchain_operations.change ?? 0) < 0
          ? "warning"
          : "success"
        : "neutral",
      value: chainOk ? formatCompactCount(v.onchain?.operations ?? 0) : "–",
      change: chainOk ? changeOf(kpi?.onchain_operations.change ?? null) : null,
      sub: chainOk ? (
        <>
          <span>operations</span>
          {kpiPlace(v, "onchain_operations") ? (
            <span className="ml-2">{kpiPlace(v, "onchain_operations")}</span>
          ) : null}
        </>
      ) : (v.onchain?.ids_verified ?? 0) > 0 ? (
        "no ledger read yet"
      ) : (
        "no proven ID yet"
      ),
      visual: chainOk
        ? spark(
            v.onchain_series.map((p) => p.operations ?? 0),
            2,
          )
        : { kind: "none" },
    },
  ];

  return (
    <section aria-labelledby="checks-h" className="flex min-w-0 flex-col gap-4">
      <h2 id="checks-h" className="text-body font-semibold text-fg">
        What reviewers check
      </h2>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        {tiles.map((t) => (
          <ScorecardTile
            key={t.key}
            data-check={t.key}
            name={t.name}
            tone={t.tone}
            value={t.value}
            sub={t.sub}
            change={t.change}
            visual={t.visual}
            selected={open === t.key}
            onSelect={() => setPicked(t.key)}
          />
        ))}
      </div>
      <Evidence
        open={open}
        tone={tiles.find((t) => t.key === open)?.tone ?? "neutral"}
        v={v}
        proofs={proofs}
        proofsState={sites.error || chain.error ? "error" : proofs ? "data" : "loading"}
        proofsError={sites.error ?? chain.error}
        onRetryProofs={() => {
          sites.refetch();
          chain.refetch();
        }}
        report={report}
        sites={sites.data ?? []}
      />
    </section>
  );
}

function formatCompactCount(n: number): string {
  return n >= 100_000 ? formatCompact(n) : formatCount(n);
}

// ---- the evidence of one check

const EVIDENCE_TITLE: Record<CheckKey, string> = {
  ownership: "Ownership",
  integrity: "Traffic integrity",
  web: "Web",
  api: "API",
  onchain: "On-chain",
};

const TONE_DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  info: "bg-info",
  neutral: "bg-fg-3",
};

function Evidence({
  open,
  tone,
  v,
  proofs,
  proofsState,
  proofsError,
  onRetryProofs,
  report,
  sites,
}: {
  open: CheckKey;
  tone: Tone;
  v: SCFView;
  proofs: Proof[] | null;
  proofsState: "loading" | "error" | "data";
  proofsError: unknown;
  onRetryProofs: () => void;
  report: { data?: IntegrityReport; isPending: boolean; error: unknown; refetch: () => unknown };
  sites: Site[];
}) {
  const { tenant, readOnly } = useProject();
  const left = proofs ? proofs.filter((p) => !p.done).length : 0;
  const note =
    open === "ownership" && proofs
      ? left
        ? `${countOf(left, "step")} left`
        : "every proof in place"
      : open === "integrity"
        ? "grade of every site, every day"
        : null;
  const names = Object.fromEntries(sites.map((s) => [s.id, s.domain]));
  const actions =
    open === "integrity" && report.data && !readOnly ? (
      <AppealLink report={report.data} appeal={{ slug: tenant.slug, siteDomains: names }} />
    ) : open === "web" || open === "api" || open === "onchain" ? (
      <Link
        to={`/projects/${tenant.id}/${open}`}
        className="rounded-sm text-small text-fg-2 hover:text-fg focus-ring"
      >
        Open {EVIDENCE_TITLE[open]}
      </Link>
    ) : null;
  return (
    <section
      aria-labelledby="evidence-h"
      data-evidence={open}
      className="flex min-w-0 flex-col gap-4 rounded-2xl border border-line bg-raised px-6 py-5"
    >
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h3 id="evidence-h" className="flex items-center gap-2.5 text-[17px] font-semibold text-fg">
          <span aria-hidden className={cn("size-2 rounded-full", TONE_DOT[tone])} />
          {EVIDENCE_TITLE[open]}
          {note ? <span className="text-small font-normal text-fg-3">{note}</span> : null}
        </h3>
        {actions}
      </div>
      {open === "ownership" ? (
        proofsState === "error" ? (
          <ErrorState
            compact
            title="The proofs did not load"
            error={proofsError}
            onRetry={onRetryProofs}
          />
        ) : proofs ? (
          <ChecksList label="Proofs of ownership" items={proofs} />
        ) : (
          <Skeleton.Table rows={4} columns={2} />
        )
      ) : open === "integrity" ? (
        <IntegrityEvidence v={v} report={report} names={names} />
      ) : open === "web" ? (
        <WebEvidence v={v} />
      ) : open === "api" ? (
        <ApiEvidence v={v} />
      ) : (
        <ChainEvidence v={v} />
      )}
    </section>
  );
}

const GRADE_TONE: Record<IntegrityGrade, Tone> = { A: "success", B: "warning", C: "critical" };

function IntegrityEvidence({
  v,
  report,
  names,
}: {
  v: SCFView;
  report: { data?: IntegrityReport; isPending: boolean; error: unknown; refetch: () => unknown };
  names: Record<string, string>;
}) {
  const [openSite, setOpenSite] = useState<string | null>(null);
  if ((v.sites?.sites ?? 0) === 0)
    return <EmptyState compact title="No site yet, so no traffic to grade" />;
  if (report.isPending)
    return (
      <LoadingState label="Loading the integrity report">
        <Skeleton.Table rows={3} columns={3} />
      </LoadingState>
    );
  if (report.error || !report.data)
    return (
      <ErrorState
        compact
        title="The integrity report did not load"
        error={report.error}
        onRetry={() => report.refetch()}
      />
    );
  const r = report.data;
  const reasons = r.reasons.filter((x) => x.level !== "none");
  const ticks = v.days.filter((_, i) => i % 7 === 0);
  return (
    <div className="flex min-w-0 flex-col gap-5" data-integrity={r.tenant_id}>
      <div className="flex min-w-0 flex-col gap-2 overflow-x-auto">
        <div className="grid min-w-[40rem] grid-cols-[14rem_minmax(0,1fr)] gap-x-4 gap-y-2">
          <span />
          <span aria-hidden className="flex justify-between text-caption text-fg-3">
            {ticks.map((d) => (
              <span key={d}>{formatTick(d, "day")}</span>
            ))}
          </span>
          {r.sites.map((s) => {
            const grades = v.days.map((d) => s.days.find((x) => x.day === d)?.grade ?? null);
            const name = names[s.site_id] ?? `Site ${s.site_id.slice(0, 8)}`;
            return (
              <div key={s.site_id} className="contents" data-site={s.site_id}>
                <button
                  type="button"
                  aria-expanded={openSite === s.site_id}
                  onClick={() => setOpenSite(openSite === s.site_id ? null : s.site_id)}
                  className="flex min-w-0 items-center gap-2 rounded-sm text-left focus-ring"
                >
                  <Pill tone={s.grade ? GRADE_TONE[s.grade] : "neutral"}>{s.grade ?? "–"}</Pill>
                  <span className="truncate font-mono text-small text-fg">{name}</span>
                  <span className="sr-only">, show the days</span>
                </button>
                <GradeCalendar
                  size="lg"
                  days={grades}
                  labels={v.days.map(
                    (d, i) => `${formatDate(d)}: ${grades[i] ?? "too little traffic"}`,
                  )}
                />
              </div>
            );
          })}
        </div>
        <span
          aria-hidden
          className="flex flex-wrap items-center gap-4 pl-[15rem] text-caption text-fg-3"
        >
          <Legend cls="bg-success/55" label="A" />
          <Legend cls="bg-warning" label="B" />
          <Legend cls="bg-critical" label="C" />
          <Legend cls="border border-line-2" label="Too little traffic to grade" />
        </span>
      </div>
      {openSite ? (
        <SiteList sites={r.sites.filter((s) => s.site_id === openSite)} siteNames={names} />
      ) : null}
      <ul aria-label="What lowered the grade" className="grid gap-3 md:grid-cols-3">
        {reasons.map((x) => (
          <li
            key={x.code}
            data-reason={x.code}
            className="flex min-w-0 flex-col gap-2 rounded-xl border border-line px-4 py-3.5"
          >
            <span
              className={cn(
                "flex items-center gap-2 text-caption font-semibold tracking-[0.08em] uppercase",
                x.level === "C" ? "text-critical" : "text-warning",
              )}
            >
              <span
                aria-hidden
                className={cn(
                  "size-1.5 rounded-full",
                  x.level === "C" ? "bg-critical" : "bg-warning",
                )}
              />
              Lowered the grade
            </span>
            <span className="text-body text-fg">{x.text}</span>
            <span className="text-small text-fg-3 tabular">{countOf(x.days, "day")}</span>
          </li>
        ))}
        <li
          data-testid="coverage"
          className="flex min-w-0 flex-col gap-2 rounded-xl border border-line px-4 py-3.5"
        >
          <span className="text-caption font-semibold tracking-[0.08em] text-fg-3 uppercase">
            Not measured
          </span>
          {r.coverage.unmeasured_share !== null ? (
            <span className="flex items-baseline gap-2">
              <span className="text-[24px] leading-7 font-semibold text-fg tabular">
                {formatPercent(r.coverage.unmeasured_share)}
              </span>
              <span className="text-small text-fg-2">of visits</span>
            </span>
          ) : (
            <span className="text-small text-fg-2">{coverageText(r.coverage)}</span>
          )}
          {/* the counts behind the estimate, as on the operator's list */}
          {r.coverage.script_requests > 0 ? (
            <span className="text-small text-fg-3 tabular">
              {formatCount(r.coverage.gpc_requests)} of {formatCount(r.coverage.script_requests)}{" "}
              loads with GPC
            </span>
          ) : null}
        </li>
      </ul>
    </div>
  );
}

function Legend({ cls, label }: { cls: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      <i className={cn("block size-3 rounded-[3px]", cls)} />
      {label}
    </span>
  );
}

// ---- the evidence of web, api and on-chain: the daily chart and every
// figure of that source that the tile does not show

const DEFINED =
  "rounded-sm underline decoration-line-strong decoration-dotted underline-offset-2 focus-ring";

type Fig = { label: string; value: ReactNode; definition?: string; name?: string } | null;

function FigureList({ items }: { items: Fig[] }) {
  const shown = items.filter((f): f is NonNullable<Fig> => f !== null);
  if (shown.length === 0) return null;
  return (
    <dl className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
      {shown.map((f) => (
        <div key={f.label} className="flex min-w-0 flex-col gap-1" data-figure={f.name}>
          <dt className="text-caption text-fg-3">
            {f.definition ? (
              <Tooltip content={f.definition}>
                {/* biome-ignore lint/a11y/noNoninteractiveTabindex: the definition opens on focus too */}
                <span tabIndex={0} className={DEFINED}>
                  {f.label}
                  <span className="sr-only">. {f.definition}</span>
                </span>
              </Tooltip>
            ) : (
              f.label
            )}
          </dt>
          <dd className="min-w-0 text-body text-fg tabular">{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

// one definition per label: "Distinct routes" is not the route rows of the
// api page, which list each method of a route
const DISTINCT_ROUTES =
  "Distinct service and route pairs with application requests. A route with two methods counts once. A route that only health checks reached does not count.";

const fig = (label: string, value: ReactNode | null, more?: Partial<NonNullable<Fig>>): Fig =>
  value === null || value === undefined ? null : { label, value, ...more };
const pct = (v: number | null) => (v === null ? null : formatPercent(v));
const cnt = (v: number | null) => (v === null ? null : formatCount(v));

// the previous range and the cohort place of a key metric, as the operator
// lists compute them (contract 19.2 and 19.3)
function kpiFigs(v: SCFView, key: KpiKey, label: string, rate = false): Fig[] {
  const k = v.comparison?.kpis[key];
  if (!k) return [];
  const hidden = v.comparison ? percentileHiddenReason(v.comparison.cohort_size) : null;
  return [
    fig(
      `${label}, previous range`,
      k.previous === null ? null : rate ? formatPercent(k.previous) : formatCount(k.previous),
      {
        name: `previous-${key}`,
      },
    ),
    rate && k.change !== null ? fig(`${label}, change`, formatPoints(k.change)) : null,
    hidden || k.percentile === null
      ? null
      : fig(`${label}, percentile`, formatCohortPercentile(k.percentile), {
          definition: "The share of the cohort with a value at or below yours.",
          name: `percentile-${key}`,
        }),
  ];
}

function marker(v: SCFView) {
  const award = v.comparison?.cohort.award_date ?? null;
  return award && v.days.includes(award)
    ? { at: `${award}T00:00:00Z`, label: `Award date ${formatDate(award)}` }
    : null;
}

function dayBuckets(v: SCFView): TimeBucket[] {
  const today = utcDay();
  return v.days.map((d) => ({
    at: `${d}T00:00:00Z`,
    tick: formatTick(d, "day"),
    label: formatDate(d),
    partial: d === today,
  }));
}

function WebEvidence({ v }: { v: SCFView }) {
  const web = v.web;
  if (!web || !v.has_web_data) return <EmptyState compact title="No web data in this range" />;
  const days = v.web_series.some((p) => p.aggregated && (p.pageviews ?? 0) > 0);
  return (
    <div className="flex min-w-0 flex-col gap-5">
      {days ? (
        <TimeChart
          title="Verified visitors"
          description="per day, UTC"
          headingLevel={3}
          buckets={dayBuckets(v)}
          series={[
            {
              key: "visitors",
              label: "Verified visitors",
              tone: "series-1",
              values: v.web_series.map((p) => (p.aggregated ? p.visitors : null)),
            },
          ]}
          marker={marker(v)}
          axisFormat={formatCompact}
          state="data"
        />
      ) : null}
      <FigureList
        items={[
          fig("Page views", formatCount(web.pageviews)),
          fig("Visits", formatCount(web.visits)),
          fig("Bounce rate", pct(web.bounce_rate)),
          fig(
            "Average visit",
            web.avg_visit_duration === null ? null : formatDuration(web.avg_visit_duration),
          ),
          fig("Quarantined share", pct(web.quarantine_share), {
            definition: "The share of page views held apart because they look automated.",
          }),
          fig("Gateway requests", formatCount(web.ingest_requests), {
            definition: "Events that reached the Veristar edge, counted or not.",
          }),
          fig("Refused share", pct(web.rejected_share), {
            definition: "The share of gateway requests the edge refused.",
          }),
          v.sites ? fig("Sites", formatCount(v.sites.sites)) : null,
          v.sites ? fig("Verified sites", formatCount(v.sites.sites_verified)) : null,
          v.sites
            ? fig("Sites with stellar.toml", formatCount(v.sites.sites_with_stellar_toml))
            : null,
          ...kpiFigs(v, "visitors", "Visitors"),
          ...kpiFigs(v, "pageviews", "Page views"),
        ]}
      />
    </div>
  );
}

function ApiEvidence({ v }: { v: SCFView }) {
  const api = v.api;
  if (!api || api.requests === 0)
    return <EmptyState compact title="No API requests in this range" />;
  return (
    <div className="flex min-w-0 flex-col gap-5">
      <TimeChart
        title="API requests"
        description="per day, UTC"
        headingLevel={3}
        buckets={dayBuckets(v)}
        series={[
          {
            key: "requests",
            label: "API requests",
            tone: "series-3",
            values: v.api_series.map((p) => p.requests),
          },
        ]}
        marker={marker(v)}
        axisFormat={formatCompact}
        state="data"
      />
      <FigureList
        items={[
          fig("4xx rate", pct(api.rate_4xx)),
          fig("5xx rate", pct(api.rate_5xx)),
          fig(
            "p95 latency",
            api.p95_over_10s
              ? OPEN_BUCKET
              : api.p95_seconds === null
                ? null
                : formatLatency(api.p95_seconds),
          ),
          fig("Services", formatCount(api.services)),
          fig("Distinct routes", formatCount(api.routes), {
            definition: DISTINCT_ROUTES,
            name: "routes",
          }),
          ...kpiFigs(v, "requests", "Requests"),
          ...kpiFigs(v, "error_rate", "5xx rate", true),
        ]}
      />
    </div>
  );
}

function ChainEvidence({ v }: { v: SCFView }) {
  const { tenant, readOnly } = useProject();
  const chain = v.onchain;
  if (!chain || chain.ids === 0)
    return (
      <EmptyState
        compact
        title="No Stellar account or contract yet"
        action={
          readOnly ? null : (
            <Button asChild variant="outline">
              <Link to={`/projects/${tenant.id}/onchain`}>Add an account</Link>
            </Button>
          )
        }
      />
    );
  const measured = chain.operations !== null;
  return (
    <div className="flex min-w-0 flex-col gap-5">
      {measured ? (
        <TimeChart
          title="Operations"
          description="per day, UTC"
          headingLevel={3}
          buckets={dayBuckets(v)}
          series={[
            {
              key: "operations",
              label: "Operations",
              tone: "series-2",
              values: v.onchain_series.map((p) => p.operations),
            },
          ]}
          marker={marker(v)}
          axisFormat={formatCompact}
          state={v.onchain_series.some((p) => (p.operations ?? 0) > 0) ? "data" : "empty"}
          emptyTitle="No operations in this range"
        />
      ) : null}
      <FigureList
        items={[
          fig("Transactions", cnt(chain.transactions)),
          fig("Verified IDs", `${formatCount(chain.ids_verified)} of ${formatCount(chain.ids)}`),
          fig("Days without data", cnt(chain.days_missing), {
            definition: DAYS_WITHOUT_DATA,
            name: "days-missing",
          }),
          chain.weekly_active_accounts.average === null
            ? null
            : fig("Weekly active accounts", weeklyActiveText(chain.weekly_active_accounts)),
          fig("stellar.toml link", v.link_two_way ? "Linked both ways" : "Not linked both ways"),
          ...kpiFigs(v, "onchain_operations", "Operations"),
        ]}
      />
      {v.contract_functions.length ? (
        <div className="flex min-w-0 flex-col gap-4">
          <h4 className="text-body font-medium text-fg">Calls per function</h4>
          {v.contract_functions.map((c) => (
            <div key={c.strkey} className="flex min-w-0 flex-col gap-2" data-contract={c.strkey}>
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <code className="font-mono text-small text-fg" title={c.strkey}>
                  {shortKey(c.strkey)}
                </code>
                <span className="text-caption text-fg-3">{functionsDaysText(c.functions)}</span>
              </div>
              <FunctionsTable summary={c.functions} label={`Calls per function of ${c.strkey}`} />
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

// ---- signed reports of the same figures: a table, and a dialog that signs
// a new one and hands over its link

export function SignedReports({
  open,
  onOpenChange,
}: {
  // the standing's button opens the dialog; alone, the section has its own
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const { tenant, readOnly } = useProject();
  const list = useQuery(reportsQuery(tenant.id));
  const queryClient = useQueryClient();
  const [ownOpen, setOwnOpen] = useState(false);
  const controlled = open !== undefined;
  const signing = controlled ? open : ownOpen;
  const setSigning = (o: boolean) => (controlled ? onOpenChange?.(o) : setOwnOpen(o));
  const [revoking, setRevoking] = useState<ReportSummary | null>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: reportKeysOf.list(tenant.id) });
  const revoke = useMutation({
    mutationFn: (id: string) => reportApi.revoke(tenant.id, id),
    onSuccess: async (r) => {
      setRevoking(null);
      toast.success(`The link of the report for ${formatDateRange(r.from, r.to)} is revoked.`);
      await refresh();
    },
  });
  const download = useMutation({
    mutationFn: async (r: ReportSummary) => {
      const d = await reportApi.get(tenant.id, r.id);
      downloadFile(d.file, `veristar-report-${tenant.slug}-${r.from}-${r.to}.json`);
    },
    onError: (e) => toast.error(`The JSON file did not download. ${errorMessage(e)}`),
  });

  const items = list.data?.items ?? [];
  const of = (r: ReportSummary) => `the report for ${formatDateRange(r.from, r.to)}`;
  const columns: Column<ReportSummary>[] = [
    {
      key: "period",
      header: "Period",
      primary: true,
      cell: (r) => (
        <span className={cn("font-medium", r.revoked_at ? "text-fg-2" : "text-fg")}>
          {formatDateRange(r.from, r.to)}
        </span>
      ),
    },
    {
      key: "link",
      header: "Link",
      mobile: true,
      cell: (r) =>
        r.revoked_at ? <Pill tone="neutral">Revoked</Pill> : <Pill tone="success">Active</Pill>,
    },
    {
      key: "generated",
      header: "Generated",
      cell: (r) => <span className="tabular">{formatDateTimeUtc(r.created_at)}</span>,
    },
    {
      key: "views",
      header: "Views",
      align: "right",
      numeric: true,
      cell: (r) => formatCount(r.view_count),
    },
    {
      key: "copy",
      header: "Copy link",
      hideHeader: true,
      action: true,
      cell: (r) =>
        r.revoked_at ? null : (
          <CopyButton text={r.url} label="Copy link" accessibleLabel={`Copy link of ${of(r)}`} />
        ),
    },
  ];
  return (
    <section aria-labelledby="signed-reports-h" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2
          id="signed-reports-h"
          className="flex items-baseline gap-2 text-[15px] leading-5 font-semibold text-fg"
        >
          Signed reports
          {items.length > 0 ? (
            <span className="text-small font-normal text-fg-3 tabular">
              {formatCount(items.length)}
            </span>
          ) : null}
        </h2>
        {controlled || readOnly ? null : (
          <Button variant="outline" size="sm" onClick={() => setSigning(true)}>
            <FileSignature aria-hidden />
            Sign a report
          </Button>
        )}
      </div>
      {list.isPending ? (
        <LoadingState label="Loading the reports">
          <Skeleton.Table rows={2} columns={3} />
        </LoadingState>
      ) : list.error ? (
        <ErrorState
          compact
          title="The reports did not load"
          error={list.error}
          onRetry={() => list.refetch()}
        />
      ) : items.length === 0 ? (
        controlled ? null : (
          <p className="text-small text-fg-2" data-reports="none">
            No report yet
          </p>
        )
      ) : (
        <DataTable<ReportSummary>
          label="Reports"
          columns={columns}
          rows={items}
          getRowKey={(r) => r.id}
          rowLabel={(r) => of(r)}
          rowProps={(r) => ({ "data-report": r.id })}
          rowActions={(r) => [
            r.revoked_at
              ? null
              : {
                  label: "Open the report",
                  icon: <ExternalLink aria-hidden />,
                  onSelect: () => window.open(r.url, "_blank", "noopener"),
                },
            {
              label: "Download JSON",
              icon: <Download aria-hidden />,
              disabled: download.isPending && download.variables?.id === r.id,
              onSelect: () => download.mutate(r),
            },
            r.revoked_at || readOnly
              ? null
              : {
                  label: "Revoke link",
                  icon: <Ban aria-hidden />,
                  destructive: true,
                  onSelect: () => {
                    revoke.reset();
                    setRevoking(r);
                  },
                },
          ]}
        />
      )}

      {readOnly ? null : <SignDialog open={signing} onOpenChange={setSigning} onSigned={refresh} />}

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(o) => {
          if (!o) {
            setRevoking(null);
            // a refusal shown for one report must not greet the next
            revoke.reset();
          }
        }}
        title={
          revoking
            ? `Revoke the link to the report for ${formatDateRange(revoking.from, revoking.to)}?`
            : "Revoke the report link?"
        }
        description="People who open the link see that the report is not available. A downloaded JSON file keeps its valid signature. You cannot restore the link."
        confirmLabel="Revoke link"
        pendingLabel="Revoking"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </section>
  );
}

// the period of closed utc days, then the signed report's link in the same
// dialog
function SignDialog({
  open,
  onOpenChange,
  onSigned,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSigned: () => Promise<unknown>;
}) {
  const { tenant } = useProject();
  const initial = closedDays(30, new Date());
  const [from, setFrom] = useState(initial.from);
  const [to, setTo] = useState(initial.to);
  const [errors, setErrors] = useState<{ from?: string; to?: string }>({});
  const create = useMutation({
    mutationFn: () => reportApi.create(tenant.id, { from, to }),
    onSuccess: async (r) => {
      toast.success(`Report for ${formatDateRange(r.from, r.to)} signed.`);
      await onSigned();
    },
  });

  function close(o: boolean) {
    onOpenChange(o);
    if (!o) {
      create.reset();
      setErrors({});
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    if (create.data) {
      close(false);
      return;
    }
    const found = validatePeriod(from, to, new Date());
    setErrors(found);
    if (!found.from && !found.to) create.mutate();
  }

  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title={create.data ? "Report signed" : "Sign a report"}
      onSubmit={submit}
      footer={
        create.data ? (
          <Button type="submit">Done</Button>
        ) : (
          <>
            <Button type="button" variant="ghost" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" pending={create.isPending}>
              <FileSignature aria-hidden />
              {create.isPending ? "Signing" : "Generate and sign"}
            </Button>
          </>
        )
      }
    >
      {create.data ? (
        <div role="status" className="flex min-w-0 flex-wrap items-center gap-2 text-small">
          <a
            href={create.data.url}
            className="min-w-0 break-all rounded-sm font-mono text-accent-text underline-offset-4 hover:underline focus-ring"
          >
            {create.data.url}
          </a>
          <CopyButton text={create.data.url} label="Copy link" size="xs" />
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <DateField label="From" value={from} onChange={setFrom} error={errors.from} />
            <DateField
              label="To"
              value={to}
              onChange={setTo}
              error={errors.to}
              hint={`${formatDate(lastFinalDay(new Date()))} or earlier, at most ${MAX_REPORT_DAYS} days`}
            />
          </div>
          <FormMessage error={create.error} />
        </div>
      )}
    </Dialog>
  );
}
