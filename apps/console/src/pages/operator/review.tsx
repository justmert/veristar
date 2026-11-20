import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { ArrowLeft, Check } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { type ReactNode, useEffect } from "react";
import { Link, useSearchParams } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { auditQuery, operatorTenantQuery, operatorTenantsQuery } from "@/api/queries";
import type {
  AuditEntry,
  OperatorSite,
  OperatorTenantDetail,
  OperatorTenantSummary,
} from "@/api/schemas";
import { Avatar } from "@/components/avatar";
import { ChecksList } from "@/components/checks-list";
import { type Column, DataTable } from "@/components/data-table";
import { DecisionPanel } from "@/components/decision-panel";
import { LabeledFacts } from "@/components/labeled-facts";
import { PageHeader } from "@/components/page-header";
import { Kbd } from "@/components/panel";
import { ErrorState, LoadingState, Skeleton } from "@/components/states";
import { Pill, statusEntry } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { auditReason } from "@/lib/audit";
import { countOf, formatDate, formatDateTimeShort, formatTimeUtc } from "@/lib/format";
import { termsVersionLabel } from "@/lib/terms";
import { ReviewSkeleton } from "@/pages/operator/page-frame";
import { useNow } from "./alerts";
import { ProjectNumbers, useProjectNumbers } from "./numbers";
import { projectChecks, siteChecks } from "./review-checks";

// contract 5.3 review=true: pending projects and active projects with a site
// in review, oldest first so nobody waits longest
export const REVIEW_QUEUE: TenantListQuery = {
  status: [],
  review: true,
  q: "",
  sort: "created_at",
  order: "asc",
};

// "today", "1 day", "3 days": how long a case has waited
function waited(iso: string, now: number): string {
  const d = Math.floor((now - new Date(iso).getTime()) / 86_400_000);
  if (d < 1) return "today";
  return d === 1 ? "1 day" : `${d} days`;
}

// the operator's queue: the cases that wait on the left, the selected one
// on the right as a short document that ends in the decision bar. on a
// phone the selected case is its own screen. the selection is in the
// address (?project=), so a case can be shared.
export function ReviewQueuePage() {
  const list = useInfiniteQuery(operatorTenantsQuery(REVIEW_QUEUE));
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const [params, setParams] = useSearchParams();
  const picked = params.get("project");
  const now = useNow(60_000);
  // the picked project, or the oldest one when none is picked or it left
  const selected = items.find((t) => t.id === picked) ?? items[0] ?? null;
  const pick = (id: string | null) =>
    setParams(id ? { project: id } : {}, { replace: id !== null && picked !== null });

  // j and k move through the queue, as in a mail client
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const el = e.target instanceof Element ? e.target : null;
      if (el?.closest("input, textarea, select, [contenteditable=true]")) return;
      if (e.key !== "j" && e.key !== "k") return;
      const i = selected ? items.findIndex((t) => t.id === selected.id) : -1;
      const next = items[Math.min(items.length - 1, Math.max(0, i + (e.key === "j" ? 1 : -1)))];
      if (next) {
        e.preventDefault();
        setParams({ project: next.id }, { replace: true });
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [items, selected, setParams]);

  // after a decision the next case in the queue opens
  function decided(t: OperatorTenantSummary) {
    const i = items.findIndex((x) => x.id === t.id);
    const next = items[i + 1] ?? items[i - 1] ?? null;
    setParams(next ? { project: next.id } : {}, { replace: true });
  }

  const empty = list.data !== undefined && items.length === 0;

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <PageHeader
        title="Review"
        count={list.data && items.length > 0 && !list.hasNextPage ? items.length : null}
        meta={
          list.hasNextPage ? (
            <span className="text-body text-fg-3 tabular" data-count="">
              {items.length}+
            </span>
          ) : null
        }
        items={[
          <span key="what">Enrollment needs approval</span>,
          list.dataUpdatedAt > 0 ? (
            <time key="at" dateTime={new Date(list.dataUpdatedAt).toISOString()} data-freshness="">
              Updated {formatTimeUtc(new Date(list.dataUpdatedAt))}
            </time>
          ) : null,
        ]}
        className={picked ? "max-lg:hidden" : undefined}
      />

      {list.isPending ? (
        <ReviewSkeleton />
      ) : list.error ? (
        <ErrorState
          title="The review queue did not load"
          error={list.error}
          onRetry={() => list.refetch()}
        />
      ) : empty ? (
        <EmptyQueue />
      ) : (
        <div className="grid min-w-0 items-start gap-6 lg:grid-cols-[15rem_minmax(0,1fr)]">
          <nav
            aria-label="Review queue"
            className={cn(
              "flex min-w-0 flex-col gap-2 lg:sticky lg:top-4",
              picked && "max-lg:hidden",
            )}
          >
            <div className="flex items-center justify-between px-1 text-small text-fg-3">
              <span>Oldest first</span>
              <span aria-hidden className="flex gap-1 max-lg:hidden">
                <Kbd>J</Kbd>
                <Kbd>K</Kbd>
              </span>
            </div>
            <ul className="flex flex-col gap-1">
              {items.map((t) => (
                <li key={t.id}>
                  <QueueItem
                    t={t}
                    now={now}
                    current={t.id === selected?.id}
                    onPick={() => pick(t.id)}
                  />
                </li>
              ))}
            </ul>
            {list.hasNextPage ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => list.fetchNextPage()}
                pending={list.isFetchingNextPage}
                className="max-lg:h-(--vs-control-touch)"
              >
                {list.isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            ) : null}
          </nav>
          {selected ? (
            <div className={cn("min-w-0", !picked && "max-lg:hidden")}>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => pick(null)}
                className="mb-3 h-(--vs-control-touch) lg:hidden"
              >
                <ArrowLeft aria-hidden />
                Back to the queue
              </Button>
              <SelectedCase
                key={selected.id}
                summary={selected}
                onDecided={() => decided(selected)}
              />
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}

function QueueItem({
  t,
  now,
  current,
  onPick,
}: {
  t: OperatorTenantSummary;
  now: number;
  current: boolean;
  onPick: () => void;
}) {
  const pending = t.status === "pending";
  return (
    <button
      type="button"
      aria-current={current ? "true" : undefined}
      aria-keyshortcuts="J K"
      data-tenant={t.id}
      onClick={onPick}
      className={cn(
        "flex w-full min-w-0 flex-col gap-1 rounded-xl border px-3.5 py-3 text-left transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-(--vs-control-touch)",
        current ? "border-line-2 bg-raised" : "border-transparent hover:bg-hover",
      )}
    >
      <span className="min-w-0 truncate text-body font-semibold text-fg">{t.name}</span>
      <span className="flex min-w-0 items-baseline justify-between gap-2 text-small text-fg-3">
        <span className="min-w-0 truncate">
          {pending
            ? "New project"
            : t.sites_in_review === 1
              ? "Site in review"
              : `${countOf(t.sites_in_review, "site")} in review`}
        </span>
        <time dateTime={t.status_changed_at} className="shrink-0 tabular">
          {waited(t.status_changed_at, now)}
        </time>
      </span>
    </button>
  );
}

// the panel of one case: the document scrolls, the decision bar closes it
function CaseCard({
  label,
  children,
  bar,
}: {
  label: string;
  children: ReactNode;
  bar: ReactNode;
}) {
  return (
    <article
      aria-label={label}
      className="flex min-w-0 flex-col overflow-hidden rounded-2xl border border-line bg-raised"
    >
      <div className="flex min-w-0 flex-col gap-7 px-5 pt-7 pb-8 sm:px-10 sm:pt-9">{children}</div>
      {bar ? (
        <div className="border-t border-line bg-raised px-5 py-3.5 max-lg:sticky max-lg:bottom-0 sm:px-6">
          {bar}
        </div>
      ) : null}
    </article>
  );
}

function Label({ children, id }: { children: ReactNode; id?: string }) {
  return (
    <h3 id={id} className="text-small font-medium text-fg-3">
      {children}
    </h3>
  );
}

function OpenProject({ id }: { id: string }) {
  return (
    <Link
      to={`/operator/projects/${id}`}
      className="rounded-sm text-small text-fg-2 underline-offset-4 hover:text-fg hover:underline focus-ring"
    >
      Open the project
    </Link>
  );
}

function SelectedCase({
  summary,
  onDecided,
}: {
  summary: OperatorTenantSummary;
  onDecided: () => void;
}) {
  const detail = useQuery(operatorTenantQuery(summary.id));
  return (
    <AnimatePresence initial={false} mode="wait">
      {detail.isPending ? (
        <motion.div key="loading" {...fade}>
          <LoadingState label={`Loading ${summary.name}`}>
            <Skeleton.Table rows={6} columns={1} />
          </LoadingState>
        </motion.div>
      ) : detail.error ? (
        <motion.div key="error" {...fade}>
          <ErrorState
            title={`${summary.name} did not load`}
            error={detail.error}
            onRetry={() => detail.refetch()}
          />
        </motion.div>
      ) : (
        <motion.div key="data" {...fade} className="flex min-w-0 flex-col gap-6">
          {detail.data.tenant.status === "pending" ||
          !detail.data.sites.some((s) => s.in_review) ? (
            <ProjectCase detail={detail.data} onDecided={onDecided} />
          ) : (
            detail.data.sites
              .filter((s) => s.in_review)
              .map((s) => (
                <SiteCase key={s.id} site={s} detail={detail.data} onDecided={onDecided} />
              ))
          )}
        </motion.div>
      )}
    </AnimatePresence>
  );
}

function enrolledBy(detail: OperatorTenantDetail): ReactNode {
  const t = detail.tenant;
  const who = t.created_by;
  const member = who ? detail.members.find((m) => m.display_name === who.display_name) : null;
  const via = who?.github_login
    ? "GitHub"
    : who?.stellar_address
      ? "Stellar wallet"
      : who?.passkey_address
        ? "passkey"
        : null;
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-3">
      <p className="flex min-w-0 items-center gap-2.5 text-body text-fg-2">
        {who ? <Avatar name={who.display_name} className="size-6" /> : null}
        <span className="min-w-0 break-words">
          {who ? <b className="font-semibold text-fg">{who.display_name}</b> : "Enrolled"}{" "}
          {who ? "enrolled it " : ""}
          <time dateTime={t.created_at}>{formatDateTimeShort(t.created_at)}</time>
        </span>
      </p>
      <LabeledFacts
        items={[
          ...(via ? [{ label: "Signed in with", value: via }] : []),
          ...(member ? [{ label: "Role", value: member.role }] : []),
          {
            label: "Terms",
            value: t.accepted_terms_version
              ? termsVersionLabel(t.accepted_terms_version)
              : "Not accepted",
          },
          {
            label: "Dashboards",
            value: t.provisioning === "ready" ? "Set up" : "Not set up yet",
          },
        ]}
      />
    </div>
  );
}

// a new project: its name and website, how it uses stellar in large text,
// the checks as sentences, who enrolled it, then the decision
function ProjectCase({
  detail,
  onDecided,
}: {
  detail: OperatorTenantDetail;
  onDecided: () => void;
}) {
  const t = detail.tenant;
  const decidable = detail.allowed_actions.some((a) => a === "approve" || a === "reject");
  return (
    <CaseCard
      label={`Review ${t.name}`}
      bar={
        decidable ? (
          <DecisionPanel
            globalKeys
            subject={{ kind: "project", id: t.id, name: t.name, allowed: detail.allowed_actions }}
            lead={<OpenProject id={t.id} />}
            onDecided={onDecided}
          />
        ) : (
          <OpenProject id={t.id} />
        )
      }
    >
      <header className="flex min-w-0 flex-col gap-1.5">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <h2 className="text-page leading-9 font-semibold tracking-tight break-words text-fg">
            {t.name}
          </h2>
          <StatusBadge kind="project" status={t.status} operator />
        </div>
        {t.description ? <p className="text-body break-words text-fg-2">{t.description}</p> : null}
        <span className="font-mono text-small break-all text-fg-2">
          {t.enrollment_answers.website}
        </span>
      </header>
      <section aria-labelledby="usage-h" className="flex min-w-0 flex-col gap-2">
        <Label id="usage-h">How it uses Stellar</Label>
        <p className="max-w-[46rem] text-section leading-7.25 break-words whitespace-pre-line text-fg font-normal">
          {t.enrollment_answers.stellar_usage}
        </p>
      </section>
      <section aria-labelledby="checks-h" className="flex min-w-0 flex-col gap-1">
        <Label id="checks-h">Checks</Label>
        <ChecksList label="Checks" items={projectChecks(detail)} className="max-w-[46rem]" />
      </section>
      {enrolledBy(detail)}
    </CaseCard>
  );
}

// a site on a domain of a removed project: the site, why it waits, its
// checks, then the project around it
function SiteCase({
  site,
  detail,
  onDecided,
}: {
  site: OperatorSite;
  detail: OperatorTenantDetail;
  onDecided: () => void;
}) {
  const t = detail.tenant;
  const numbers = useProjectNumbers(t.id);
  const others = detail.sites.filter((s) => s.id !== site.id);
  const otherVerified = others.filter((s) => s.verification === "verified").length;
  return (
    <CaseCard
      label={`Review ${site.domain}`}
      bar={
        <DecisionPanel
          globalKeys
          subject={{ kind: "site", id: site.id, name: site.domain }}
          lead={<OpenProject id={t.id} />}
          onDecided={onDecided}
        />
      }
    >
      <header className="flex min-w-0 flex-col gap-1.5">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <h2 className="text-page leading-9 font-semibold tracking-tight break-all text-fg">
            {site.domain}
          </h2>
          <Pill tone="warning">Site in review</Pill>
        </div>
        <p className="text-body text-fg-2">
          A new site of <span className="text-fg">{t.name}</span>, added{" "}
          <time dateTime={site.created_at}>{formatDate(site.created_at)}</time>
        </p>
      </header>
      {site.domain_previously_removed ? (
        <section aria-labelledby={`why-${site.id}`} className="flex min-w-0 flex-col gap-2">
          <Label id={`why-${site.id}`}>Why it waits</Label>
          <p className="max-w-[46rem] text-section leading-7.25 text-fg font-normal">
            This domain, or a parent or subdomain of it, belonged to a project that was removed.
          </p>
        </section>
      ) : null}
      <section aria-labelledby={`sc-${site.id}`} className="flex min-w-0 flex-col gap-1">
        <Label id={`sc-${site.id}`}>Checks</Label>
        <ChecksList label="Checks" items={siteChecks(site)} className="max-w-[46rem]" />
      </section>
      <section aria-labelledby={`pr-${site.id}`} className="flex min-w-0 flex-col gap-1">
        <Label id={`pr-${site.id}`}>{t.name}</Label>
        <ChecksList
          label={t.name}
          className="max-w-[46rem]"
          items={[
            {
              key: "status",
              status: "info",
              children: (
                <>
                  {statusEntry({ kind: "project", status: t.status, operator: true }).word} since{" "}
                  {formatDate(t.status_changed_at)},{" "}
                  {others.length === 0
                    ? "no other site"
                    : `${countOf(otherVerified, "other site")} verified of ${others.length}`}
                </>
              ),
            },
            {
              key: "usage",
              status: "info",
              children: t.enrollment_answers.stellar_usage,
            },
          ]}
        />
      </section>
      <ProjectNumbers tenantId={t.id} numbers={numbers} />
    </CaseCard>
  );
}

// ---- empty queue: the recent decisions

const DECISIONS = ["tenant.approve", "tenant.reject", "site.approve", "site.reject"] as const;

function useDecisionPage(action: string) {
  const q = useInfiniteQuery(
    auditQuery({ tenant_id: "", actor: "", target: "", q: "", from: "", to: "", action }),
  );
  return { data: q.data?.pages[0]?.items, isPending: q.isPending, error: q.error };
}

function useRecentDecisions() {
  const all = [
    useDecisionPage(DECISIONS[0]),
    useDecisionPage(DECISIONS[1]),
    useDecisionPage(DECISIONS[2]),
    useDecisionPage(DECISIONS[3]),
  ];
  const rows = all
    .flatMap((q) => q.data ?? [])
    .sort((x, y) => y.at.localeCompare(x.at))
    .slice(0, 10);
  return {
    rows,
    pending: all.some((q) => q.isPending),
    error: all.every((q) => q.error) ? all[0]?.error : null,
  };
}

function EmptyQueue() {
  const recent = useRecentDecisions();
  const today = new Date().toISOString().slice(0, 10);
  const columns: Column<AuditEntry>[] = [
    {
      key: "at",
      header: "When",
      cell: (e) => (
        <time dateTime={e.at} title={formatDateTimeShort(e.at)} className="text-fg-3 tabular">
          {e.at.slice(0, 10) === today
            ? formatTimeUtc(e.at).replace(" UTC", "")
            : formatDate(e.at).replace(/, \d{4}$/, "")}
        </time>
      ),
    },
    {
      key: "what",
      header: "Project or site",
      primary: true,
      cell: (e) =>
        e.target.startsWith("tenant:") ? (
          <Link
            to={`/operator/projects/${e.target.slice(7)}`}
            className="rounded-sm font-medium text-fg underline-offset-4 hover:underline focus-ring"
          >
            {e.target_display_name ?? e.target}
          </Link>
        ) : (
          <span className="font-medium text-fg">{e.target_display_name ?? e.target}</span>
        ),
    },
    {
      key: "decision",
      header: "Decision",
      cell: (e) => {
        const ok = e.action.endsWith(".approve");
        const site = e.action.startsWith("site.");
        return (
          <Pill tone={ok ? "success" : "critical"}>
            {ok ? (site ? "Site approved" : "Approved") : site ? "Site rejected" : "Rejected"}
          </Pill>
        );
      },
    },
    {
      key: "by",
      header: "By",
      cell: (e) => <span className="text-fg-2">{e.actor_display_name ?? e.actor}</span>,
    },
    {
      key: "reason",
      header: "Reason",
      cell: (e) => <span className="text-fg-2">{auditReason(e) ?? ""}</span>,
    },
  ];
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <p className="flex items-center gap-2.5 text-body text-fg" role="status">
        <Check aria-hidden className="size-4 text-success" strokeWidth={2.6} />
        Nothing waits for a decision
      </p>
      <section aria-labelledby="recent-h" className="flex min-w-0 flex-col gap-3">
        <div className="flex items-baseline justify-between gap-3">
          <h2 id="recent-h" className="text-title font-semibold text-fg leading-5">
            Recent decisions
          </h2>
          <Link
            to="/operator/audit?action=tenant."
            className="rounded-sm text-small text-fg underline-offset-4 hover:underline focus-ring"
          >
            Audit log
          </Link>
        </div>
        <DataTable
          label="Recent decisions"
          columns={columns}
          rows={recent.rows}
          getRowKey={(e) => String(e.id)}
          state={recent.pending ? "loading" : recent.error ? "error" : "data"}
          error={recent.error}
          empty={{ title: "No decision yet" }}
        />
      </section>
    </div>
  );
}
