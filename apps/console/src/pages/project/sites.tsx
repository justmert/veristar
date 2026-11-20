import { useMutation, useQueries, useQuery } from "@tanstack/react-query";
import { BarChart3, Plus, Settings, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useId, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api, type SiteInput, type SitePatch } from "@/api/endpoints";
import { ingestDayQuery, ingestMonthQuery, keys, sitesQuery } from "@/api/queries";
import type { Ingest, Site, TenantStatus } from "@/api/schemas";
import { webHourlyQuery } from "@/api/web";
import { type Column, DataTable, type Sort } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { FilterBar } from "@/components/filter-bar";
import { TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { type SetupStep as SetupBarStep, SetupNext } from "@/components/setup-progress";
import { EmptyState, ErrorState, Missing } from "@/components/states";
import { Pill } from "@/components/status";
import { StatusBadge } from "@/components/status-badge";
import { toast } from "@/components/toast";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Skeleton as Bone } from "@/components/ui/skeleton";
import { formatCount, formatShareOf, utcDay } from "@/lib/format";
import { sumRejected } from "@/lib/outcomes";
import {
  combineFirstEvent,
  type FirstEvent,
  firstEvent,
  setupRank,
  setupStatus,
  siteSetupPath,
  webSeen,
} from "@/lib/site-setup";
import * as v from "@/lib/validate";
import { webPath } from "@/lib/web-dashboard";
import { useProject } from "@/pages/project-layout";
import { queryClient } from "@/query-client";
import { PAGE_HEADERS, SitesSkeleton } from "./page-frame";

export type SiteValues = { name: string; domain: string; origins: string };
export type SiteErrors = Partial<Record<keyof SiteValues, string>>;

export function parseOrigins(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

export function validateSite(values: SiteValues): SiteErrors {
  const errors: SiteErrors = {};
  const name = v.siteName(values.name);
  if (name) errors.name = v.fieldMessage(name);
  const domain = v.domain(values.domain.trim());
  if (domain) errors.domain = v.fieldMessage(domain);
  const list = parseOrigins(values.origins);
  const found = v.originList(list, domain ? "" : values.domain.trim());
  errors.origins = originMessage(found, list) ?? undefined;
  if (!errors.origins) delete errors.origins;
  return errors;
}

// one message for the origins box, naming the first origin that failed
function originMessage(found: Record<string, string>, list: string[]): string | null {
  if (found.origins) return v.fieldMessage(found.origins);
  for (const [path, code] of Object.entries(found)) {
    const m = /^origins\[(\d+)\]$/.exec(path);
    if (m?.[1] !== undefined) return `${list[Number(m[1])] ?? path}: ${v.fieldMessage(code)}`;
  }
  return null;
}

export function serverSiteErrors(err: unknown, values: SiteValues): SiteErrors {
  if (!(err instanceof ApiError) || err.code !== "validation_failed") return {};
  const out: SiteErrors = {};
  const originsFound: Record<string, string> = {};
  for (const [path, code] of Object.entries(err.fields)) {
    if (path === "name" || path === "domain") out[path] ??= v.fieldMessage(code);
    else if (path === "origins" || path.startsWith("origins[")) originsFound[path] = code;
  }
  const o = originMessage(originsFound, parseOrigins(values.origins));
  if (o) out.origins = o;
  return out;
}

// only the fields that changed: a patch that resends unchanged origins would
// still republish the registry entry for nothing
export function sitePatch(site: Site, values: SiteValues): SitePatch {
  const patch: SitePatch = {};
  const name = values.name.trim();
  if (name !== site.name) patch.name = name;
  const origins = parseOrigins(values.origins);
  if (origins.join("\n") !== site.origins.join("\n")) patch.origins = origins;
  return patch;
}

// ------------------------------------------------------------ add a site

// a new site starts with its own domain as the one allowed origin; more
// origins are added on the site's page
export function AddSiteForm({
  tenantId,
  onDone,
  onCancel,
  cancellable,
  submitLabel = "Add site",
  wide = false,
}: {
  tenantId: string;
  onDone: (site?: Site) => void;
  onCancel?: () => void;
  cancellable: boolean;
  submitLabel?: string;
  // the full-width submit of the first-site card
  wide?: boolean;
}) {
  const [values, setValues] = useState<SiteValues>({ name: "", domain: "", origins: "" });
  const [errors, setErrors] = useState<SiteErrors>({});
  const withOrigin = (s: SiteValues): SiteValues => ({
    ...s,
    origins: s.domain.trim() ? `https://${s.domain.trim()}` : "",
  });
  const create = useMutation({
    mutationFn: (body: SiteInput) => api.createSite(tenantId, body),
    onSuccess: async (site) => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenantId) });
      toast.success(`${site.name} is added. Install its tag next.`);
      onDone(site);
    },
    onError: (err) => setErrors(serverSiteErrors(err, withOrigin(values))),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const full = withOrigin(values);
    const found = validateSite(full);
    // the origin follows the domain, so its error is the domain's
    if (found.origins && !found.domain) found.domain = found.origins;
    delete found.origins;
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    create.mutate({
      name: full.name.trim(),
      domain: full.domain.trim(),
      origins: parseOrigins(full.origins),
    });
  }

  return (
    <form noValidate onSubmit={submit} aria-label="Add a site" className="flex flex-col gap-4">
      <TextField
        label="Name"
        name="name"
        placeholder="Lumen Pay app"
        maxLength={100}
        value={values.name}
        onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
        error={errors.name}
      />
      <TextField
        label="Domain"
        name="domain"
        placeholder="lumenpay.example"
        autoCapitalize="none"
        spellCheck={false}
        className="font-mono"
        value={values.domain}
        onChange={(e) => setValues((s) => ({ ...s, domain: e.target.value.toLowerCase() }))}
        error={errors.domain ?? errors.origins}
      />
      <FormMessage
        error={
          create.error &&
          Object.keys(serverSiteErrors(create.error, withOrigin(values))).length === 0
            ? create.error
            : null
        }
      />
      {wide ? (
        <Button type="submit" pending={create.isPending} className="mt-1 h-[42px] w-full">
          {create.isPending ? "Adding" : submitLabel}
        </Button>
      ) : (
        <div className="flex flex-wrap items-center justify-end gap-2 pt-2">
          {cancellable ? (
            <Button
              type="button"
              variant="outline"
              className="max-lg:h-(--vs-control-touch)"
              onClick={onCancel ?? (() => onDone())}
            >
              Cancel
            </Button>
          ) : null}
          <Button
            type="submit"
            pending={create.isPending}
            className="max-lg:h-(--vs-control-touch)"
          >
            {create.isPending ? "Adding" : submitLabel}
          </Button>
        </div>
      )}
    </form>
  );
}

// add a site from anywhere in the project; the next screen is its setup
export function AddSiteDialog({
  tenantId,
  open,
  onOpenChange,
}: {
  tenantId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const navigate = useNavigate();
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Add a site">
      {open ? (
        <div className="pb-4">
          <AddSiteForm
            tenantId={tenantId}
            cancellable
            onCancel={() => onOpenChange(false)}
            onDone={(site) => {
              onOpenChange(false);
              if (site) navigate(siteSetupPath(site));
            }}
          />
        </div>
      ) : null}
    </Dialog>
  );
}

// the site rule of the control plane: a suspended project refuses new sites
// (409 tenant_suspended); a project that is not approved takes them, and
// they wait for review with the project
export function canAddSites(status: TenantStatus, termsCurrent: boolean): boolean {
  return termsCurrent && status !== "suspended" && status !== "removed";
}

export function AddSiteButton({
  tenantId,
  variant = "outline",
}: {
  tenantId: string;
  variant?: "outline" | "default";
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant={variant}
        size={variant === "default" ? "default" : "sm"}
        onClick={() => setOpen(true)}
        className="max-lg:h-(--vs-control-touch)"
      >
        <Plus aria-hidden />
        Add site
      </Button>
      <AddSiteDialog tenantId={tenantId} open={open} onOpenChange={setOpen} />
    </>
  );
}

// ------------------------------------------------------------ setup steps

// the three required steps of a site in the shared setup bar: the tracker
// runs once any event arrives (counted or refused), events arrive once one
// is counted, the domain once a check passed. a failed domain check or
// refused events are the problem of their step.
export function siteSteps(site: Site, event: FirstEvent | null): SetupBarStep[] {
  const received = event === "received";
  const installed = received || event === "refused";
  return [
    { key: "install", label: "Install the tracker", state: installed ? "done" : "current" },
    {
      key: "event",
      label: "Events arrive",
      state: received ? "done" : event === "refused" ? "problem" : installed ? "current" : "todo",
    },
    {
      key: "domain",
      label: "Verify the domain",
      state:
        site.verification === "verified"
          ? "done"
          : !received
            ? "todo"
            : site.last_verify_error
              ? "problem"
              : "current",
    },
  ];
}

export function setupComplete(steps: SetupBarStep[]): boolean {
  return steps.every((s) => s.state === "done");
}

// ------------------------------------------------------------ first event

// rows are folded every few minutes, so a 30 s poll finds a first event soon
// after it is countable without asking more often than it can change
export const FIRST_EVENT_POLL = 30_000;

// today's page views of a site from the web dashboard's engine: the second
// source of the first event. a card or row shows a gap at once instead of
// waiting for the dashboards to be set up.
function webTodayQuery(site: Site) {
  return { ...webHourlyQuery(site.tenant_id, utcDay(), site.id, "primary"), retry: false };
}

// the first event of one site, from both sources, polled while nothing has
// arrived. `month` and `day` keep the gateway's refusal reasons.
export function useFirstEvent(site: Site): {
  event: FirstEvent | null;
  day: Ingest | undefined;
  month: Ingest | undefined;
} {
  const month = useQuery(ingestMonthQuery(site.id));
  const monthReceived = month.data !== undefined && firstEvent(month.data) === "received";
  const day = useQuery({
    ...ingestDayQuery(site.id),
    refetchInterval: (q) =>
      monthReceived || (q.state.data && firstEvent(q.state.data) === "received")
        ? false
        : FIRST_EVENT_POLL,
  });
  const gatewayKnown = month.data !== undefined || day.data !== undefined;
  const gateway = gatewayKnown ? firstEvent(month.data, day.data) : null;
  const web = useQuery({
    ...webTodayQuery(site),
    enabled: !monthReceived,
    refetchInterval: (q) =>
      gateway === "received" || webSeen(q.state.data) ? false : FIRST_EVENT_POLL,
  });
  return {
    event: combineFirstEvent(gateway, web.data ? webSeen(web.data) : null),
    day: day.data,
    month: month.data,
  };
}

// ------------------------------------------------------------ sites table

type Row = {
  site: Site;
  event: FirstEvent | null;
  // neither source answered
  failed: boolean;
  month: Ingest | undefined;
  monthFailed: boolean;
};

const PAGE_SIZE = 25;
// from this many sites the list gets search, sort and pages
export const SITE_TOOLS_FROM = 11;

// the sites whose name or domain holds the search text
export function matchSites(list: Site[], q: string): Site[] {
  const t = q.trim().toLowerCase();
  if (!t) return list;
  return list.filter((s) => s.name.toLowerCase().includes(t) || s.domain.includes(t));
}

function counted(m: Ingest | undefined): number | null {
  return m ? (m.totals.accepted ?? 0) : null;
}

export function sortRows(rows: Row[], sort: Sort): Row[] {
  const dir = sort.order === "asc" ? 1 : -1;
  const by = (r: Row): string | number =>
    sort.key === "name"
      ? r.site.name.toLowerCase()
      : sort.key === "events"
        ? (counted(r.month) ?? -1)
        : r.event === null
          ? 99
          : setupRank(setupStatus(r.site, r.event));
  return [...rows].sort((a, b) => {
    const x = by(a);
    const y = by(b);
    return (
      (x < y ? -1 : x > y ? 1 : 0) * dir ||
      a.site.name.localeCompare(b.site.name, "en", { numeric: true })
    );
  });
}

// the state pill of a site that is not simply live: paused, removed by an
// operator, or a domain an operator checks first
function siteStatePill(site: Site): ReactNode {
  if (site.status !== "active") return <StatusBadge kind="site" status={site.status} />;
  if (site.in_review) return <Pill tone="info">In review</Pill>;
  return null;
}

function SetupCell({ row }: { row: Row }) {
  if (row.failed) return <Missing reason="Did not load" />;
  if (row.event === null) return <Bone className="h-8 w-44" />;
  return <SetupNext steps={siteSteps(row.site, row.event)} status={siteStatePill(row.site)} />;
}

// the project's sites with how far each is set up and its last 30 days:
// one row per site that opens the site, the next step only where one is
// open, the rest in the row menu. search, sort and pages from 11 sites.
export function SitesTable({
  sites,
  nextStep = true,
  canRemove = false,
}: {
  sites: Site[];
  // false while no setup step can finish (a suspended project)
  nextStep?: boolean;
  // owners who accepted the current terms delete sites
  canRemove?: boolean;
}) {
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<Sort>({ key: "setup", order: "asc" });
  const [page, setPage] = useState(0);
  const navigate = useNavigate();
  const tools = sites.length >= SITE_TOOLS_FROM;
  const months = useQueries({ queries: sites.map((s) => ingestMonthQuery(s.id)) });
  const gateway = months.map((m) => (m.data ? firstEvent(m.data) : null));
  const webs = useQueries({
    queries: sites.map((s, i) => ({
      ...webTodayQuery(s),
      enabled: months[i]?.isFetched === true && gateway[i] !== "received",
    })),
  });
  const rows: Row[] = sites.map((site, i) => {
    const w = webs[i];
    const web = w?.data ? webSeen(w.data) : null;
    const event = combineFirstEvent(gateway[i] ?? null, web);
    return {
      site,
      event,
      failed: event === null && months[i]?.isError === true && (w?.isError ?? true),
      month: months[i]?.data,
      monthFailed: months[i]?.isError === true,
    };
  });
  const byId = new Map(rows.map((r) => [r.site.id, r]));
  const matched = matchSites(sites, q).flatMap((s) => byId.get(s.id) ?? []);
  // a short list keeps the project's order of sites: the ones that need a
  // step first, by name
  const shown = sortRows(matched, tools ? sort : { key: "setup", order: "asc" });
  const pages = Math.max(1, Math.ceil(shown.length / PAGE_SIZE));
  const current = Math.min(page, pages - 1);
  const slice = shown.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE);

  const remove = async (site: Site) => {
    await api.deleteSite(site.tenant_id, site.id);
    toast.success(`${site.name} was deleted.`);
    await queryClient.invalidateQueries({ queryKey: keys.sites(site.tenant_id) });
  };

  const columns: Column<Row>[] = [
    {
      key: "site",
      header: "Site",
      sortKey: tools ? "name" : undefined,
      primary: true,
      minWidth: "min-w-56",
      className: "max-w-0 w-[34%]",
      truncate: (r) => `${r.site.name}, ${r.site.domain}`,
      cell: (r) => (
        <span className="flex min-w-0 flex-col gap-1 py-1">
          <span className="truncate font-medium text-fg">{r.site.name}</span>
          <span className="truncate font-mono text-caption font-normal text-fg-2">
            {r.site.domain}
          </span>
        </span>
      ),
    },
    {
      key: "setup",
      header: "Setup",
      sortKey: tools ? "setup" : undefined,
      mobile: true,
      minWidth: "min-w-56",
      cell: (r) => <SetupCell row={r} />,
    },
    {
      key: "events",
      header: "Events, 30 days",
      sortKey: tools ? "events" : undefined,
      align: "right",
      numeric: true,
      mobile: true,
      minWidth: "min-w-32",
      cell: (r) => {
        if (r.monthFailed) return <Missing reason="Did not load" />;
        const n = counted(r.month);
        return n === null ? (
          <Bone className="ml-auto h-3 w-12" />
        ) : (
          <span className="font-semibold text-fg">{formatCount(n)}</span>
        );
      },
    },
    {
      key: "held",
      header: "Held back",
      definition:
        "The share of this site's events in 30 days that were not counted: quarantined, from bots or refused.",
      align: "right",
      numeric: true,
      minWidth: "min-w-28",
      cell: (r) => {
        const t = r.month?.totals;
        if (r.monthFailed) return <Missing reason="Did not load" />;
        if (!t) return <Bone className="ml-auto h-3 w-10" />;
        const all = Object.values(t).reduce((a, b) => a + b, 0);
        // a site with no event has no share: the cell stays empty
        if (all === 0) return null;
        return formatShareOf((t.quarantined ?? 0) + (t.bot ?? 0) + sumRejected(t), all);
      },
    },
  ];

  return (
    <div className="flex flex-col gap-4">
      {tools ? (
        <FilterBar
          className="min-w-0"
          search={q}
          onSearch={(t) => {
            setQ(t);
            setPage(0);
          }}
          searchLabel="Search sites"
          placeholder="Search by name or domain"
          onClearAll={() => setQ("")}
        />
      ) : null}
      <DataTable<Row>
        label="Sites"
        columns={columns}
        rows={slice}
        getRowKey={(r) => r.site.id}
        rowLabel={(r) => r.site.name}
        rowHref={(r) => siteSetupPath(r.site)}
        rowProps={(r) => ({ "data-site": r.site.id })}
        // the one inline action: continue the setup of a site that is not
        // ready
        rowNext={(r) =>
          nextStep && r.event !== null && !setupComplete(siteSteps(r.site, r.event))
            ? { label: "Continue setup", to: siteSetupPath(r.site) }
            : null
        }
        rowActions={(r) => [
          {
            label: "Web data",
            icon: <BarChart3 aria-hidden />,
            onSelect: () => navigate(webPath(r.site.tenant_id, r.site.id)),
          },
          {
            label: "Settings",
            icon: <Settings aria-hidden />,
            onSelect: () => navigate(`${siteSetupPath(r.site)}#settings`),
          },
          canRemove && {
            label: "Remove",
            icon: <Trash2 aria-hidden />,
            destructive: true,
            confirm: {
              title: `Delete the site “${r.site.name}”?`,
              description: `Events from ${r.site.domain} are refused, and all web data of this site is deleted. You cannot undo this.`,
              confirmLabel: "Delete site",
              pendingLabel: "Deleting",
              typed: { value: r.site.domain, label: `Type ${r.site.domain} to confirm` },
              run: () => remove(r.site),
            },
          },
        ]}
        sort={tools ? sort : undefined}
        onSort={
          tools
            ? (key) =>
                setSort((s) =>
                  s.key === key
                    ? { key, order: s.order === "asc" ? "desc" : "asc" }
                    : { key, order: "asc" },
                )
            : undefined
        }
        empty={{
          title: "No site matches",
          action: (
            <Button variant="outline" size="sm" onClick={() => setQ("")}>
              Clear the search
            </Button>
          ),
        }}
        footer={
          shown.length > PAGE_SIZE
            ? {
                kind: "pager",
                page: current,
                pageSize: PAGE_SIZE,
                total: shown.length,
                onPage: setPage,
              }
            : undefined
        }
      />
    </div>
  );
}

// ------------------------------------------------------------ the page

export function SitesPage() {
  const { tenant, isOwner, termsCurrent } = useProject();
  // the control plane's rule: a suspended project's sites cannot be created
  // or changed (contract 5.11); a project that is not approved takes sites,
  // and they wait for review with it
  const canChange = canAddSites(tenant.status, termsCurrent);
  const suspended = tenant.status === "suspended";
  const sites = useQuery(sitesQuery(tenant.id));
  const [adding, setAdding] = useState(false);
  const live = useMemo(
    () => (sites.data ?? []).filter((s) => s.status !== "removed"),
    [sites.data],
  );
  const months = useQueries({ queries: live.map((s) => ingestMonthQuery(s.id)) });
  const read = months.map((m) => m.dataUpdatedAt).filter((t) => t > 0);
  const freshness =
    read.length > 0
      ? { at: new Date(Math.min(...read)), cadence: "Event counts refresh every few minutes." }
      : null;
  const empty = sites.data !== undefined && live.length === 0;

  const add =
    canChange && !empty ? (
      <Button onClick={() => setAdding(true)} className="max-lg:h-(--vs-control-touch)">
        <Plus aria-hidden />
        Add site
      </Button>
    ) : null;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader {...PAGE_HEADERS.sites} freshness={empty ? null : freshness} primary={add} />
      {sites.isPending ? (
        <SitesSkeleton />
      ) : sites.error ? (
        <ErrorState
          title="The sites did not load"
          error={sites.error}
          onRetry={() => sites.refetch()}
        />
      ) : live.length === 0 ? (
        canChange ? (
          <FirstSiteCard tenantId={tenant.id} />
        ) : (
          <EmptyState title="No site yet">
            {suspended
              ? "You cannot add sites while the project is suspended."
              : "An owner must accept the new terms before you can add sites."}
          </EmptyState>
        )
      ) : (
        <SitesTable sites={live} nextStep={!suspended} canRemove={isOwner && termsCurrent} />
      )}
      <AddSiteDialog tenantId={tenant.id} open={adding} onOpenChange={setAdding} />
    </div>
  );
}

// a project without a site: the add form is the page, centred in a card
function FirstSiteCard({ tenantId }: { tenantId: string }) {
  const navigate = useNavigate();
  const id = useId();
  return (
    <div className="flex min-h-[60vh] items-center justify-center">
      <section
        aria-labelledby={id}
        className="flex w-full max-w-[600px] flex-col gap-5 rounded-2xl border border-line bg-raised px-[30px] py-7 max-sm:px-5"
      >
        <h2 id={id} className="text-title text-fg">
          Add your first site
        </h2>
        <AddSiteForm
          tenantId={tenantId}
          cancellable={false}
          wide
          onDone={(site) => {
            if (site) navigate(siteSetupPath(site));
          }}
        />
      </section>
    </div>
  );
}
