import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { fade } from "@veristar/brand/motion";
import {
  ArrowLeft,
  ArrowRight,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CircleX,
} from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect } from "react";
import { Link, useSearchParams } from "react-router";
import type { TenantListQuery } from "@/api/endpoints";
import { operatorTenantQuery, operatorTenantsQuery } from "@/api/queries";
import type { OperatorSite, OperatorTenantSummary } from "@/api/schemas";
import { DecisionPanel } from "@/components/decision-panel";
import { EvidenceList, evidenceOf } from "@/components/evidence-list";
import { PageHeader } from "@/components/page-header";
import { EmptyState, ErrorState, LoadingState, Skeleton } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { countOf, formatCount } from "@/lib/format";
import { ReviewSkeleton } from "@/pages/operator/page-frame";
import { useNow } from "./alerts";
import { ageText, DecisionSection, enrolledLine, Waiting } from "./decision";
import { ProjectNumbers, useProjectNumbers, withNumbers } from "./numbers";
import { Answers } from "./project";

// contract 5.3 review=true: pending projects and active projects with a site
// in review, oldest first so nobody waits longest
export const REVIEW_QUEUE: TenantListQuery = {
  status: [],
  review: true,
  q: "",
  sort: "created_at",
  order: "asc",
};

// the operator's queue: the projects that wait on the left, the selected one
// in the middle with its evidence and enrollment answers first (the facts a
// decision on a new project rests on) and its figures only when it has
// data, and the decision in a column that stays in view. on a phone the
// selected project is its own screen with the decision at the bottom. the
// selection is in the address (?project=), so a project can be shared.
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
      const el = e.target as HTMLElement | null;
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

  // after a decision the next project in the queue opens
  function decided(t: OperatorTenantSummary) {
    const i = items.findIndex((x) => x.id === t.id);
    const next = items[i + 1] ?? items[i - 1] ?? null;
    setParams(next ? { project: next.id } : {}, { replace: true });
  }

  const count = list.data ? `${formatCount(items.length)}${list.hasNextPage ? "+" : ""}` : null;

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="Review"
        meta={
          count ? (
            <span className="text-body text-fg-2 tabular" data-count="">
              {count}
            </span>
          ) : null
        }
        freshness={list.dataUpdatedAt > 0 ? { at: new Date(list.dataUpdatedAt) } : null}
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
      ) : items.length === 0 ? (
        <EmptyState icon={<CircleCheck aria-hidden />} title="Nothing to review">
          New projects show here. Sites on a domain of a removed project show here too.
        </EmptyState>
      ) : (
        <div className="grid min-w-0 items-start gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
          <nav
            aria-label="Review queue"
            className={cn(
              "flex min-w-0 flex-col gap-2 lg:sticky lg:top-4",
              picked && "max-lg:hidden",
            )}
          >
            <ul className="flex flex-col overflow-hidden rounded-lg border border-line bg-raised">
              {items.map((t) => (
                <li key={t.id} className="border-b border-line-subtle last:border-0">
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
              <SelectedProject
                key={selected.id}
                summary={selected}
                now={now}
                onDecided={() => decided(selected)}
              />
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}

// one status per fact, as in the evidence list: an icon, a colour and the
// state in words
const FACT = {
  ok: { Icon: CircleCheck, tone: "text-success" },
  partial: { Icon: CircleAlert, tone: "text-warning" },
  missing: { Icon: CircleX, tone: "text-critical" },
  absent: { Icon: CircleDashed, tone: "text-fg-3" },
} as const;

function Fact({
  part,
  all,
  words,
}: {
  part: number;
  all: number;
  // the words for all, some and none of the sites
  words: [string, string, string];
}) {
  const state = all === 0 ? "absent" : part === 0 ? "missing" : part < all ? "partial" : "ok";
  const { Icon, tone } = FACT[state];
  const text =
    state === "absent"
      ? "No site yet"
      : state === "ok"
        ? words[0]
        : state === "partial"
          ? `${words[1]} ${formatCount(part)} of ${formatCount(all)}`
          : words[2];
  return (
    <span className="inline-flex items-center gap-1" data-fact={state}>
      <Icon aria-hidden className={cn("size-3", tone)} strokeWidth={2.6} />
      {text}
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
      aria-keyshortcuts="J K"
      // the keys move in the queue; the decision's keys sit on its buttons
      title="J / K"
      data-tenant={t.id}
      onClick={onPick}
      className={cn(
        "flex w-full min-w-0 flex-col gap-1 px-4 py-3 text-left transition-colors duration-(--vs-duration-press) ease-standard focus-ring max-lg:min-h-(--vs-control-touch)",
        current ? "bg-selected" : "hover:bg-hover",
      )}
    >
      <span className="flex min-w-0 items-baseline justify-between gap-2">
        <span className="min-w-0 truncate text-body font-medium text-fg">{t.name}</span>
        <span className="shrink-0 text-caption text-fg-2 tabular">
          {ageText(t.status_changed_at, now)}
        </span>
      </span>
      <span className="text-caption text-fg-2">
        {pending ? "New project" : `${countOf(t.sites_in_review, "site")} in review`}
      </span>
      <span className="flex flex-wrap gap-x-3 gap-y-1 text-caption text-fg-2">
        {t.site_count === 0 ? (
          <Fact part={0} all={0} words={["", "", ""]} />
        ) : (
          <>
            <Fact
              part={t.sites_verified}
              all={t.site_count}
              words={["Domain verified", "Domain verified on", "Domain not verified"]}
            />
            <Fact
              part={t.sites_with_stellar_toml}
              all={t.site_count}
              words={["stellar.toml found", "stellar.toml on", "No stellar.toml"]}
            />
          </>
        )}
      </span>
    </button>
  );
}

function SelectedProject({
  summary,
  now,
  onDecided,
}: {
  summary: OperatorTenantSummary;
  now: number;
  onDecided: () => void;
}) {
  const detail = useQuery(operatorTenantQuery(summary.id));
  const numbers = useProjectNumbers(summary.id);
  return (
    <section aria-label={`Review ${summary.name}`} className="flex min-w-0 flex-col">
      <AnimatePresence initial={false} mode="wait">
        {detail.isPending ? (
          <motion.div key="loading" {...fade}>
            <LoadingState label={`Loading ${summary.name}`}>
              <Skeleton.Table rows={5} columns={2} />
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
          <motion.div
            key="data"
            {...fade}
            className="grid min-w-0 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_22rem]"
          >
            <div className="flex min-w-0 flex-col gap-6">
              <header className="flex min-w-0 flex-col gap-1">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  {detail.data.tenant.status === "pending" ? (
                    <Waiting since={detail.data.tenant.status_changed_at} now={now} />
                  ) : (
                    <StatusBadge kind="project" status={detail.data.tenant.status} operator />
                  )}
                  <Link
                    to={`/operator/projects/${summary.id}`}
                    className="inline-flex items-center gap-1 rounded-sm text-small text-fg underline-offset-4 hover:underline focus-ring"
                  >
                    Open the project page
                    <ArrowRight aria-hidden className="size-3.5" />
                  </Link>
                </div>
                <h2 className="text-heading break-words text-fg">{detail.data.tenant.name}</h2>
                <p className="text-small break-words text-fg-2">
                  {enrolledLine(detail.data.tenant)}
                </p>
              </header>

              <section aria-labelledby="evidence-h" className="flex min-w-0 flex-col gap-2">
                <h2 id="evidence-h" className="text-title text-fg">
                  Evidence
                </h2>
                <EvidenceList items={withNumbers(evidenceOf(detail.data), numbers)} />
              </section>

              <Answers detail={detail.data} />

              {detail.data.sites.some((s) => s.in_review) ? (
                <section aria-labelledby="sites-review-h" className="flex flex-col gap-3">
                  <h2 id="sites-review-h" className="text-title text-fg">
                    Sites in review
                  </h2>
                  <ul className="flex flex-col gap-3">
                    {detail.data.sites
                      .filter((s) => s.in_review)
                      .map((s) => (
                        <li key={s.id}>
                          <SiteReview site={s} />
                        </li>
                      ))}
                  </ul>
                </section>
              ) : null}

              <ProjectNumbers tenantId={summary.id} numbers={numbers} />
            </div>

            {detail.data.allowed_actions.some((a) => a === "approve" || a === "reject") ? (
              <div className="min-w-0 xl:sticky xl:top-4">
                <DecisionSection>
                  <DecisionPanel
                    subject={{
                      kind: "project",
                      id: detail.data.tenant.id,
                      name: detail.data.tenant.name,
                      allowed: detail.data.allowed_actions,
                    }}
                    onDecided={onDecided}
                  />
                </DecisionSection>
              </div>
            ) : null}
          </motion.div>
        )}
      </AnimatePresence>
    </section>
  );
}

// a site on a domain of a removed project, decided on its own
function SiteReview({ site }: { site: OperatorSite }) {
  return (
    <div className="flex min-w-0 flex-col gap-3 rounded-lg border border-line bg-raised p-4">
      <div className="flex min-w-0 flex-col gap-1">
        <span className="font-mono text-small break-all text-fg">{site.domain}</span>
        {site.domain_previously_removed ? (
          <span className="text-caption text-warning">
            This domain, or a parent or subdomain of it, belonged to a removed project.
          </span>
        ) : null}
      </div>
      <DecisionPanel subject={{ kind: "site", id: site.id, name: site.domain }} />
    </div>
  );
}
