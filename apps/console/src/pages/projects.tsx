import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Plus } from "lucide-react";
import { Link } from "react-router";
import { meQuery } from "@/api/queries";
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
