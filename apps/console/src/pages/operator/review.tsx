import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import { ArrowRight, Check, CircleCheck, Minus } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { api } from "@/api/endpoints";
import { operatorAlertsQuery, operatorTenantQuery, operatorTenantsQuery } from "@/api/queries";
import type { OperatorSite, OperatorTenantSummary } from "@/api/schemas";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { AnimatedNumber, Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { Kbd } from "@/components/panel";
import { ReasonDialog } from "@/components/reason-dialog";
import { StatusDot, StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { formatCount } from "@/lib/format";
import { AlertItem, AlertsError, LiveCheck, PipelineDown, useNow } from "./alerts";
import {
  ageText,
  DecisionPanel,
  EvidenceList,
  enrolledLine,
  evidenceOf,
  refreshOperator,
  Waiting,
} from "./decision";

// contract 5.3 review=true: pending projects and active projects with a site
// in review, oldest first so nobody waits longest
const QUEUE: TenantListQuery = {
  status: [],
  review: true,
  q: "",
  sort: "created_at",
  order: "asc",
};

// the operator's workspace (system v2 F-Review): alerts that need action
// first, then the queue on the left and the selected project with its
// evidence and the decision on the right
export function ReviewQueuePage() {
  return (
    <div className="flex flex-col gap-8">
      <AttentionAlerts />
      <ReviewQueue />
    </div>
  );
}

// ---- alerts that need action

function AttentionAlerts() {
  const alerts = useQuery(operatorAlertsQuery);
  const now = useNow(15_000);
  // held back and inhibited alerts send nothing, so they wait on the alerts tab
  const acting = alerts.data?.alerts.filter((a) => a.state !== "suppressed") ?? [];
  return (
    <section aria-labelledby="attention-alerts-h" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 id="attention-alerts-h" className="flex items-baseline gap-2 text-section text-fg">
          Alerts
          {alerts.data && !alerts.error ? (
            <AnimatedNumber
              value={acting.length}
              format={(n) => formatCount(Math.round(n))}
              className="font-mono text-body font-normal text-fg-3"
            />
          ) : null}
        </h2>
        <div className="flex flex-wrap items-center gap-3">
          <LiveCheck alerts={alerts} />
          <Button asChild variant="ghost" size="sm" className="max-lg:h-(--vs-control-touch)">
            <Link to="/operator/alerts">
              All alerts
              <ArrowRight aria-hidden />
            </Link>
          </Button>
        </div>
      </div>
      {alerts.isPending ? (
        <Loading label="Loading the alerts" rows={1} />
      ) : alerts.error ? (
        <AlertsError
          error={alerts.error}
          lastSuccess={alerts.dataUpdatedAt}
          onRetry={() => alerts.refetch()}
        />
      ) : (
        <>
          {alerts.data.watchdog_firing ? null : <PipelineDown />}
          {acting.length === 0 ? (
            <p className="flex items-center gap-2 rounded-lg border border-line bg-raised px-4 py-3 text-small text-fg-2">
              <CircleCheck aria-hidden className="size-4 shrink-0 text-success" />
              No alert needs action.
            </p>
          ) : (
            <Stagger as="ul" className="grid gap-3 xl:grid-cols-2">
              {acting.map((a) => (
                <StaggerItem as="li" key={a.fingerprint}>
                  <AlertItem alert={a} now={now} />
                </StaggerItem>
              ))}
            </Stagger>
          )}
        </>
      )}
    </section>
  );
}

// ---- the queue

function ReviewQueue() {
  const list = useInfiniteQuery(operatorTenantsQuery(QUEUE));
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const [picked, setPicked] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const now = useNow(60_000);
  // the picked project, or the oldest one when it left the queue
  const selected = items.find((t) => t.id === picked) ?? items[0] ?? null;

  // j and k move through the queue, as in a mail client
  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      if (el?.closest("input, textarea, select, [contenteditable=true]")) return;
      if (e.key !== "j" && e.key !== "k") return;
      const i = selected ? items.findIndex((t) => t.id === selected.id) : -1;
      const next = items[Math.min(items.length - 1, Math.max(0, i + (e.key === "j" ? 1 : -1)))];
      if (next) {
        e.preventDefault();
        setPicked(next.id);
        setDone(null);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [items, selected]);

  function decided(t: OperatorTenantSummary, choice: "approve" | "reject") {
    const i = items.findIndex((x) => x.id === t.id);
    const next = items[i + 1] ?? items[i - 1] ?? null;
    setPicked(next?.id ?? null);
    setDone(
      `${choice === "approve" ? "Approved" : "Rejected"} ${t.name}.${next ? ` Next in queue: ${next.name}.` : " The queue is empty."}`,
    );
  }

  return (
    <section aria-labelledby="review-h" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 id="review-h" className="flex items-baseline gap-2 text-section text-fg">
          Review queue
          {list.data ? (
            <span className="font-mono text-body font-normal text-fg-3">
              {formatCount(items.length)}
              {list.hasNextPage ? "+" : ""}
            </span>
          ) : null}
        </h2>
        <p className="flex items-center gap-1.5 text-caption text-fg-3">
          Projects waiting for approval, and active projects with a site whose domain belonged to a
          removed project. Oldest first.
          <span className="max-lg:hidden">
            <Kbd>J</Kbd> <Kbd>K</Kbd> move.
          </span>
        </p>
      </div>

      {done ? (
        <Notice tone="success" icon={<Check aria-hidden />}>
          {done}
        </Notice>
      ) : null}

      {list.isPending ? (
        <Loading label="Loading the review queue" />
      ) : list.error ? (
        <ErrorPanel error={list.error} onRetry={() => list.refetch()} />
      ) : items.length === 0 ? (
        <StateBlock kind="empty" icon={<CircleCheck aria-hidden />} title="Nothing to review">
          New enrollments and sites on a previously removed domain appear here.
        </StateBlock>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)] xl:grid-cols-[22rem_minmax(0,1fr)]">
          <div className="flex flex-col gap-2 lg:sticky lg:top-4">
            <Stagger
              as="ul"
              className="flex flex-col overflow-hidden rounded-lg border border-line bg-raised"
            >
              {items.map((t) => (
                <StaggerItem
                  as="li"
                  key={t.id}
                  className="border-b border-line-subtle last:border-0"
                >
                  <QueueItem
                    t={t}
                    now={now}
                    current={t.id === selected?.id}
                    onPick={() => {
                      setPicked(t.id);
                      setDone(null);
                    }}
                  />
                </StaggerItem>
              ))}
            </Stagger>
            {list.hasNextPage ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => list.fetchNextPage()}
                pending={list.isFetchingNextPage}
              >
                {list.isFetchingNextPage ? "Loading" : "Load more"}
              </Button>
            ) : null}
          </div>
          {selected ? (
            <SelectedProject
              key={selected.id}
              summary={selected}
              now={now}
              onDecided={(c) => decided(selected, c)}
            />
          ) : null}
        </div>
      )}
    </section>
  );
}

function Chip({ ok, children }: { ok: boolean; children: string }) {
  return (
    <span className="flex items-center gap-1">
      {ok ? (
        <Check aria-hidden className="size-3 text-success" strokeWidth={2.8} />
      ) : (
        <Minus aria-hidden className="size-3 text-fg-3" strokeWidth={2.8} />
      )}
      {children}
      <span className="sr-only">{ok ? " yes" : " no"}</span>
    </span>
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
      onClick={onPick}
      className={cn(
        "flex w-full flex-col gap-1 px-3.5 py-3 text-left transition-colors duration-(--vs-duration-press) ease-standard focus-ring",
        current ? "bg-selected" : "hover:bg-hover",
      )}
    >
      <span className="flex items-baseline justify-between gap-2">
        <span className="truncate text-body font-medium text-fg">{t.name}</span>
        {pending ? (
          <span className="shrink-0 font-mono text-caption text-fg-3">
            {ageText(t.status_changed_at, now)}
          </span>
        ) : null}
      </span>
      <span className={cn("text-caption", pending ? "text-warning" : "text-fg-2")}>
        {pending
          ? "New project"
          : `${t.sites_in_review} ${t.sites_in_review === 1 ? "site" : "sites"} in review`}
      </span>
      <span className="flex flex-wrap gap-x-2.5 gap-y-1 text-caption text-fg-2">
        <Chip ok={t.sites_verified > 0}>Domain</Chip>
        <Chip ok={t.sites_with_stellar_toml > 0}>toml</Chip>
        <span className="text-fg-3">
          {t.site_count} {t.site_count === 1 ? "site" : "sites"}
        </span>
      </span>
    </button>
  );
}

// ---- the selected project

function SelectedProject({
  summary,
  now,
  onDecided,
}: {
  summary: OperatorTenantSummary;
  now: number;
  onDecided: (c: "approve" | "reject") => void;
}) {
  const detail = useQuery(operatorTenantQuery(summary.id));
  return (
    <section
      aria-label={`Review ${summary.name}`}
      className="flex min-w-0 flex-col rounded-lg border border-line bg-raised"
    >
      <AnimatePresence initial={false} mode="wait">
        {detail.isPending ? (
          <motion.div key="loading" {...fade} className="px-5 py-4">
            <Loading label={`Loading ${summary.name}`} />
          </motion.div>
        ) : detail.error ? (
          <motion.div key="error" {...fade} className="p-4">
            <ErrorPanel error={detail.error} onRetry={() => detail.refetch()} />
          </motion.div>
        ) : (
          <motion.div key="data" {...fade} className="flex flex-col">
            <header className="flex flex-col gap-1.5 border-b border-line-subtle px-5 pt-4 pb-3.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                {detail.data.tenant.status === "pending" ? (
                  <Waiting since={detail.data.tenant.status_changed_at} now={now} />
                ) : (
                  <StatusPill status={detail.data.tenant.status} />
                )}
                <Button asChild variant="ghost" size="xs">
                  <Link to={`/operator/tenants/${summary.id}`}>
                    Open project page
                    <ArrowRight aria-hidden />
                  </Link>
                </Button>
              </div>
              <h3 className="text-page text-fg">{detail.data.tenant.name}</h3>
              <p className="text-small text-fg-2">
                {enrolledLine(detail.data.tenant)}{" "}
                <span className="break-all">{detail.data.tenant.enrollment_answers.website}</span>
              </p>
            </header>
            <div className="flex flex-col gap-1 border-b border-line-subtle px-5 py-3.5">
              <span className="pb-1 text-caption text-fg-3">EVIDENCE</span>
              <EvidenceList items={evidenceOf(detail.data)} />
            </div>
            <div className="flex flex-col gap-1.5 border-b border-line-subtle px-5 py-3.5">
              <span className="text-caption text-fg-3">HOW IT USES STELLAR</span>
              <p className="whitespace-pre-wrap break-words text-body text-fg">
                {detail.data.tenant.enrollment_answers.stellar_usage}
              </p>
              {detail.data.tenant.description ? (
                <p className="whitespace-pre-wrap break-words text-small text-fg-2">
                  {detail.data.tenant.description}
                </p>
              ) : null}
            </div>
            {detail.data.sites.some((s) => s.in_review) ? (
              <div className="flex flex-col gap-2 border-b border-line-subtle px-5 py-3.5">
                <span className="text-caption text-fg-3">SITES IN REVIEW</span>
                <ul className="flex flex-col gap-2">
                  {detail.data.sites
                    .filter((s) => s.in_review)
                    .map((s) => (
                      <li key={s.id}>
                        <SiteReview site={s} />
                      </li>
                    ))}
                </ul>
              </div>
            ) : null}
            <div className="px-5 pt-4 pb-5">
              {detail.data.allowed_actions.some((a) => a === "approve" || a === "reject") ? (
                <DecisionPanel detail={detail.data} onDecided={onDecided} />
              ) : (
                <p className="text-small text-fg-2">
                  The project is {detail.data.tenant.status}; only its sites in review wait for a
                  decision.
                </p>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </section>
  );
}

// a site on a domain of a removed project, decided on its own
function SiteReview({ site }: { site: OperatorSite }) {
  const [action, setAction] = useState<"approve" | "reject" | null>(null);
  const run = useMutation({
    mutationFn: ({ act, reason }: { act: "approve" | "reject"; reason: string }) =>
      api.operator.siteAction(site.id, act, reason),
    onSuccess: async () => {
      setAction(null);
      await refreshOperator();
    },
  });
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-line px-3 py-2.5">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="flex items-center gap-2 font-mono text-small text-fg">
          <StatusDot tone="warning" />
          <span className="break-all">{site.domain}</span>
        </span>
        <span className="text-caption text-fg-2">
          {site.domain_previously_removed
            ? "This domain, a parent or a subdomain of it, belonged to a removed project."
            : "In review."}
        </span>
      </div>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={() => setAction("reject")}>
          Reject site
        </Button>
        <Button size="sm" onClick={() => setAction("approve")}>
          Approve site
        </Button>
      </div>
      <ReasonDialog
        open={action !== null}
        onOpenChange={(o) => {
          if (!o) {
            setAction(null);
            run.reset();
          }
        }}
        title={action === "approve" ? `Approve ${site.domain}?` : `Reject ${site.domain}?`}
        description={
          action === "approve"
            ? "The site becomes active and starts receiving traffic."
            : "The site is removed. Its owners see the reason."
        }
        reasonRequired={action === "reject"}
        confirmLabel={action === "approve" ? "Approve site" : "Reject site"}
        pendingLabel={action === "approve" ? "Approving" : "Rejecting"}
        tone={action === "approve" ? "primary" : "destructive"}
        pending={run.isPending}
        error={run.error}
        onConfirm={(reason) => action && run.mutate({ act: action, reason })}
      />
    </div>
  );
}
