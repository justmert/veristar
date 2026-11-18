import { useMutation, useQueries, useQuery } from "@tanstack/react-query";
import { MonitorSmartphone, Plus } from "lucide-react";
import { type FormEvent, type ReactNode, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError } from "@/api/client";
import { api, type SiteInput, type SitePatch } from "@/api/endpoints";
import { ingestDayQuery, ingestMonthQuery, keys, sitesQuery } from "@/api/queries";
import type { Ingest, Site, TenantStatus } from "@/api/schemas";
import { webHourlyQuery } from "@/api/web";
import { type Column, DataTable, type Sort } from "@/components/data-table";
import { FormMessage } from "@/components/error-panel";
import { FilterBar } from "@/components/filter-bar";
import { TextAreaField, TextField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { EmptyState, ErrorState, Missing } from "@/components/states";
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
  nextAction,
  setupRank,
  setupStatus,
  siteSetupPath,
  webSeen,
} from "@/lib/site-setup";
import * as v from "@/lib/validate";
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

export function AddSiteForm({
  tenantId,
  onDone,
  onCancel,
  cancellable,
  submitLabel = "Add site",
}: {
  tenantId: string;
  onDone: (site?: Site) => void;
  onCancel?: () => void;
  cancellable: boolean;
  submitLabel?: string;
}) {
  const [values, setValues] = useState<SiteValues>({ name: "", domain: "", origins: "" });
  const [errors, setErrors] = useState<SiteErrors>({});
  const create = useMutation({
    mutationFn: (body: SiteInput) => api.createSite(tenantId, body),
    onSuccess: async (site) => {
      await queryClient.invalidateQueries({ queryKey: keys.sites(tenantId) });
      toast.success(`${site.name} is added. Install its tag next.`);
      onDone(site);
    },
    onError: (err) => setErrors(serverSiteErrors(err, values)),
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    const found = validateSite(values);
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    create.mutate({
      name: values.name.trim(),
      domain: values.domain.trim(),
      origins: parseOrigins(values.origins),
    });
  }

  const domain = values.domain.trim();
  const suggestion = domain && !v.domain(domain) ? `https://${domain}` : null;

  return (
    <form noValidate onSubmit={submit} aria-label="Add a site" className="flex flex-col gap-4">
      <TextField
        label="Name"
        name="name"
        placeholder="Marketing site"
        maxLength={100}
        value={values.name}
        onChange={(e) => setValues((s) => ({ ...s, name: e.target.value }))}
        error={errors.name}
      />
      <TextField
        label="Domain"
        name="domain"
        placeholder="example.org"
        hint="Cannot be changed later."
        autoCapitalize="none"
        spellCheck={false}
        className="font-mono"
        value={values.domain}
        onChange={(e) => setValues((s) => ({ ...s, domain: e.target.value.toLowerCase() }))}
        error={errors.domain}
      />
      <OriginsField
        value={values.origins}
        onChange={(origins) => setValues((s) => ({ ...s, origins }))}
        error={errors.origins}
        suggestion={values.origins.trim() === "" ? suggestion : null}
      />
      <FormMessage
        error={
          create.error && Object.keys(serverSiteErrors(create.error, values)).length === 0
            ? create.error
            : null
        }
      />
      <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line pt-4">
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
        <Button type="submit" pending={create.isPending} className="max-lg:h-(--vs-control-touch)">
          {create.isPending ? "Adding" : submitLabel}
        </Button>
      </div>
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
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title="Add a site"
      description="Add one site for each website. Its setup page comes next."
    >
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

export function OriginsField({
  value,
  onChange,
  error,
  suggestion,
}: {
  value: string;
  onChange: (v: string) => void;
  error?: string;
  suggestion: string | null;
}) {
  return (
    <div className="flex flex-col items-start gap-2">
      <div className="w-full">
        <TextAreaField
          label="Allowed origins"
          name="origins"
          rows={3}
          className="font-mono text-small"
          autoCapitalize="none"
          spellCheck={false}
          placeholder="https://example.org"
          hint="Put one origin on each line. Only pages on these origins can send events: the domain or its subdomains over https, or http://localhost for tests."
          value={value}
          onChange={(e) => onChange(e.target.value)}
          error={error}
        />
      </div>
      {suggestion ? (
        <Button
          type="button"
          variant="outline"
          size="xs"
          className="animate-fade-in font-mono max-lg:h-(--vs-control-touch)"
          onClick={() => onChange(suggestion)}
        >
          Use {suggestion}
        </Button>
      ) : null}
    </div>
  );
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

function SetupCell({ row }: { row: Row }) {
  if (row.failed) return <Missing reason="Did not load" />;
  return <StatusBadge kind="setup" status={setupStatus(row.site, row.event)} />;
}

// the project's sites with how far each is set up and its last 30 days:
// search, sort and pages for long lists, one status and one next action per
// site. rows stack on a phone.
export function SitesTable({
  sites,
  actions,
  nextStep = true,
}: {
  sites: Site[];
  actions?: ReactNode;
  // false while no setup step can finish (a suspended project)
  nextStep?: boolean;
}) {
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<Sort>({ key: "setup", order: "asc" });
  const [page, setPage] = useState(0);
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
  const shown = sortRows(
    matchSites(sites, q).flatMap((s) => byId.get(s.id) ?? []),
    sort,
  );
  const pages = Math.max(1, Math.ceil(shown.length / PAGE_SIZE));
  const current = Math.min(page, pages - 1);
  const slice = shown.slice(current * PAGE_SIZE, (current + 1) * PAGE_SIZE);

  const nextColumn: Column<Row> = {
    key: "next",
    header: "Next",
    // the next step is the card's action on a phone, never behind Details
    action: true,
    minWidth: "min-w-36",
    cell: (r) => {
      if (r.event === null) return null;
      const a = nextAction(r.site, setupStatus(r.site, r.event));
      return (
        <Link
          to={a.to}
          className="relative z-(--vs-layer-raised) rounded-sm whitespace-nowrap text-fg underline decoration-line-strong underline-offset-4 transition-colors duration-(--vs-duration-press) ease-standard hover:decoration-fg focus-ring"
        >
          {a.label}
        </Link>
      );
    },
  };
  const columns: Column<Row>[] = [
    {
      key: "site",
      header: "Site",
      sortKey: "name",
      primary: true,
      minWidth: "min-w-56",
      className: "max-w-0 w-full",
      truncate: (r) => `${r.site.name}, ${r.site.domain}`,
      cell: (r) => (
        <span className="flex min-w-0 flex-col py-1">
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate">{r.site.name}</span>
            {r.site.status !== "active" ? <StatusBadge kind="site" status={r.site.status} /> : null}
          </span>
          <span className="truncate font-mono text-caption font-normal text-fg-2">
            {r.site.domain}
          </span>
        </span>
      ),
    },
    {
      key: "setup",
      header: "Setup",
      sortKey: "setup",
      mobile: true,
      minWidth: "min-w-40",
      cell: (r) => <SetupCell row={r} />,
    },
    {
      key: "events",
      header: "Counted events, 30 days",
      sortKey: "events",
      align: "right",
      numeric: true,
      mobile: true,
      minWidth: "min-w-40",
      cell: (r) => {
        if (r.monthFailed) return <Missing reason="Did not load" />;
        const n = counted(r.month);
        return n === null ? <Bone className="ml-auto h-3 w-12" /> : formatCount(n);
      },
    },
    {
      key: "held",
      header: "Not counted",
      align: "right",
      numeric: true,
      minWidth: "min-w-28",
      cell: (r) => {
        const t = r.month?.totals;
        if (r.monthFailed) return <Missing reason="Did not load" />;
        if (!t) return <Bone className="ml-auto h-3 w-10" />;
        const all = Object.values(t).reduce((a, b) => a + b, 0);
        if (all === 0) return <Missing reason="No events in 30 days" />;
        return formatShareOf((t.quarantined ?? 0) + (t.bot ?? 0) + sumRejected(t), all);
      },
    },
  ];
  if (nextStep) columns.push(nextColumn);

  return (
    <div className="flex flex-col gap-4">
      {sites.length > 5 || actions ? (
        <div className="flex flex-wrap items-end gap-3">
          {sites.length > 5 ? (
            <FilterBar
              className="min-w-0 flex-1"
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
          {actions}
        </div>
      ) : null}
      <DataTable<Row>
        label="Sites"
        columns={columns}
        rows={slice}
        getRowKey={(r) => r.site.id}
        rowLabel={(r) => r.site.name}
        rowHref={(r) => siteSetupPath(r.site)}
        rowProps={(r) => ({ "data-site": r.site.id })}
        sort={sort}
        onSort={(key) =>
          setSort((s) =>
            s.key === key
              ? { key, order: s.order === "asc" ? "desc" : "asc" }
              : { key, order: "asc" },
          )
        }
        empty={{
          title: "No site matches",
          text: "Search by another name or domain.",
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
  const { tenant, termsCurrent } = useProject();
  // the control plane's rule: a suspended project's sites cannot be created
  // or changed (contract 5.11); a project that is not approved takes sites,
  // and they wait for review with it
  const canChange = canAddSites(tenant.status, termsCurrent);
  // the resubmit form above holds the main action of a project that is not
  // approved, and no setup step can finish while it is suspended
  const rejected = tenant.status === "rejected";
  const suspended = tenant.status === "suspended";
  const sites = useQuery(sitesQuery(tenant.id));
  const navigate = useNavigate();
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

  const add =
    canChange && live.length > 0 ? (
      <Button
        variant={rejected ? "outline" : "default"}
        onClick={() => setAdding(true)}
        className="max-lg:h-(--vs-control-touch)"
      >
        <Plus aria-hidden />
        Add site
      </Button>
    ) : null;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader {...PAGE_HEADERS.sites} freshness={freshness} primary={add} />
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
          <section
            aria-labelledby="first-site"
            className="flex max-w-dialog flex-col gap-4 rounded-lg border border-line bg-raised p-5 max-sm:p-4"
          >
            <div className="flex items-start gap-3">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-line-strong text-fg-2">
                <MonitorSmartphone aria-hidden className="size-5" strokeWidth={1.75} />
              </span>
              <div className="flex flex-col gap-1">
                <h2 id="first-site" className="text-title text-fg">
                  Add your first site
                </h2>
                <p className="text-small text-fg-2">
                  Name the website to measure. Its setup page shows the tag to install.
                  {rejected ? " The site waits until operators approve the project." : ""}
                </p>
              </div>
            </div>
            <AddSiteForm
              tenantId={tenant.id}
              cancellable={false}
              onDone={(site) => {
                if (site) navigate(siteSetupPath(site));
              }}
            />
          </section>
        ) : (
          <EmptyState
            icon={<MonitorSmartphone aria-hidden strokeWidth={1.75} />}
            title="No site yet"
          >
            {tenant.status === "suspended"
              ? "You cannot add sites while the project is suspended."
              : "An owner must accept the new terms before you can add sites."}
          </EmptyState>
        )
      ) : (
        <SitesTable sites={live} nextStep={!suspended} />
      )}
      <AddSiteDialog tenantId={tenant.id} open={adding} onOpenChange={setAdding} />
    </div>
  );
}
