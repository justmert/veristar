import { useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import { ChevronRight, CircleAlert, FolderPlus, Plus } from "lucide-react";
import { type ReactNode, useState } from "react";
import { Link, redirect } from "react-router";
import type { ApiFilters } from "@/api/endpoints";
import { apiOverviewQuery, meQuery, sitesQuery } from "@/api/queries";
import type { Me, MeTenant, RemovedTenant } from "@/api/schemas";
import { webSummaryQuery } from "@/api/web";
import { FilterBar } from "@/components/filter-bar";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/page-header";
import { EmptyState, ErrorState, Missing } from "@/components/states";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatCount } from "@/lib/format";
import { readWebState, webQuery } from "@/lib/web-dashboard";
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
  const tenants = me.data?.tenants ?? [];
  const header = (
    <PageHeader
      title="Projects"
      description="A project is one team, with its sites, keys and members."
      primary={me.data && tenants.length > 0 ? <EnrollButton /> : null}
    />
  );

  if (me.isPending)
    return (
      <section className="flex flex-col gap-8">
        {header}
        <div role="status" aria-busy="true" className="grid grid-cols-1 gap-4 md:grid-cols-2">
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
        <ErrorState error={me.error} onRetry={() => me.refetch()} />
      </section>
    );

  const shown = matchProjects(tenants, q);
  return (
    <section className="flex flex-col gap-8">
      {header}
      <RemovedNotices removed={me.data.removed_tenants} />

      {tenants.length === 0 ? (
        <EmptyState
          icon={<FolderPlus aria-hidden strokeWidth={1.75} />}
          title="No project yet"
          action={<EnrollButton />}
        >
          Enroll your project to get a tracking tag and private dashboards.
        </EmptyState>
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
            >
              Search by another name or address.
            </EmptyState>
          ) : (
            <Stagger as="ul" className="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
              {shown.map((t) => (
                <StaggerItem as="li" key={t.id} className="flex min-w-0">
                  <ProjectCard tenant={t} />
                </StaggerItem>
              ))}
            </Stagger>
          )}
        </div>
      )}
    </section>
  );
}

const ROLE: Record<MeTenant["role"], string> = { owner: "Owner", member: "Member" };

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

const NO_FILTERS: ApiFilters = { service: null, route: null, method: null };

// one project as a card: who it is, what waits for it, and its last 30
// days, read from the sources its own pages read (page views from the web
// dashboard, requests from the api metrics), so the numbers agree
function ProjectCard({ tenant: t }: { tenant: MeTenant }) {
  const next = nextStep(t);
  const sites = useQuery(sitesQuery(t.id));
  const live = (sites.data ?? []).filter((s) => s.status !== "removed");
  const hasSites = live.length > 0;
  const web = useQuery({
    ...webSummaryQuery(t.id, webQuery(readWebState(new URLSearchParams()), new Date())),
    enabled: hasSites,
    // a card shows the gap at once; the web page waits for the setup
    retry: false,
  });
  const api = useQuery({ ...apiOverviewQuery(t.id, { range: "30d" }, NO_FILTERS), retry: false });
  const verified = live.filter((s) => s.verification === "verified").length;
  const views = web.data?.current.totals.pageviews ?? null;
  const requests = api.data?.current.summary.requests ?? null;

  return (
    <Link
      to={`/projects/${t.id}`}
      data-tenant={t.id}
      className="group flex w-full min-w-0 flex-col rounded-lg border border-line bg-raised transition-colors duration-(--vs-duration-press) ease-standard hover:border-line-strong hover:bg-hover focus-ring"
    >
      <div className="flex items-start gap-3 p-5 max-sm:p-4">
        <span
          aria-hidden
          className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-selected text-title text-fg uppercase"
        >
          {t.name.slice(0, 1)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="truncate text-title text-fg" title={t.name}>
            {t.name}
          </span>
          <span className="flex min-w-0 items-center gap-2 text-small text-fg-2">
            <span className="truncate font-mono" title={t.slug}>
              {t.slug}
            </span>
            <span aria-hidden>·</span>
            <span className="shrink-0">{ROLE[t.role]}</span>
          </span>
        </span>
        <span className="flex shrink-0 items-center gap-1">
          <StatusBadge kind="project" status={t.status} />
          <ChevronRight
            aria-hidden
            className="size-4 text-fg-3 transition-transform duration-(--vs-duration-press) ease-standard group-hover:translate-x-0.5 group-hover:text-fg"
          />
        </span>
      </div>
      {next ? (
        <span
          className={cn(
            "mx-5 mb-4 flex min-w-0 items-center gap-2 text-small max-sm:mx-4",
            next.act ? "text-warning" : "text-fg-2",
          )}
        >
          {next.act ? <CircleAlert aria-hidden className="size-4 shrink-0" /> : null}
          <span className="min-w-0">{next.text}</span>
        </span>
      ) : null}
      <dl className="mt-auto grid grid-cols-3 border-t border-line-subtle">
        <Metric label="Sites" pending={sites.isPending}>
          {sites.isError ? (
            <Missing reason="Did not load" />
          ) : (
            <span className="flex flex-col">
              {formatCount(live.length)}
              {hasSites ? (
                <span className="font-sans text-caption font-normal text-fg-2">
                  {formatCount(verified)} verified
                </span>
              ) : null}
            </span>
          )}
        </Metric>
        <Metric
          label="Page views, 30 days"
          pending={sites.isPending || (hasSites && web.isPending)}
        >
          {sites.isError || web.isError ? (
            <Missing reason="Did not load" />
          ) : !hasSites ? (
            <Missing reason="No site yet" />
          ) : views === null ? (
            <Missing reason="No data in this range" />
          ) : (
            formatCount(views)
          )}
        </Metric>
        <Metric label="API requests, 30 days" pending={api.isPending}>
          {api.isError || requests === null ? (
            <Missing reason="Did not load" />
          ) : (
            formatCount(requests)
          )}
        </Metric>
      </dl>
    </Link>
  );
}

function Metric({
  label,
  pending,
  children,
}: {
  label: string;
  pending: boolean;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 border-r border-line-subtle px-5 py-3 last:border-r-0 max-sm:px-3">
      <dt className="truncate text-caption text-fg-2">{label}</dt>
      <dd className="min-h-6 font-mono text-body font-medium text-fg tabular">
        {pending ? <Skeleton className="h-5 w-12" /> : children}
      </dd>
    </div>
  );
}

function CardSkeleton() {
  return (
    <div aria-hidden className="flex flex-col rounded-lg border border-line bg-raised">
      <div className="flex items-start gap-3 p-5">
        <Skeleton className="size-10 rounded-lg" />
        <div className="flex flex-1 flex-col gap-2">
          <Skeleton className="h-4 w-1/2" />
          <Skeleton className="h-3 w-1/3" />
        </div>
      </div>
      <div className="grid grid-cols-3 border-t border-line-subtle">
        {[0, 1, 2].map((i) => (
          <div key={i} className="flex flex-col gap-2 px-5 py-3">
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-5 w-1/2" />
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
