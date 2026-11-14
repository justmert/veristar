import { useQuery } from "@tanstack/react-query";
import { cn } from "cn";
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
import { meQuery } from "@/api/queries";
import type { MeTenant, RemovedTenant } from "@/api/schemas";
import { ErrorPanel, Loading, StateBlock } from "@/components/error-panel";
import { Stagger, StaggerItem } from "@/components/motion";
import { Notice } from "@/components/notice";
import { PageHeader } from "@/components/panel";
import { StatusPill, type Tone, toneText } from "@/components/status";
import { Button } from "@/components/ui/button";

export function ProjectsPage() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading your projects" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  const tenants = me.data.tenants;

  return (
    <section className="flex max-w-4xl flex-col gap-5">
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
        <Stagger as="ul" className="overflow-hidden rounded-lg border border-line bg-raised">
          {tenants.map((t) => (
            <StaggerItem as="li" key={t.id} className="border-b border-line-subtle last:border-b-0">
              <ProjectRow tenant={t} />
            </StaggerItem>
          ))}
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

function ProjectRow({ tenant: t }: { tenant: MeTenant }) {
  const next = nextStep(t);
  return (
    <Link
      to={`/projects/${t.id}`}
      className="group grid grid-cols-[2.25rem_minmax(0,1fr)_1rem] items-center gap-x-3.5 px-4 py-3.5 transition-colors duration-(--vs-duration-press) hover:bg-hover focus-ring focus-visible:-outline-offset-2 sm:grid-cols-[2.25rem_minmax(0,1fr)_auto_1rem]"
    >
      <span
        aria-hidden
        className="flex size-9 items-center justify-center rounded-md bg-selected text-small font-semibold text-fg uppercase"
      >
        {t.name.slice(0, 1)}
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="truncate text-title font-medium text-fg">{t.name}</span>
        <span
          className={cn(
            "flex min-w-0 items-center gap-1.5 text-small [&_svg]:size-3.5 [&_svg]:shrink-0",
            next.tone ? toneText(next.tone) : "text-fg-3",
          )}
        >
          {next.icon}
          <span className="truncate">{next.text}</span>
        </span>
        <span className="mt-1 flex items-center gap-2 text-caption font-normal text-fg-2 sm:hidden">
          <StatusPill status={t.status} />
          {ROLE[t.role]}
        </span>
      </span>
      <span className="hidden flex-col items-end gap-1 text-caption font-normal text-fg-2 sm:flex">
        <StatusPill status={t.status} />
        {ROLE[t.role]}
      </span>
      <ChevronRight
        aria-hidden
        className="size-4 text-fg-3 transition-transform duration-(--vs-duration-press) group-hover:translate-x-0.5 group-hover:text-fg-2"
      />
    </Link>
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
