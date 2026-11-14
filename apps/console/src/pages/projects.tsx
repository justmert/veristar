import { useQueries, useQuery } from "@tanstack/react-query";
import { cn } from "@veristar/brand/cn";
import {
  ChevronRight,
  CirclePause,
  CircleX,
  Clock,
  FileText,
  FolderPlus,
  Plus,
} from "lucide-react";
import { type ReactNode, useState } from "react";
import { Link } from "react-router";
import type { ApiFilters } from "@/api/endpoints";
import { apiOverviewQuery, ingestMonthQuery, meQuery, sitesQuery } from "@/api/queries";
import type { MeTenant, RemovedTenant } from "@/api/schemas";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/panel";
import { StatusPill, type Tone, toneText } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatCount } from "@/lib/format";

export function ProjectsPage() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading your projects" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  const tenants = me.data.tenants;

  return (
    <section className="flex flex-col gap-6">
      <PageHeader
        title="Your projects"
        description="A project is one team. It holds its websites and the people who manage them."
        actions={
          tenants.length > 0 ? (
            <Button asChild className="max-sm:h-(--vs-control-touch)">
              <Link to="/projects/new">
                <Plus aria-hidden />
                Enroll a project
              </Link>
            </Button>
          ) : null
        }
      />
      <RemovedNotices removed={me.data.removed_tenants} />

      {tenants.length === 0 ? (
        <StateBlock
          kind="empty"
          icon={<FolderPlus aria-hidden strokeWidth={1.75} />}
          title="Enroll your first project"
          action={
            <Button asChild className="max-sm:h-(--vs-control-touch)">
              <Link to="/projects/new">
                <Plus aria-hidden />
                Enroll a project
              </Link>
            </Button>
          }
        >
          Enroll your Stellar project to get a tracking snippet for its website and a private
          dashboard. If a teammate already enrolled it, ask them to add your GitHub username on the
          project's members page.
        </StateBlock>
      ) : (
        <Stagger as="ul" className="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
          {tenants.map((t) => (
            <StaggerItem as="li" key={t.id} className="flex">
              <ProjectCard tenant={t} />
            </StaggerItem>
          ))}
          <StaggerItem as="li" className="flex">
            <Link
              to="/projects/new"
              className="flex min-h-48 w-full flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-line-strong text-fg-2 transition-colors duration-(--vs-duration-press) hover:border-fg-3 hover:bg-raised hover:text-fg focus-ring"
            >
              <span className="flex size-10 items-center justify-center rounded-lg border border-line-strong">
                <Plus aria-hidden className="size-5" />
              </span>
              <span className="text-body font-medium">Enroll another project</span>
              <span className="max-w-64 text-center text-small text-fg-3">
                One project per team. Its sites, members and API keys live inside it.
              </span>
            </Link>
          </StaggerItem>
        </Stagger>
      )}
    </section>
  );
}

const ROLE: Record<MeTenant["role"], string> = { owner: "Owner", member: "Member" };

// the one thing that waits on the person for this project, from what
// /api/me says about it; an active project with current terms shows its
// address instead
function nextStep(t: MeTenant): { text: string; tone: Tone | null; icon: ReactNode } {
  const owner = t.role === "owner";
  if (t.status === "rejected")
    return {
      text: owner
        ? "Not approved: edit the answers and resubmit"
        : "Not approved: an owner can edit the answers and resubmit",
      tone: "critical",
      icon: <CircleX aria-hidden />,
    };
  if (!t.terms_current)
    return {
      text: owner ? "New terms to accept" : "New terms: ask an owner to accept them",
      tone: "warning",
      icon: <FileText aria-hidden />,
    };
  if (t.status === "pending")
    return { text: "Waiting for operator review", tone: "info", icon: <Clock aria-hidden /> };
  if (t.status === "suspended")
    return {
      text: "Suspended: new events are refused",
      tone: "info",
      icon: <CirclePause aria-hidden />,
    };
  return { text: t.slug, tone: null, icon: null };
}

const NO_FILTERS: ApiFilters = { service: null, route: null, method: null };

// one project as a card: who it is, what waits on the person, and the last
// 30 days in numbers, read from the same queries the overview uses (so
// opening the project shows them from the cache)
function ProjectCard({ tenant: t }: { tenant: MeTenant }) {
  const next = nextStep(t);
  const sites = useQuery(sitesQuery(t.id));
  const list = sites.data ?? [];
  const month = useQueries({ queries: list.map((s) => ingestMonthQuery(s.id)) });
  const api = useQuery(apiOverviewQuery(t.id, { range: "30d" }, NO_FILTERS));
  const verified = list.filter((s) => s.verification === "verified").length;
  const monthDone = month.every((m) => !m.isPending);
  const accepted = month.reduce((n, m) => n + (m.data?.totals.accepted ?? 0), 0);
  const requests = api.data?.current.summary.requests ?? null;

  return (
    <Link
      to={`/projects/${t.id}`}
      className="group flex w-full flex-col rounded-lg border border-line bg-raised transition duration-(--vs-duration-reveal) ease-standard hover:border-line-strong hover:bg-hover focus-ring"
    >
      <div className="flex items-start gap-3.5 p-5 pb-4">
        <span
          aria-hidden
          className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-selected text-title text-fg uppercase"
        >
          {t.name.slice(0, 1)}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="truncate text-title text-fg">{t.name}</span>
          <span className="truncate text-small text-fg-3">{t.slug}</span>
        </span>
        <span className="flex shrink-0 flex-col items-end gap-1.5">
          <StatusPill status={t.status} />
          <span className="text-caption font-normal text-fg-3">{ROLE[t.role]}</span>
        </span>
      </div>
      {next.tone ? (
        <span
          className={cn(
            "mx-5 mb-4 flex min-w-0 items-center gap-2 rounded-md border border-line bg-sunken px-3 py-2 text-small [&_svg]:size-3.5 [&_svg]:shrink-0",
            toneText(next.tone),
          )}
        >
          {next.icon}
          <span className="truncate">{next.text}</span>
        </span>
      ) : null}
      <dl className="mt-auto grid grid-cols-3 border-t border-line-subtle">
        <Metric label="Sites" pending={sites.isPending} failed={sites.isError}>
          {list.length}
          {list.length > 0 ? (
            <span className="ml-1.5 font-sans text-caption font-normal text-fg-3">
              {verified} verified
            </span>
          ) : null}
        </Metric>
        <Metric
          label="Web events, 30d"
          pending={sites.isPending || !monthDone}
          failed={sites.isError || month.some((m) => m.isError)}
        >
          {list.length === 0 ? "none" : formatCount(accepted)}
        </Metric>
        <Metric label="API requests, 30d" pending={api.isPending} failed={api.isError}>
          {requests === null || requests === 0 ? "none" : formatCount(requests)}
        </Metric>
      </dl>
      <span className="flex items-center justify-between border-t border-line-subtle px-5 py-3 text-small text-fg-2 transition-colors duration-(--vs-duration-press) group-hover:text-fg">
        Open project
        <ChevronRight
          aria-hidden
          className="size-4 transition-transform duration-(--vs-duration-press) group-hover:translate-x-0.5"
        />
      </span>
    </Link>
  );
}

function Metric({
  label,
  pending,
  failed,
  children,
}: {
  label: string;
  pending: boolean;
  failed: boolean;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 border-r border-line-subtle px-5 py-3.5 last:border-r-0">
      <dt className="truncate text-caption font-normal text-fg-3">{label}</dt>
      <dd className="flex items-baseline font-mono text-title font-medium text-fg tabular">
        {pending ? (
          <Skeleton className="h-5 w-12" />
        ) : failed ? (
          <span className="text-fg-3">n/a</span>
        ) : (
          children
        )}
      </dd>
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
            title={`${r.name} was removed by an operator`}
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
            Its sites no longer accept traffic and its dashboards were deleted.
            {r.status_reason ? ` Reason given: ${r.status_reason}` : null}
          </Notice>
        </li>
      ))}
    </ul>
  );
}
