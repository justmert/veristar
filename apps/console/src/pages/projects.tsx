import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Plus } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import { meQuery } from "@/api/queries";
import type { RemovedTenant } from "@/api/schemas";
import { ErrorPanel, Loading } from "@/components/error-panel";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";

export function ProjectsPage() {
  const me = useQuery(meQuery);
  if (me.isPending) return <Loading label="Loading your projects" />;
  if (me.error) return <ErrorPanel error={me.error} onRetry={() => me.refetch()} />;
  const tenants = me.data.tenants;

  return (
    <section>
      <RemovedNotices removed={me.data.removed_tenants} />
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight">Your projects</h1>
          <p className="mt-1 text-muted-foreground">
            A project is one team. It holds its websites and the people who manage them.
          </p>
        </div>
        <Button asChild>
          <Link to="/projects/new">
            <Plus aria-hidden />
            Enroll a project
          </Link>
        </Button>
      </div>

      {tenants.length === 0 ? (
        <div className="mt-10 rounded-xl border border-dashed bg-card p-8">
          <h2 className="text-lg font-semibold">No projects yet</h2>
          <p className="mt-1 max-w-prose text-muted-foreground">
            Enroll your Stellar project to get a tracking snippet for its website and a private
            dashboard. If a teammate already enrolled it, ask them to add your GitHub username on
            the project's members page.
          </p>
          <Button asChild className="mt-5">
            <Link to="/projects/new">Enroll a project</Link>
          </Button>
        </div>
      ) : (
        <ul className="mt-8 divide-y overflow-hidden rounded-xl border bg-card">
          {tenants.map((t) => (
            <li key={t.id}>
              <Link
                to={`/projects/${t.id}`}
                className="group flex items-center gap-4 px-4 py-4 hover:bg-muted/60 focus-visible:bg-muted/60 sm:px-5"
              >
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">{t.name}</p>
                  <p className="truncate text-sm text-muted-foreground">
                    {t.slug}, you are {t.role === "owner" ? "an owner" : "a member"}
                    {t.terms_current ? "" : ", new terms to accept"}
                  </p>
                </div>
                <StatusPill status={t.status} />
                <ChevronRight
                  aria-hidden
                  className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5"
                />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
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
    <ul className="mb-8 space-y-3" aria-label="Removed projects">
      {shown.map((r) => (
        <li
          key={r.id}
          role="status"
          className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm"
        >
          <p className="font-medium">{r.name} was removed by an operator</p>
          <p className="mt-1 text-muted-foreground">
            Its sites no longer accept traffic and its dashboards were deleted.
          </p>
          {r.status_reason ? (
            <p className="mt-1 text-muted-foreground">Reason given: {r.status_reason}</p>
          ) : null}
          <Button
            variant="outline"
            size="sm"
            className="mt-3"
            onClick={() => {
              const next = new Set(seen);
              next.add(`${r.id}@${r.status_changed_at}`);
              markSeen(next);
              setSeen(next);
            }}
          >
            Dismiss
          </Button>
        </li>
      ))}
    </ul>
  );
}
