import { useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  Check,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CirclePause,
  CirclePlay,
  CircleX,
  LoaderCircle,
  MoreHorizontal,
  Trash2,
} from "lucide-react";
import { type ReactNode, useId, useLayoutEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { api } from "@/api/endpoints";
import { type ChainId, type OnchainView, operatorOnchainQuery } from "@/api/onchain";
import {
  apiCompareQuery,
  integrityQuery,
  operatorTenantQuery,
  webCompareQuery,
} from "@/api/queries";
import type {
  ApiCompareProject,
  IntegrityGrade,
  IntegrityReport,
  OperatorSite,
  OperatorTenantDetail,
  TenantAction,
  TenantStatus,
  WebCompareProject,
} from "@/api/schemas";
import { Avatar } from "@/components/avatar";
import { type TimeBucket, TimeChart } from "@/components/charts";
import { type CheckItem, ChecksList } from "@/components/checks-list";
import { OperatorCohortPanel } from "@/components/cohort";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { type Column, DataTable, useRowPages } from "@/components/data-table";
import { DecisionPanel } from "@/components/decision-panel";
import { Disclosure } from "@/components/disclosure";
import { StellarTomlSummary, VerificationDetail } from "@/components/evidence";
import { IntegrityReportView } from "@/components/integrity";
import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/panel";
import { ScorecardTile, type ScorecardVisual } from "@/components/scorecard-tile";
import { countChange, type StatChange } from "@/components/stat-card";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill, type Tone } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Menu, MenuContent, MenuItem, MenuTrigger } from "@/components/ui/menu";
import { Tooltip } from "@/components/ui/tooltip";
import { isNotFound } from "@/lib/errors";
import {
  countOf,
  formatCompact,
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatTick,
  truncateMiddle,
} from "@/lib/format";
import { compareSearch } from "@/lib/global";
import { shortKey } from "@/lib/onchain";
import { outcomeLabel } from "@/lib/outcomes";
import { termsVersionLabel } from "@/lib/terms";
import { NotFoundPage } from "@/pages/public/not-found";
import { refreshOperator } from "./decision";
import { TenantOnchainSection } from "./onchain-parts";

type ActionSpec = {
  label: string;
  title: (name: string) => string;
  description: string;
  reasonRequired: boolean;
  tone: "primary" | "destructive";
  pending: string;
  done: (name: string) => string;
};

// contract 5.5: suspend and remove need a reason; resume takes an optional
// note. approve and reject are the decision panel's.
export const ACTIONS: Record<"suspend" | "resume" | "remove", ActionSpec> = {
  suspend: {
    label: "Suspend",
    title: (n) => `Suspend ${n}?`,
    description:
      "The sites of the project stop accepting events. Its dashboards stay readable. You can resume it later.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Suspending",
    done: (n) => `${n} is suspended. Its members see the reason.`,
  },
  resume: {
    label: "Resume",
    title: (n) => `Resume ${n}?`,
    description: "The project becomes active again and its sites accept events.",
    reasonRequired: false,
    tone: "primary",
    pending: "Resuming",
    done: (n) => `${n} is active again.`,
  },
  remove: {
    label: "Remove",
    title: (n) => `Remove ${n}?`,
    description:
      "The sites stop accepting events. The dashboards and their data are deleted, and the members lose access. The project record and the audit log stay. You cannot undo this.",
    reasonRequired: true,
    tone: "destructive",
    pending: "Removing",
    done: (n) => `${n} is removed. Its data is being deleted.`,
  },
};

const MENU_ORDER = ["resume", "suspend", "remove"] as const;

// the removal steps of contract 5.13 in words; the daily totals step says
// what the setting did with them
const STEP_TEXT: Record<string, string> = {
  registry: "Project record closed",
  otlp_tokens: "API tokens revoked",
  umami: "Web analytics deleted",
  web_ingest: "Web traffic stopped",
  api: "API data deleted",
  chain: "On-chain data deleted",
  reports: "Reports revoked",
};

function stepText(name: string, keepAggregates: boolean): string {
  if (name === "agg") return keepAggregates ? "Daily totals kept" : "Daily totals deleted";
  const known = STEP_TEXT[name];
  if (known) return known;
  // a step this console does not know yet, in sentence case
  const words = name.replace(/_/g, " ");
  return `${words.charAt(0).toUpperCase()}${words.slice(1)} done`;
}

// a removal deletes data in steps; the page checks again until it is done
const REMOVAL_POLL_MS = 10_000;

export function OperatorProjectPage() {
  const { tenantId = "" } = useParams();
  const detail = useQuery({
    ...operatorTenantQuery(tenantId),
    refetchInterval: (q) =>
      q.state.data?.removal && q.state.data.removal.completed_at === null ? REMOVAL_POLL_MS : false,
  });
  if (isNotFound(detail.error)) return <NotFoundPage />;
  if (detail.error)
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Project" />
        <ErrorState
          title="The project did not load"
          error={detail.error}
          onRetry={() => detail.refetch()}
        />
      </div>
    );
  if (detail.isPending)
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Project" />
        <LoadingState label="Loading the project">
          <Skeleton.StatRow count={3} />
          <Skeleton.Table rows={3} columns={2} />
        </LoadingState>
      </div>
    );
  return <TenantDetail detail={detail.data} />;
}

type CheckKey = "ownership" | "integrity" | "web" | "api" | "onchain";

const DEFAULT_RANGE = { from: "", to: "" };

const GRADE_RANK: Record<IntegrityGrade, number> = { A: 0, B: 1, C: 2 };

// the grade of the project on each day: the lowest grade of its sites
export function dailyGrades(report: IntegrityReport | undefined, days: string[]): (IntegrityGrade | null)[] {
  return days.map((d) => {
    let worst: IntegrityGrade | null = null;
    for (const s of report?.sites ?? []) {
      const g = s.days.find((x) => x.day === d)?.grade ?? null;
      if (g && (worst === null || GRADE_RANK[g] > GRADE_RANK[worst])) worst = g;
    }
    return worst;
  });
}

export type Proof = CheckItem & { done: boolean; label: string };

// every proof of ownership the project holds or lacks, as the scf review
// counts them: each live site's domain, a stellar.toml, and each on-chain
// id's control (and an account's home domain)
export function proofsOf(sites: OperatorSite[], chain: OnchainView | undefined): Proof[] {
  const out: Proof[] = [];
  const live = sites.filter((x) => x.status !== "removed");
  const mono = (t: string) => <span className="font-mono text-caption text-fg-3">{t}</span>;
  const when = (t: string | null) =>
    t ? <span className="text-caption text-fg-3">{formatDate(t)}</span> : undefined;
  for (const x of live) {
    const ok = x.verification === "verified";
    out.push({
      key: `domain-${x.id}`,
      label: ok ? "Domain verified" : "Domain not verified",
      done: ok,
      status: ok ? "passed" : "failed",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {ok ? "Domain verified" : "Domain not verified"}
          {mono(x.domain)}
        </span>
      ),
      aside: ok ? when(x.verified_at) : undefined,
    });
  }
  const first = live[0];
  if (first) {
    const found = live.find((x) => x.stellar_toml?.status === "found");
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
      aside: found ? when(found.stellar_toml_checked_at) : undefined,
    });
  }
  const ids = chain?.ids ?? [];
  const accounts = ids.filter((i) => i.kind === "account");
  const contracts = ids.filter((i) => i.kind === "contract");
  const proven = (i: ChainId): Proof => {
    const ok = i.verification.status === "verified";
    const what = i.kind === "account" ? "Account" : "Contract";
    return {
      key: `proof-${i.id}`,
      label: ok ? `${what} proven` : `${what} not proven`,
      done: ok,
      status: ok ? "passed" : "failed",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {ok ? `${what} proven` : `${what} not proven`}
          <span title={i.strkey}>{mono(shortKey(i.strkey))}</span>
        </span>
      ),
      aside: ok ? when(i.verification.verified_at) : undefined,
    };
  };
  for (const acc of accounts) {
    const home = acc.read.home_domain;
    out.push({
      key: `home-${acc.id}`,
      label: home ? "Home domain set" : "Home domain not set",
      done: !!home,
      status: home ? "passed" : "missing",
      children: (
        <span className="flex flex-wrap items-baseline gap-x-3">
          {home ? "Home domain set" : "Home domain not set"}
          <span title={acc.strkey}>{mono(home ?? shortKey(acc.strkey))}</span>
        </span>
      ),
    });
    out.push(proven(acc));
  }
  if (accounts.length === 0)
    out.push({
      key: "account",
      label: "No account added",
      done: false,
      status: "missing",
      children: "No account added",
    });
  for (const c of contracts) out.push(proven(c));
  if (contracts.length === 0)
    out.push({
      key: "contract",
      label: "No contract added",
      done: false,
      status: "missing",
      children: "No contract added",
    });
  return out;
}

function compact(n: number): string {
  return n >= 100_000 ? formatCompact(n) : formatCount(n);
}

const gradeTone = (x: IntegrityGrade | null | undefined): Tone =>
  x === "A" ? "success" : x === "B" ? "warning" : x === "C" ? "critical" : "neutral";

// the operator's page of one project: the same five-tile scorecard as the
// scf review (ownership, traffic integrity, web, api, on-chain), so the
// operator and the project read one picture. the picked tile opens its
// detail under the row; the answers, the cohort and the members are in the
// rail. a removed project shows its record only: its data is deleted and no
// action applies.
export function TenantDetail({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const removed = t.status === "removed";
  const decides = detail.allowed_actions.some((a) => a === "approve" || a === "reject");
  const web = useQuery({ ...webCompareQuery([t.id], DEFAULT_RANGE), enabled: !removed });
  const api = useQuery({ ...apiCompareQuery([t.id], DEFAULT_RANGE), enabled: !removed });
  const chain = useQuery({ ...operatorOnchainQuery(t.id, {}), enabled: !removed });
  const report = useQuery({
    ...integrityQuery("operator", t.id, DEFAULT_RANGE),
    enabled: !removed && detail.sites.length > 0,
  });
  const w = web.data?.projects[0];
  const a = api.data?.projects[0];
  const days = web.data?.days ?? api.data?.days ?? [];
  const range = web.data ?? api.data ?? null;
  const today = new Date().toISOString().slice(0, 10);
  const running = range ? range.to >= today : false;
  const labels = days.map((d) => formatDate(d));
  const proofs = chain.data || chain.error ? proofsOf(detail.sites, chain.data) : null;
  const proofsDone = proofs?.filter((x) => x.done).length ?? 0;
  const grade = report.data && report.data.graded_days > 0 ? report.data : null;
  const webOk = (w?.totals.visitors ?? 0) > 0;
  const apiOk = (a?.totals.requests ?? 0) > 0;
  const onchain = w?.onchain ?? a?.onchain;
  const chainOk = (onchain?.totals.ids_verified ?? 0) > 0 && (onchain?.totals.operations ?? 0) > 0;
  const spark = (values: number[], series: 1 | 2 | 3): ScorecardVisual =>
    values.some((x) => x > 0) ? { kind: "spark", values, series, running, labels } : { kind: "none" };

  // the page opens on the weakest check, so it leads with what is missing
  const weakest: CheckKey =
    proofs && proofsDone < proofs.length
      ? "ownership"
      : grade?.grade && grade.grade !== "A"
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
      tone: !proofs ? "neutral" : proofsDone === proofs.length ? "success" : "critical",
      value: proofs ? `${proofsDone} of ${proofs.length}` : "–",
      sub: "proofs in place",
      visual: proofs
        ? { kind: "proofs", total: proofs.length, done: proofsDone, labels: proofs.map((x) => x.label) }
        : { kind: "none" },
    },
    {
      key: "integrity",
      name: "Traffic integrity",
      tone: gradeTone(grade?.grade),
      value: grade?.grade ? `Grade ${grade.grade}` : "–",
      sub: grade ? countOf(grade.graded_days, "site day") + " graded" : "not graded",
      visual: grade ? { kind: "calendar", days: dailyGrades(report.data, days), labels } : { kind: "none" },
    },
    {
      key: "web",
      name: "Web",
      tone: webOk ? "success" : "neutral",
      value: webOk ? compact(w?.totals.visitors ?? 0) : "–",
      change: webOk && w ? countChange(w.totals.visitors, w.previous_totals.visitors) : null,
      sub: "verified visitors",
      visual: spark((w?.series ?? []).map((x) => x.visitors ?? 0), 1),
    },
    {
      key: "api",
      name: "API",
      tone: apiOk ? "success" : "neutral",
      value: apiOk ? compact(a?.totals.requests ?? 0) : "–",
      change: apiOk && a ? countChange(a.totals.requests, a.previous_totals.requests) : null,
      sub: "requests",
      visual: spark((a?.series ?? []).map((x) => x.requests), 3),
    },
    {
      key: "onchain",
      name: "On-chain",
      tone: chainOk ? "success" : "neutral",
      value: chainOk ? compact(onchain?.totals.operations ?? 0) : "–",
      sub: chainOk ? "operations" : "no proven ID yet",
      visual: chainOk ? spark((onchain?.series ?? []).map((x) => x.operations ?? 0), 2) : { kind: "none" },
    },
  ];

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title={t.name}
        crumb={
          <Link to="/operator/projects" className="rounded-sm hover:text-fg focus-ring">
            All projects
          </Link>
        }
        meta={<StatusBadge kind="project" status={t.status} operator />}
        items={[
          <span key="slug" className="font-mono text-[12.5px]" title={t.slug}>
            {truncateMiddle(t.slug, 40)}
          </span>,
          <span key="enrolled" title={formatDateTimeUtc(t.created_at)}>
            Enrolled {formatDate(t.created_at)}
            {t.created_by ? ` by ${t.created_by.display_name}` : ""}
          </span>,
          t.approved_by ? <span key="approved">Approved by {t.approved_by.display_name}</span> : null,
          t.status !== "active" ? (
            <span key="since">Status since {formatDateTimeUtc(t.status_changed_at)}</span>
          ) : null,
          range && !removed ? (
            <span key="range">{`${formatDateRange(range.from, range.to)}, UTC`}</span>
          ) : null,
        ]}
        secondary={
          <>
            {removed ? null : (
              <Link
                to={`/operator/compare?${compareSearch([t.id], DEFAULT_RANGE)}`}
                className="rounded-sm px-2 text-small text-fg-2 hover:text-fg focus-ring"
              >
                Open in Compare
              </Link>
            )}
            <Link
              to={`/operator/audit?tenant_id=${t.id}`}
              className="rounded-sm px-2 text-small text-fg-2 hover:text-fg focus-ring"
            >
              Audit log
            </Link>
            <MoreActions detail={detail} />
          </>
        }
      />

      {t.status_reason ? (
        <p className="-mt-2 max-w-prose text-small break-words text-fg">
          <span className="text-fg-3">Reason </span>
          <q>{t.status_reason}</q>
        </p>
      ) : null}

      {detail.removal ? <Removal removal={detail.removal} /> : null}

      {decides ? (
        <section
          aria-label="Decision"
          className="rounded-2xl border border-line bg-raised px-5 py-3.5 sm:px-6"
        >
          <DecisionPanel
            subject={{ kind: "project", id: t.id, name: t.name, allowed: detail.allowed_actions }}
            lead={<span className="text-fg-3">The project waits for a decision</span>}
          />
        </section>
      ) : null}

      {removed ? null : (
        <section aria-label="Scorecard" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
          {tiles.map((x) => (
            <ScorecardTile
              key={x.key}
              data-check={x.key}
              name={x.name}
              tone={x.tone}
              value={x.value}
              sub={x.sub}
              change={x.change}
              visual={x.visual}
              selected={open === x.key}
              onSelect={() => setPicked(x.key)}
            />
          ))}
        </section>
      )}

      <div className="grid min-w-0 items-start gap-4 xl:grid-cols-[minmax(0,1fr)_20.5rem]">
        <div className="flex min-w-0 flex-col gap-4">
          {removed ? (
            <Sites sites={detail.sites} decides={false} project={t.status} />
          ) : (
            <CheckDetail
              open={open}
              tone={tiles.find((x) => x.key === open)?.tone ?? "neutral"}
              detail={detail}
              proofs={proofs}
              chainError={chain.error}
              onRetryChain={() => chain.refetch()}
              web={w}
              api={a}
              days={days}
              running={running}
            />
          )}
        </div>
        <aside className="flex min-w-0 flex-col gap-4 xl:sticky xl:top-4">
          <Answers detail={detail} />
          <OperatorCohortPanel tenantId={t.id} readOnly={removed} />
          <Box id="members-h" title="Members" count={detail.members.length}>
            <ul className="flex flex-col gap-2.5">
              {detail.members.map((m) => (
                <li
                  key={m.github_id ?? m.stellar_address ?? m.passkey_address ?? m.display_name}
                  className="flex min-w-0 items-center gap-2.5 text-body"
                  title={`${m.role === "owner" ? "Owner" : "Member"} since ${formatDate(m.created_at)}`}
                >
                  <Avatar name={m.display_name} className="size-6" />
                  <span className="min-w-0 flex-1 truncate text-fg">{m.display_name}</span>
                  <span className="text-small text-fg-3">
                    {m.role === "owner" ? "Owner" : "Member"}
                  </span>
                </li>
              ))}
            </ul>
          </Box>
        </aside>
      </div>
    </div>
  );
}

const DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  critical: "bg-critical",
  info: "bg-info",
  neutral: "bg-fg-3",
};

const DETAIL_TITLE: Record<CheckKey, string> = {
  ownership: "Ownership",
  integrity: "Traffic integrity",
  web: "Web",
  api: "API",
  onchain: "On-chain",
};

function dayBuckets(days: string[], running: boolean): TimeBucket[] {
  return days.map((d, i) => ({
    at: `${d}T00:00:00Z`,
    tick: formatTick(d, "day"),
    label: formatDate(d),
    partial: running && i === days.length - 1,
  }));
}

// the detail of the picked tile: its proofs and sites, its grades, its
// chart with the tracker requests by outcome, or its on-chain ids
function CheckDetail({
  open,
  tone,
  detail,
  proofs,
  chainError,
  onRetryChain,
  web,
  api,
  days,
  running,
}: {
  open: CheckKey;
  tone: Tone;
  detail: OperatorTenantDetail;
  proofs: Proof[] | null;
  chainError: unknown;
  onRetryChain: () => void;
  web: WebCompareProject | undefined;
  api: ApiCompareProject | undefined;
  days: string[];
  running: boolean;
}) {
  const t = detail.tenant;
  const link =
    open === "web"
      ? { to: "web", label: "Open Web" }
      : open === "api"
        ? { to: "api", label: "Open API" }
        : null;
  return (
    <section
      aria-labelledby="check-detail-h"
      data-detail={open}
      className="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-raised"
    >
      <div className="flex items-center gap-2.5 px-5 py-3.5">
        <span aria-hidden className={cn("size-2 rounded-full", DOT[tone])} />
        <h2 id="check-detail-h" className="text-[15px] font-semibold text-fg">
          {DETAIL_TITLE[open]}
        </h2>
        {open === "web" ? <span className="text-small text-fg-3">verified visitors per day</span> : null}
        {open === "api" ? <span className="text-small text-fg-3">requests per day</span> : null}
        {link ? (
          <Link
            to={`/operator/compare?${compareSearch([t.id], DEFAULT_RANGE)}&view=${link.to}`}
            className="ml-auto rounded-sm text-small text-fg-2 hover:text-fg focus-ring"
          >
            Open in Compare
          </Link>
        ) : null}
      </div>
      {open === "ownership" ? (
        <div className="flex min-w-0 flex-col gap-5 border-t border-line px-5 pt-2 pb-5">
          {chainError ? (
            <ErrorState
              compact
              title="The on-chain IDs did not load"
              error={chainError}
              onRetry={onRetryChain}
            />
          ) : null}
          {proofs ? <ChecksList label="Proofs of ownership" items={proofs} /> : null}
          <Sites sites={detail.sites} decides project={t.status} />
        </div>
      ) : open === "integrity" ? (
        <div className="min-w-0 border-t border-line p-5">
          <IntegrityReportView
            scope="operator"
            tenantId={t.id}
            range={DEFAULT_RANGE}
            siteNames={Object.fromEntries(detail.sites.map((x) => [x.id, x.domain]))}
          />
        </div>
      ) : open === "onchain" ? (
        <div className="min-w-0 border-t border-line p-5">
          <TenantOnchainSection tenantId={t.id} />
        </div>
      ) : (
        <div className="flex min-w-0 flex-col">
          <div className="px-5 pb-4">
            {open === "web" ? (
              web && web.totals.visitors > 0 ? (
                <TimeChart
                  title="Verified visitors"
                  description="per day, UTC"
                  headingLevel={3}
                  buckets={dayBuckets(days, running)}
                  series={[
                    {
                      key: "visitors",
                      label: "Verified visitors",
                      tone: "series-1",
                      values: web.series.map((x) => (x.aggregated ? x.visitors : null)),
                    },
                  ]}
                  axisFormat={formatCompact}
                  state="data"
                />
              ) : (
                <EmptyState compact title="No web data in this range" />
              )
            ) : api && api.totals.requests > 0 ? (
              <TimeChart
                title="API requests"
                description="per day, UTC"
                headingLevel={3}

                buckets={dayBuckets(days, running)}
                series={[
                  {
                    key: "requests",
                    label: "API requests",
                    tone: "series-3",
                    values: api.series.map((x) => x.requests),
                  },
                ]}
                axisFormat={formatCompact}
                state="data"
              />
            ) : (
              <EmptyState compact title="No API requests in this range" />
            )}
          </div>
          {open === "web" ? <Outcomes ingest={detail.ingest} /> : null}
        </div>
      )}
    </section>
  );
}

// the tracker requests of the last 30 full days as accepted, held back and
// refused; every outcome of the last 24 hours and 30 days opens under them
function Outcomes({ ingest }: { ingest: OperatorTenantDetail["ingest"] }) {
  const sum = (pred: (k: string) => boolean) =>
    Object.entries(ingest.last_30d)
      .filter(([k]) => pred(k))
      .reduce((n, [, v]) => n + v, 0);
  const accepted = sum((k) => k === "accepted");
  const held = sum((k) => k.startsWith("quarantine"));
  const refused = sum((k) => k !== "accepted" && !k.startsWith("quarantine"));
  return (
    <div className="flex min-w-0 flex-col border-t border-line">
      <h3 className="px-5 pt-3 text-small font-medium text-fg-2">Tracker requests, last 30 full days</h3>
      <dl className="grid grid-cols-3">
        {[
          ["Accepted", accepted],
          ["Held back", held],
          ["Refused", refused],
        ].map(([k, v], i) => (
          <div
            key={k as string}
            className={cn("flex flex-col gap-0.5 px-5 py-3", i > 0 && "border-l border-line")}
          >
            <dt className="text-small text-fg-3">{k}</dt>
            <dd className="text-[17px] font-semibold text-fg tabular">{formatCount(v as number)}</dd>
          </div>
        ))}
      </dl>
      <Disclosure
        variant="inline"
        summary="Every outcome"
        className="border-t border-line px-5 py-3 text-small"
        bodyClassName="pt-3"
      >
        <div className="grid gap-5 sm:grid-cols-2">
          <Counts title="Last 24 full hours, UTC" counts={ingest.last_24h} />
          <Counts title="Last 30 full days, UTC" counts={ingest.last_30d} />
        </div>
      </Disclosure>
    </div>
  );
}

function Box({
  id,
  title,
  count,
  aside,
  children,
}: {
  id: string;
  title: string;
  count?: number;
  aside?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section
      aria-labelledby={id}
      className="flex min-w-0 flex-col gap-3 rounded-2xl border border-line bg-raised p-5"
    >
      <div className="flex items-baseline gap-2">
        <h2 id={id} className="text-body font-semibold text-fg">
          {title}
        </h2>
        {count !== undefined ? (
          <span className="ml-auto text-small text-fg-3 tabular">{count}</span>
        ) : null}
        {aside}
      </div>
      {children}
    </section>
  );
}

// the enrollment answers in the rail, label over value
export function Answers({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const a = t.enrollment_answers;
  const rows: [string, ReactNode][] = [
    ["Website", <span className="font-mono text-[12.5px] break-all">{a.website}</span>],
    ["How it uses Stellar", <Clamped text={a.stellar_usage} />],
    ...(t.description
      ? [["Description", <Clamped text={t.description} />] as [string, ReactNode]]
      : []),
    ...(a.scf_round_url
      ? [["SCF round link", <span className="break-all">{a.scf_round_url}</span>] as [string, ReactNode]]
      : []),
  ];
  return (
    <Box id="answers-h" title="Enrollment answers">
      <dl className="flex flex-col gap-3 text-body">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-w-0 flex-col gap-0.5">
            <dt className="text-small text-fg-3">{k}</dt>
            <dd className="text-fg">{v}</dd>
          </div>
        ))}
        {detail.stellar_ids.length > 0 ? (
          <div className="flex min-w-0 flex-col gap-0.5">
            <dt className="text-small text-fg-3">Account and contract IDs</dt>
            <dd>
              <ul className="flex flex-col gap-1" aria-label="Account and contract IDs">
                {detail.stellar_ids.map((x) => (
                  <li key={x.id} className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                    <code className="min-w-0 font-mono text-caption break-all text-fg">{x.id}</code>
                    <span className={x.valid ? "text-caption text-fg-3" : "text-caption text-warning"}>
                      {x.valid ? (x.type ?? "").replace("_", " ") : "Not a valid ID"}
                    </span>
                  </li>
                ))}
              </ul>
            </dd>
          </div>
        ) : null}
        <div className="flex min-w-0 flex-col gap-0.5">
          <dt className="text-small text-fg-3">Terms</dt>
          <dd className="text-fg">
            {t.accepted_terms_version
              ? `Accepted, ${termsVersionLabel(t.accepted_terms_version).replace("Version", "version")}`
              : "Not accepted"}
          </dd>
        </div>
        <div className="flex min-w-0 flex-col gap-0.5">
          <dt className="text-small text-fg-3">Dashboards</dt>
          <dd className="text-fg">{t.provisioning === "ready" ? "Set up" : "Not set up yet"}</dd>
        </div>
      </dl>
    </Box>
  );
}

// a long answer shows its first three lines and a toggle for the rest, so
// one long answer does not push the other answers out of view. the toggle
// shows only when the text has more lines than that.
export function Clamped({ text }: { text: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  const id = useId();
  const [open, setOpen] = useState(false);
  const [long, setLong] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || open) return;
    const measure = () => setLong(el.scrollHeight > el.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [open, text]);
  return (
    <span className="flex flex-col items-start gap-1">
      <span
        ref={ref}
        id={id}
        data-clamped={open ? undefined : ""}
        className={cn("whitespace-pre-wrap break-words", !open && "line-clamp-3")}
      >
        {text}
      </span>
      {long ? (
        <Button
          variant="link"
          size="xs"
          aria-expanded={open}
          aria-controls={id}
          onClick={() => setOpen((o) => !o)}
          className="max-lg:min-h-(--vs-control-touch)"
        >
          {open ? "Show less" : "Show more"}
        </Button>
      ) : null}
    </span>
  );
}

// suspend, resume and remove: less frequent than a decision, so they sit in
// one menu, and each goes through the confirmation with its reason
function MoreActions({ detail }: { detail: OperatorTenantDetail }) {
  const t = detail.tenant;
  const [action, setAction] = useState<(typeof MENU_ORDER)[number] | null>(null);
  const run = useMutation({
    mutationFn: ({ act, reason }: { act: TenantAction; reason: string }) =>
      api.operator.tenantAction(t.id, act, reason),
    onSuccess: async (_d, vars) => {
      setAction(null);
      const spec = ACTIONS[vars.act as (typeof MENU_ORDER)[number]];
      toast.success(spec.done(t.name));
      await refreshOperator();
    },
  });
  const allowed = MENU_ORDER.filter((x) => detail.allowed_actions.includes(x));
  if (allowed.length === 0) return null;
  const spec = action ? ACTIONS[action] : null;
  return (
    <>
      <Menu>
        <MenuTrigger asChild>
          <Button variant="outline" className="max-lg:h-(--vs-control-touch)">
            <MoreHorizontal aria-hidden />
            Actions
          </Button>
        </MenuTrigger>
        <MenuContent align="end">
          {allowed.map((x) => (
            <MenuItem
              key={x}
              tone={ACTIONS[x].tone === "destructive" ? "critical" : undefined}
              onSelect={() => {
                run.reset();
                setAction(x);
              }}
            >
              {x === "suspend" ? (
                <CirclePause aria-hidden />
              ) : x === "remove" ? (
                <Trash2 aria-hidden />
              ) : (
                <CirclePlay aria-hidden />
              )}
              {ACTIONS[x].label} project
            </MenuItem>
          ))}
        </MenuContent>
      </Menu>
      <ConfirmDialog
        open={action !== null}
        onOpenChange={(o) => {
          if (!o) setAction(null);
        }}
        title={spec ? spec.title(t.name) : ""}
        description={spec?.description ?? ""}
        reason={{ required: spec?.reasonRequired ?? true }}
        typed={
          action === "remove" ? { value: t.slug, label: `Type ${t.slug} to confirm` } : undefined
        }
        confirmLabel={spec ? `${spec.label} project` : ""}
        pendingLabel={spec?.pending ?? ""}
        tone={spec?.tone ?? "destructive"}
        pending={run.isPending}
        error={run.error}
        onConfirm={(reason) => action && run.mutate({ act: action, reason })}
      />
    </>
  );
}

// the sites as dense rows, so many sites do not push the record down: the
// first rows, then the rest on request
const SITES_SHOWN = 10;

function Sites({
  sites,
  decides,
  project,
}: {
  sites: OperatorSite[];
  decides: boolean;
  project: TenantStatus;
}) {
  const [open, setOpen] = useState<OperatorSite | null>(null);
  // sites that wait for a decision come first, and each gets its decision
  // above the table
  const ordered = [...sites].sort((a, b) => Number(b.in_review) - Number(a.in_review));
  const waiting = decides ? ordered.filter((s) => s.in_review) : [];
  const paged = useRowPages(ordered, SITES_SHOWN);
  const columns: Column<OperatorSite>[] = [
    {
      key: "site",
      header: "Site",
      primary: true,
      minWidth: "min-w-48",
      className: "w-full max-w-0",
      truncate: (x) => `${x.name}, ${x.domain}`,
      cell: (x) => (
        <span className="flex min-w-0 flex-col py-1">
          <span className="flex min-w-0 items-center gap-1 font-medium text-fg">
            <span className="truncate">{x.name}</span>
            {x.in_review || x.status !== "active" || project !== "active" ? (
              <StatusBadge
                kind="site"
                status={x.in_review ? "pending" : x.status}
                project={project}
                operator
              />
            ) : null}
            {x.domain_previously_removed ? (
              <Tooltip content="This domain, or a parent or subdomain of it, belonged to a removed project.">
                <CircleAlert
                  aria-label="Domain of a removed project"
                  className="size-3.5 shrink-0 text-warning"
                />
              </Tooltip>
            ) : null}
          </span>
          <span className="truncate font-mono text-caption font-normal text-fg-2">{x.domain}</span>
        </span>
      ),
    },
    {
      key: "domain",
      header: "Domain",
      mobile: true,
      cell: (x) =>
        x.verification === "verified" ? (
          <Quiet>Verified</Quiet>
        ) : x.verification === "unchecked" ? (
          <span className="text-fg-3">Not checked yet</span>
        ) : (
          <Pill tone="critical">Not verified</Pill>
        ),
    },
    {
      key: "toml",
      header: "stellar.toml",
      mobile: true,
      cell: (x) =>
        x.stellar_toml?.status === "found" ? (
          <Quiet>Found</Quiet>
        ) : x.stellar_toml === null ? (
          <span className="text-fg-3">Not checked yet</span>
        ) : (
          <Pill tone="critical">Not found</Pill>
        ),
    },
    {
      key: "origins",
      header: "Origins",
      align: "right",
      numeric: true,
      cell: (x) => formatCount(x.origins.length),
    },
  ];
  return (
    <Section title="Sites" count={sites.length > 0 ? sites.length : null}>
      {waiting.length > 0 ? (
        <ul className="flex min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-raised">
          {waiting.map((x) => (
            <li key={x.id} className="border-b border-line-subtle last:border-0">
              <SiteRow site={x} project={project} />
            </li>
          ))}
        </ul>
      ) : null}
      {sites.length === 0 ? (
        <p className="text-small text-fg-2">The project has no site.</p>
      ) : (
        <DataTable<OperatorSite>
          label="Sites"
          columns={columns}
          rows={paged.rows}
          getRowKey={(x) => x.id}
          rowLabel={(x) => x.name}
          rowProps={(x) => ({ "data-site": x.id })}
          rowActions={(x) => [{ label: "Show checks", onSelect: () => setOpen(x) }]}
          footer={paged.footer}
        />
      )}
      <Dialog
        open={open !== null}
        onOpenChange={(o) => {
          if (!o) setOpen(null);
        }}
        title={open ? `Checks of ${open.domain}` : ""}
      >
        {open ? <SiteChecks site={open} /> : null}
      </Dialog>
    </Section>
  );
}

function DomainFact({ site }: { site: OperatorSite }) {
  return (
    <SiteFact
      state={
        site.verification === "verified"
          ? "ok"
          : site.verification === "unchecked"
            ? "absent"
            : "missing"
      }
    >
      {site.verification === "verified"
        ? "Domain verified"
        : site.verification === "unchecked"
          ? "Domain not checked yet"
          : "Domain not verified"}
    </SiteFact>
  );
}

function TomlFact({ site }: { site: OperatorSite }) {
  const toml = site.stellar_toml?.status;
  return (
    <SiteFact state={toml === "found" ? "ok" : toml === undefined ? "absent" : "missing"}>
      {toml === "found"
        ? "stellar.toml found"
        : toml === undefined
          ? "stellar.toml not checked yet"
          : "No stellar.toml"}
    </SiteFact>
  );
}

// the full checks of one site: the domain check, the stellar.toml, the
// allowed origins and the reason of its status
function SiteChecks({ site }: { site: OperatorSite }) {
  return (
    <div className="flex min-w-0 flex-col gap-4 text-small">
      {site.domain_previously_removed ? (
        <p className="text-warning">
          This domain, or a parent or subdomain of it, belonged to a removed project.
        </p>
      ) : null}
      {site.status_reason ? (
        <p className="break-words text-fg">
          <span className="text-fg-2">Reason: </span>
          {site.status_reason}
        </p>
      ) : null}
      <VerificationDetail
        verification={site.verification}
        verifyMethod={site.verify_method}
        verifiedAt={site.verified_at}
        lastVerifyAt={site.last_verify_at}
        lastVerifyError={site.last_verify_error}
      />
      <StellarTomlSummary
        toml={site.stellar_toml}
        checkedAt={site.stellar_toml_checked_at}
        domain={site.domain}
      />
      <div className="flex flex-col gap-1">
        <h3 className="text-caption text-fg-2">Allowed origins</h3>
        <ul className="flex flex-wrap gap-1" aria-label="Allowed origins">
          {site.origins.map((o) => (
            <li
              key={o}
              className="rounded-sm border border-line px-1 font-mono text-caption break-all text-fg-2"
            >
              {o}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

// a passed check in a cell: a quiet check icon with plain text
function Quiet({ children }: { children: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap text-fg-2">
      <Check aria-hidden className="size-3.5 text-success" strokeWidth={2.6} />
      {children}
    </span>
  );
}

// one fact of a site with its status in words: the icon and the colour
// only repeat what the text says
function SiteFact({ state, children }: { state: "ok" | "missing" | "absent"; children: string }) {
  const Icon = state === "ok" ? CircleCheck : state === "missing" ? CircleX : CircleDashed;
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap" data-fact={state}>
      <Icon
        aria-hidden
        className={cn(
          "size-3.5",
          state === "ok" ? "text-success" : state === "missing" ? "text-critical" : "text-fg-3",
        )}
        strokeWidth={2.4}
      />
      {children}
    </span>
  );
}

// a site that waits for a decision: its name, its checks in words, the
// full checks on request, and the decision
function SiteRow({ site, project }: { site: OperatorSite; project: TenantStatus }) {
  return (
    <article
      aria-labelledby={`op-site-${site.id}`}
      data-review-site={site.id}
      className="flex min-w-0 flex-col gap-2 px-4 py-3 sm:px-5"
    >
      <div className="grid min-w-0 gap-x-4 gap-y-1 md:grid-cols-[minmax(0,1fr)_auto] md:items-center">
        <div className="flex min-w-0 flex-col">
          <h3 id={`op-site-${site.id}`} className="truncate text-body font-medium text-fg">
            {site.name}
          </h3>
          <span className="truncate font-mono text-caption text-fg-2" title={site.domain}>
            {site.domain}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-small text-fg-2">
          <StatusBadge kind="site" status="pending" project={project} operator />
          <DomainFact site={site} />
          <TomlFact site={site} />
        </div>
      </div>
      <Disclosure
        variant="inline"
        summary="Checks and allowed origins"
        className="text-small"
        bodyClassName="pt-3"
      >
        <SiteChecks site={site} />
      </Disclosure>
      <div className="border-t border-line-subtle pt-3">
        <DecisionPanel subject={{ kind: "site", id: site.id, name: site.domain }} />
      </div>
    </article>
  );
}

function Counts({ title, counts }: { title: string; counts: Record<string, number> }) {
  const rows = Object.entries(counts).sort((x, y) => y[1] - x[1]);
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <h3 className="text-caption text-fg-2">{title}</h3>
      {rows.length === 0 ? (
        <p className="text-small text-fg-2">No tracker requests.</p>
      ) : (
        <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 text-small">
          {rows.map(([k, n]) => (
            <div key={k} className="contents">
              <dt className="border-b border-line-subtle py-1 text-fg-2">{outcomeLabel(k)}</dt>
              <dd className="border-b border-line-subtle py-1 text-right text-fg tabular">
                {formatCount(n)}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

function Removal({ removal }: { removal: NonNullable<OperatorTenantDetail["removal"]> }) {
  const by = removal.requested_by_display_name;
  const steps = Object.entries(removal.steps).sort((x, y) =>
    x[1].done_at.localeCompare(y[1].done_at),
  );
  const done = removal.completed_at !== null;
  return (
    <section
      aria-labelledby="removal-h"
      className="flex flex-col gap-3 rounded-lg border border-line bg-raised p-4 sm:p-5"
    >
      <h2 id="removal-h" className="text-title text-fg">
        Removal
      </h2>
      <p className="flex items-center gap-2 text-small font-medium text-fg" role="status">
        {done ? (
          <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
        ) : (
          <LoaderCircle aria-hidden className="size-4 animate-spin text-fg-2" />
        )}
        {done
          ? `Completed ${formatDateTimeUtc(removal.completed_at ?? "")}.`
          : "The service is deleting the data. This page checks again every 10 seconds."}
      </p>
      <p className="text-small text-fg-2" data-requested="">
        Requested {formatDateTimeUtc(removal.requested_at)}
        {by ? ` by ${by}` : ""}. The daily totals are {removal.keep_aggregates ? "kept" : "deleted"}
        .
      </p>
      {steps.length > 0 ? (
        <ul className="flex flex-col gap-1" aria-label="Removal steps">
          {steps.map(([name, s]) => (
            <li
              key={name}
              className="flex flex-wrap items-center gap-x-2 text-small"
              data-step={name}
            >
              <Check aria-hidden className="size-3.5 text-success" strokeWidth={2.6} />
              <span className="text-fg">{stepText(name, removal.keep_aggregates)}</span>
              <span className="text-fg-2">{formatDateTimeUtc(s.done_at)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-small text-fg-2">No step is done yet.</p>
      )}
    </section>
  );
}
