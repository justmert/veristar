import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { CircleAlert, Plus } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Link, redirect } from "react-router";
import type { Kpi } from "@/api/cohort-schemas";
import { type SCFView, scfViewQuery } from "@/api/onchain";
import { meQuery } from "@/api/queries";
import type { Me, MeTenant, RemovedTenant } from "@/api/schemas";
import { FilterBar } from "@/components/filter-bar";
import { GradeRing } from "@/components/grade-ring";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { SetupBar, type SetupStep } from "@/components/setup-progress";
import { ChangeLine, type StatChange } from "@/components/stat-card";
import { EmptyState, ErrorState } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useDemo } from "@/lib/demo";
import { formatChange, formatCompact } from "@/lib/format";
import { queryClient } from "@/query-client";

// an operator who is a member of no project starts on the operator review
// page; the project list would only offer to enroll one
export function homeRedirect(me: Me): string | null {
  return me.user.is_operator && me.tenants.length === 0 && me.removed_tenants.length === 0
    ? "/operator"
    : null;
}

export async function projectsLoader() {
  const to = homeRedirect(await queryClient.ensureQueryData(meQuery));
  if (to) throw redirect(to);
  return null;
}

// from this many projects the list gets a search field
export const SEARCH_FROM = 7;

// the projects whose name or address holds the search text, in any case
export function matchProjects(list: MeTenant[], q: string): MeTenant[] {
  const t = q.trim().toLowerCase();
  if (!t) return list;
  return list.filter((p) => p.name.toLowerCase().includes(t) || p.slug.includes(t));
}

function EnrollButton() {
  return (
    <Button asChild className="max-sm:h-(--vs-control-touch)">
      <Link to="/projects/new">
        <Plus aria-hidden />
        Enroll a project
      </Link>
    </Button>
  );
}

export function ProjectsPage() {
  const me = useQuery(meQuery);
  const [q, setQ] = useState("");
  // the cards whose numbers did not load, with how to load them again
  const [failed, setFailed] = useState<Record<string, () => void>>({});
  const onFailed = useCallback((id: string, retry: (() => void) | null) => {
    setFailed((f) => {
      if (retry === null) {
        if (!(id in f)) return f;
        const { [id]: _, ...rest } = f;
        return rest;
      }
      if (f[id]) return f;
      return { ...f, [id]: retry };
    });
  }, []);
  const retries = Object.values(failed);
  const tenants = me.data?.tenants ?? [];
  // the demo's projects are samples; enrolling starts from the banner
  const demo = useDemo();
  const header = (
    <PageHeader
      title="My projects"
      items={tenants.length > 0 ? [<span key="range">Last 30 days</span>] : undefined}
      primary={me.data && tenants.length > 0 && !demo ? <EnrollButton /> : null}
    />
  );

  if (me.isPending)
    return (
      <section className="flex flex-col gap-8">
        {header}
        <div role="status" aria-busy="true" className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <span className="sr-only">Loading your projects</span>
          {[0, 1].map((i) => (
            <CardSkeleton key={i} />
          ))}
        </div>
      </section>
    );
  if (me.error)
    return (
      <section className="flex flex-col gap-8">
        {header}
        <ErrorState
          title="Your projects did not load"
          error={me.error}
          onRetry={() => me.refetch()}
        />
      </section>
    );

  const shown = matchProjects(tenants, q);
  return (
    <section className="flex flex-col gap-8">
      {header}
      <RemovedNotices removed={me.data.removed_tenants} />
      {retries.length > 0 ? (
        <Notice
          tone="critical"
          role="alert"
          title="Numbers did not load"
          actions={
            <Button
              variant="outline"
              size="sm"
              className="max-sm:h-(--vs-control-touch)"
              onClick={() => {
                for (const r of retries) r();
              }}
            >
              Try again
            </Button>
          }
        />
      ) : null}

      {tenants.length === 0 ? (
        <EmptyState title="No project yet" action={demo ? null : <EnrollButton />} />
      ) : (
        <div className="flex flex-col gap-4">
          {tenants.length >= SEARCH_FROM ? (
            <FilterBar
              search={q}
              onSearch={setQ}
              searchLabel="Search projects"
              placeholder="Search by name or address"
              onClearAll={() => setQ("")}
            />
          ) : null}
          {shown.length === 0 ? (
            <EmptyState
              compact
              title="No project matches"
              action={
                <Button variant="outline" size="sm" onClick={() => setQ("")}>
                  Clear the search
                </Button>
              }
            />
          ) : (
            <Stagger as="ul" className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              {shown.map((t) => (
                <StaggerItem as="li" key={t.id} className="flex min-w-0">
                  <ProjectCard tenant={t} onFailed={onFailed} />
                </StaggerItem>
              ))}
            </Stagger>
          )}
        </div>
      )}
    </section>
  );
}

// the one thing that waits for this project, or null when nothing does.
// `act` is true when the person must do it (amber); otherwise they wait.
export function nextStep(t: MeTenant): { text: string; act: boolean } | null {
  const owner = t.role === "owner";
  if (t.status === "rejected")
    return {
      text: owner ? "Change the answers and send them again." : "An owner can send it again.",
      act: owner,
    };
  if (!t.terms_current)
    return {
      text: owner ? "Accept the new terms." : "An owner must accept the new terms.",
      act: owner,
    };
  if (t.status === "pending") return { text: "Operators review the project.", act: false };
  if (t.status === "suspended") return { text: "New events are refused.", act: false };
  return null;
}

// the last 30 days, as the scf review reads them: one call gives the
// numbers, their previous period, the setup facts, the grade and the award
const RANGE = {};

// a project's initial on a tint of its own, so cards tell apart at a glance
const TINTS = [
  "bg-series-1/20 text-series-1",
  "bg-series-3/20 text-series-3",
  "bg-series-4/20 text-series-4",
  "bg-series-2/20 text-series-2",
  "bg-series-5/20 text-series-5",
];
// fnv-1a with a final mix: ids made close together (same time prefix, one
// differing tail) still spread over every tint instead of landing on one
function tintOf(id: string): string {
  let h = 0x811c9dc5;
  for (const c of id) h = Math.imul(h ^ c.charCodeAt(0), 0x01000193) >>> 0;
  h ^= h >>> 16;
  h = Math.imul(h, 0x85ebca6b) >>> 0;
  h ^= h >>> 13;
  h = Math.imul(h, 0xc2b2ae35) >>> 0;
  h ^= h >>> 16;
  return TINTS[h % TINTS.length] as string;
}

function changeOf(kpi: Kpi | undefined): StatChange | null {
  if (!kpi || kpi.change === null) return null;
  const r = kpi.change;
  const direction = r > 0.0005 ? "up" : r < -0.0005 ? "down" : "flat";
  return {
    text: formatChange(r),
    direction,
    good: direction === "flat" ? null : direction === "up",
    period: "previous 30 days",
  };
}

// the three setup steps of a project, as the overview names them: a site,
// its tracker and domain, then the api or a proven on-chain account
function setupSteps(v: SCFView): SetupStep[] {
  const sites = v.sites?.sites ?? 0;
  const site = sites > 0;
  const tracker = site && (v.sites?.sites_verified ?? 0) > 0 && v.has_web_data;
  const data = v.has_api_data || (v.onchain?.ids_verified ?? 0) > 0;
  const state = (done: boolean, before: boolean): SetupStep["state"] =>
    done ? "done" : before ? "current" : "todo";
  return [
    { key: "site", label: "Add a site", state: state(site, true) },
    {
      key: "tracker",
      label: "Install the tracker and verify the domain",
      state: state(tracker, site),
    },
    {
      key: "data",
      label: "Connect your API or an on-chain account",
      state: state(data, tracker),
    },
  ];
}

// one project as a card: who it is, its grade, and its last 30 days, or
// what to set up next while it has no data. the whole card opens the
// project; the page links along its foot open one page each
function ProjectCard({
  tenant: t,
  onFailed,
}: {
  tenant: MeTenant;
  onFailed: (id: string, retry: (() => void) | null) => void;
}) {
  const demo = useDemo();
  // a reader has no next step to take
  const next = demo ? null : nextStep(t);
  // a project in review or rejected has no data pages yet
  const live = t.status === "active" || t.status === "suspended";
  const view = useQuery({ ...scfViewQuery(t.id, RANGE), enabled: live, retry: false });
  const { refetch } = view;
  useEffect(() => {
    onFailed(t.id, view.isError ? () => refetch() : null);
  }, [t.id, view.isError, onFailed, refetch]);

  const v = view.data;
  const kpis = v?.comparison?.kpis;
  const ops = v?.onchain && v.onchain.ids_verified > 0 ? (v.onchain.operations ?? null) : null;
  const numbers: CardNumber[] = v
    ? [
        {
          key: "visitors",
          label: "Visitors",
          series: 1,
          value: kpis?.visitors.value ?? v.web?.visitors ?? 0,
          change: changeOf(kpis?.visitors),
        },
        {
          key: "requests",
          label: "API requests",
          series: 3,
          value: kpis?.requests.value ?? v.api?.requests ?? 0,
          change: changeOf(kpis?.requests),
        },
        {
          key: "onchain",
          label: "On-chain ops",
          series: 4,
          value: kpis?.onchain_operations.value ?? ops,
          change: changeOf(kpis?.onchain_operations),
        },
      ]
    : [];
  const steps = v ? setupSteps(v) : [];
  const hasData = numbers.some((n) => (n.value ?? 0) > 0);
  const setup = v !== undefined && !hasData && steps.some((s) => s.state !== "done");
  const base = `/projects/${t.id}`;
  const links = !live
    ? [{ to: base, label: "Overview" }]
    : setup
      ? [
          { to: base, label: "Overview" },
          { to: `${base}/sites`, label: "Sites" },
          ...(demo ? [] : [{ to: `${base}/connect-api`, label: "Connect API" }]),
          { to: `${base}/onchain`, label: "On-chain" },
        ]
      : [
          { to: base, label: "Overview" },
          { to: `${base}/web`, label: "Web" },
          { to: `${base}/api`, label: "API" },
          { to: `${base}/onchain`, label: "On-chain" },
          { to: `${base}/scf`, label: "SCF review" },
        ];
  const award = v?.cohort.confirmed ?? null;
  const awardText = award ? [award.round, award.program].filter(Boolean).join(" ") : null;

  return (
    <article
      data-tenant={t.id}
      className="group relative flex min-h-70 w-full min-w-0 flex-col rounded-[18px] border border-line bg-raised transition-colors duration-(--vs-duration-press) ease-standard hover:border-line-strong has-[[data-card-link]:focus-visible]:outline-2 has-[[data-card-link]:focus-visible]:outline-offset-2 has-[[data-card-link]:focus-visible]:outline-accent-text"
    >
      <div className="flex items-center gap-4 px-6 pt-5.5 pb-4.5 max-sm:px-4">
        <span
          aria-hidden
          className={cn(
            "flex size-13 shrink-0 items-center justify-center rounded-[14px] text-[20px] font-semibold uppercase",
            tintOf(t.id),
          )}
        >
          {t.name.slice(0, 1)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-1.25">
          <Link
            to={base}
            data-card-link=""
            className="truncate text-[21px] leading-7 font-semibold tracking-[-0.02em] text-fg outline-none after:absolute after:inset-0 after:rounded-[18px] after:content-['']"
            title={t.name}
          >
            {t.name}
          </Link>
          <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[12.5px] text-fg-2">
            <StatusBadge kind="project" status={t.status} />
            {live ? (
              view.isPending ? (
                <Skeleton className="h-3 w-24" />
              ) : v ? (
                <span className="truncate">{awardText ?? "No SCF award"}</span>
              ) : null
            ) : null}
          </span>
        </span>
        {v?.integrity && (v.sites?.sites ?? 0) > 0 && v.integrity.graded_days > 0 ? (
          <GradeRing row={v.integrity} className="ml-auto" />
        ) : null}
      </div>

      {next ? (
        <p
          className={cn(
            "mx-6 mb-4 flex min-w-0 items-center gap-2 text-small max-sm:mx-4",
            next.act ? "text-warning" : "text-fg-2",
          )}
        >
          {next.act ? <CircleAlert aria-hidden className="size-4 shrink-0" /> : null}
          <span className="min-w-0">{next.text}</span>
        </p>
      ) : null}

      {live ? (
        view.isPending ? (
          <div aria-hidden className="grid grid-cols-3 gap-3 px-6 pb-5.5 max-sm:px-4">
            {[0, 1, 2].map((i) => (
              <span key={i} className="flex flex-col gap-2">
                <Skeleton className="h-3 w-3/4" />
                <Skeleton className="h-6 w-1/2" />
              </span>
            ))}
          </div>
        ) : view.isError ? (
          <p className="flex items-center gap-2 px-6 pb-5.5 text-small text-critical max-sm:px-4">
            <CircleAlert aria-hidden className="size-4 shrink-0" />
            Did not load
          </p>
        ) : setup ? (
          <div className="flex flex-col gap-2.5 px-6 pb-5.5 max-sm:px-4" data-setup="">
            <span className="flex flex-wrap items-baseline gap-x-2.5 text-[14px]">
              <span className="text-fg-2">Next</span>
              <span className="font-medium text-fg">
                {steps.find((s) => s.state !== "done")?.label}
              </span>
              <span className="ml-auto text-[12.5px] text-fg-2 tabular">
                setup {steps.filter((s) => s.state === "done").length} of {steps.length}
              </span>
            </span>
            <SetupBar steps={steps} />
          </div>
        ) : (
          <dl className="grid grid-cols-3 gap-3 px-6 pb-5.5 max-sm:px-4">
            {numbers.map((n) => (
              <div key={n.key} data-number={n.key} className="flex min-w-0 flex-col gap-1.5">
                <dt className="flex items-center gap-1.75 truncate text-caption text-fg-2">
                  <span
                    aria-hidden
                    className={cn("size-1.5 shrink-0 rounded-full", DOT[n.series])}
                  />
                  {n.label}
                </dt>
                <dd className="flex flex-wrap items-baseline gap-x-2">
                  <span className="text-[22px] leading-6.5 font-semibold tracking-[-0.02em] text-fg tabular">
                    {n.value === null ? "\u2013" : formatCompact(n.value)}
                  </span>
                  {n.change ? <ChangeLine change={n.change} className="font-medium" /> : null}
                </dd>
              </div>
            ))}
          </dl>
        )
      ) : null}

      <nav
        aria-label={`Pages of ${t.name}`}
        className="relative z-10 mt-auto flex flex-wrap gap-0.5 px-3.5 py-2 shadow-[0_-1px_0_var(--color-line-subtle)]"
      >
        {links.map((l, i) => (
          <Link
            key={l.to}
            to={l.to}
            className={cn(
              "rounded-lg px-2.5 py-1.5 text-[13px] transition-colors duration-(--vs-duration-press) hover:bg-hover hover:text-fg focus-ring max-sm:py-2.5",
              i === 0 ? "text-fg" : "text-fg-2",
            )}
          >
            {l.label}
          </Link>
        ))}
      </nav>
    </article>
  );
}

type CardNumber = {
  key: string;
  label: string;
  series: 1 | 3 | 4;
  value: number | null;
  change: StatChange | null;
};
const DOT: Record<CardNumber["series"], string> = {
  1: "bg-series-1",
  3: "bg-series-3",
  4: "bg-series-4",
};

function CardSkeleton() {
  return (
    <div aria-hidden className="flex min-h-70 flex-col rounded-[18px] border border-line bg-raised">
      <div className="flex items-center gap-4 px-6 pt-5.5 pb-4.5">
        <Skeleton className="size-13 rounded-[14px]" />
        <div className="flex flex-1 flex-col gap-2">
          <Skeleton className="h-5 w-1/2" />
          <Skeleton className="h-3 w-1/3" />
        </div>
      </div>
      <div className="grid grid-cols-3 gap-3 px-6">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex flex-col gap-2">
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-6 w-1/2" />
          </div>
        ))}
      </div>
    </div>
  );
}

// contract 5.10: each removed project is shown once with the operator's
// reason. "once" is per browser: a dismissal is remembered locally, and a
// browser that cannot store it shows the notice again, which loses nothing.
const SEEN_KEY = "sa-console.removed-seen";

function seenIds(): Set<string> {
  try {
    const raw = localStorage.getItem(SEEN_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    return new Set(Array.isArray(parsed) ? parsed.filter((x) => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

function markSeen(ids: Set<string>) {
  try {
    localStorage.setItem(SEEN_KEY, JSON.stringify([...ids]));
  } catch {
    // storage refused (private mode, blocked site data); the notice comes back next time
  }
}

export function RemovedNotices({ removed }: { removed: RemovedTenant[] }) {
  const [seen, setSeen] = useState(seenIds);
  const shown = removed.filter((r) => !seen.has(`${r.id}@${r.status_changed_at}`));
  if (shown.length === 0) return null;
  return (
    <ul className="flex flex-col gap-3" aria-label="Removed projects">
      {shown.map((r) => (
        <li key={r.id}>
          <Notice
            tone="critical"
            title={`An operator removed ${r.name}`}
            actions={
              <Button
                variant="ghost"
                size="sm"
                className="max-sm:h-(--vs-control-touch)"
                onClick={() => {
                  const next = new Set(seen);
                  next.add(`${r.id}@${r.status_changed_at}`);
                  markSeen(next);
                  setSeen(next);
                }}
              >
                Dismiss
              </Button>
            }
          >
            Its sites no longer accept traffic. Its dashboards were deleted.
            {r.status_reason ? ` Reason: ${r.status_reason}` : null}
          </Notice>
        </li>
      ))}
    </ul>
  );
}
