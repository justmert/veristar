import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Columns3, Download, Info } from "lucide-react";
import { type ReactNode, useEffect, useId, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { cohortListQuery } from "@/api/cohorts";
import type { TenantListQuery } from "@/api/endpoints";
import { exportPaths } from "@/api/onchain";
import { apiProjectsQuery, operatorTenantsQuery, webProjectsQuery } from "@/api/queries";
import { type ApiSort, apiSort, type TenantStatus, tenantStatus, webSort } from "@/api/schemas";
import { DataTable, PAGE_SIZE } from "@/components/data-table";
import { type Filter, FilterBar } from "@/components/filter-bar";
import { SelectField } from "@/components/form-field";
import { PageHeader } from "@/components/page-header";
import { Kbd } from "@/components/panel";
import { DateField, isIsoDay } from "@/components/range-control";
import { Segmented } from "@/components/segmented";
import { statusText } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog } from "@/components/ui/dialog";
import {
  Menu,
  MenuCheckboxItem,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuTrigger,
} from "@/components/ui/menu";
import { formatDate, formatDateRange } from "@/lib/format";
import {
  COHORT_KEYS,
  type CohortKey,
  dayPairError,
  EMPTY_FILTERS,
  type IntegrityFilter,
  type ListState,
  MAX_COMPARE,
  type ProjectFilters,
  readListState,
  type Tri,
  type TriKey,
  writeListState,
} from "@/lib/global";
import {
  columnsOf,
  DEFAULT_API_SORT,
  DEFAULT_WEB_SORT,
  listExportHref,
  loadColumns,
  nextSort,
  type ProjectsView,
  phoneColumns,
  readView,
  saveColumns,
  shownColumns,
  statusParams,
  statusQuery,
  switchView,
  VIEWS,
} from "@/lib/operator-projects";
import { PortfolioBar, type Segment, usePortfolio } from "./portfolio";
import { apiColumns, blankColumns, pick, statusColumns, webColumns } from "./project-columns";
import { handedOver, ListRange, type Selection } from "./shared";

const SEARCH_DELAY_MS = 250;

const isEmpty = (f: ProjectFilters) => JSON.stringify(f) === JSON.stringify(EMPTY_FILTERS);

// every enrolled project in one list with three views: its status, its web
// numbers and its api numbers over a range of utc days. the view, the
// filters and the sort are in the address, so a list can be shared.
export function OperatorProjectsPage() {
  const [params, setParams] = useSearchParams();
  const view = readView(params.get("view"));
  const location = useLocation();
  const [selected, setSelected] = useState<Selection>(() => handedOver(location.state));
  const [chosen, setChosen] = useState<Record<ProjectsView, string[] | null>>(() => ({
    status: loadColumns("status"),
    web: loadColumns("web"),
    api: loadColumns("api"),
  }));

  const statusQ = statusQuery(params);
  const webState = readListState(params, webSort.options, DEFAULT_WEB_SORT);
  const apiRead = readListState(params, apiSort.options, DEFAULT_API_SORT);
  const apiState: ListState<ApiSort> = {
    ...apiRead,
    filters: { ...apiRead.filters, integrity: "" },
  };
  const listState: ListState<string> = view === "web" ? webState : apiState;
  const filters = listState.filters;
  const badDays = view !== "status" && dayPairError(filters) !== null;

  const statusList = useInfiniteQuery({
    ...operatorTenantsQuery(statusQ),
    enabled: view === "status",
  });
  const webList = useInfiniteQuery({
    ...webProjectsQuery(webState),
    enabled: view === "web" && !badDays,
  });
  const apiList = useInfiniteQuery({
    ...apiProjectsQuery(apiState),
    enabled: view === "api" && !badDays,
  });
  const list = view === "web" ? webList : view === "api" ? apiList : statusList;
  // the web and api lists say when their numbers were read; the status list
  // reads live, so its time is the time of the answer
  const readAt =
    view === "web"
      ? (webList.data?.pages[0]?.read_at ?? null)
      : view === "api"
        ? (apiList.data?.pages[0]?.read_at ?? null)
        : statusList.dataUpdatedAt > 0
          ? new Date(statusList.dataUpdatedAt)
          : null;

  const writeList = (next: ListState<string>) => {
    const out = writeListState(next, view === "web" ? DEFAULT_WEB_SORT : DEFAULT_API_SORT);
    out.set("view", view);
    setParams(out, { replace: true });
  };
  const setFilters = (f: ProjectFilters) => writeList({ ...listState, filters: f });
  const setStatusQ = (q: Partial<TenantListQuery>) =>
    setParams(statusParams({ ...statusQ, ...q }), { replace: true });

  // the search applies as the person types, after a short pause, so each key
  // press does not ask the server
  const currentQ = view === "status" ? statusQ.q : filters.q;
  const [search, setSearch] = useState(currentQ);
  useEffect(() => setSearch(currentQ), [currentQ]);
  useEffect(() => {
    if (search === currentQ) return;
    const t = setTimeout(() => {
      if (view === "status") setStatusQ({ q: search });
      else setFilters({ ...filters, q: search });
    }, SEARCH_DELAY_MS);
    return () => clearTimeout(t);
  });

  const sort = view === "status" ? statusQ.sort : listState.sort;
  const order = view === "status" ? statusQ.order : listState.order;
  const onSort = (key: string) => {
    if (view === "status")
      setStatusQ(nextSort({ sort: statusQ.sort, order }, key as TenantListQuery["sort"]));
    else writeList({ ...listState, ...nextSort({ sort, order }, key) });
  };

  const slot = (id: string) => selected.findIndex((s) => s.id === id);
  // every loaded row of the view, and the page of 25 the table shows
  const statusRows = badDays ? [] : (statusList.data?.pages.flatMap((p) => p.items) ?? []);
  const webRows = badDays ? [] : (webList.data?.pages.flatMap((p) => p.items) ?? []);
  const apiRows = badDays ? [] : (apiList.data?.pages.flatMap((p) => p.items) ?? []);
  const loaded =
    view === "status" ? statusRows.length : view === "web" ? webRows.length : apiRows.length;
  // pages of 25 like every table: the server answers 50 rows at a time,
  // and a page past the loaded rows asks for the next 50. the page goes
  // back to the first when the view, the filters or the sort change
  const listKey = JSON.stringify(view === "status" ? [view, statusQ] : [view, listState]);
  const [pageAt, setPageAt] = useState({ page: 0, key: listKey });
  const lastPage = Math.max(0, Math.ceil(loaded / PAGE_SIZE) - 1);
  const page =
    pageAt.key !== listKey ? 0 : list.hasNextPage ? pageAt.page : Math.min(pageAt.page, lastPage);
  const onPage = (p: number) => setPageAt({ page: p, key: listKey });
  // a page the loaded rows do not fill asks for the next rows
  const short = (page + 1) * PAGE_SIZE > loaded;
  useEffect(() => {
    if (short && list.hasNextPage && !list.isFetchingNextPage && !list.isPlaceholderData)
      list.fetchNextPage();
  }, [short, list]);
  const pageOf = <R,>(rows: R[]) => rows.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);

  // by default a column with no value on any row of the page (no previous
  // range to change from, no on-chain id) is left out; Columns still offers it
  const blank =
    chosen[view] !== null
      ? new Set<string>()
      : view === "web"
        ? blankColumns(pageOf(webRows))
        : view === "api"
          ? blankColumns(pageOf(apiRows))
          : new Set<string>();
  const shown = shownColumns(view, chosen[view], sort).filter(
    (id) => !blank.has(id) || columnsOf(view).find((c) => c.id === id)?.sortKey === sort,
  );
  const phone = phoneColumns(view, shown, sort);

  const first = view === "web" ? webList.data?.pages[0] : apiList.data?.pages[0];
  const resolved = view !== "status" && first ? { from: first.from, to: first.to } : null;
  const total = view === "status" ? null : (first?.total ?? null);
  const filtered =
    view === "status"
      ? statusQ.status.length > 0 || statusQ.review || statusQ.q.trim() !== ""
      : !isEmpty(filters);
  const clearAll = () => {
    setSearch("");
    if (view === "status") setParams({ view: "status" }, { replace: true });
    else setFilters(EMPTY_FILTERS);
  };

  // the selection keeps the names, so the strip names a project after the
  // list changed
  function select(ids: Set<string>, names: Map<string, string>) {
    setSelected((cur) => {
      const kept = cur.filter((s) => ids.has(s.id));
      const added = [...ids]
        .filter((id) => !kept.some((s) => s.id === id))
        .map((id) => ({ id, name: names.get(id) ?? id }));
      return [...kept, ...added].slice(0, MAX_COMPARE);
    });
  }
  const selection = (names: Map<string, string>) => ({
    selected: new Set(selected.map((s) => s.id)),
    onSelect: (ids: Set<string>) => select(ids, names),
    disabled: () => selected.length >= MAX_COMPARE,
  });

  const compare = new URLSearchParams({ tenants: selected.map((s) => s.id).join(",") });
  if (resolved) {
    compare.set("from", resolved.from);
    compare.set("to", resolved.to);
  }

  const state: "loading" | "error" | "data" = badDays
    ? "data"
    : list.isPending
      ? "loading"
      : list.error
        ? "error"
        : "data";
  const common = {
    sort: { key: sort, order },
    onSort,
    state,
    error: list.error,
    onRetry: () => list.refetch(),
    footer: {
      kind: "pager" as const,
      page,
      pageSize: PAGE_SIZE,
      // the web and api lists count every match; the status list counts
      // what it has loaded, with more to come
      total: total ?? loaded,
      more: total === null && list.hasNextPage,
      onPage,
    },
    loadingRows: 8,
    className: list.isPlaceholderData
      ? "opacity-60 transition-opacity duration-(--vs-duration-change) ease-standard"
      : "transition-opacity duration-(--vs-duration-change) ease-standard",
    empty: badDays
      ? { title: "The day filters do not fit", text: dayPairError(filters) ?? undefined }
      : filtered
        ? {
            title: "No project matches these filters",
            text: "Change or clear the filters.",
            action: (
              <Button variant="outline" size="sm" onClick={clearAll}>
                Clear filters
              </Button>
            ),
          }
        : { title: "No project is enrolled yet", text: "Enrolled projects show here." },
  };

  let table: ReactNode;
  if (view === "status") {
    const rows = statusRows;
    table = (
      <DataTable
        label="Projects"
        columns={pick(statusColumns(slot), shown, phone)}
        rows={pageOf(rows)}
        getRowKey={(t) => t.id}
        rowLabel={(t) => t.name}
        rowHref={(t) => `/operator/projects/${t.id}`}
        rowProps={(t) => ({ "data-tenant": t.id })}
        selection={selection(new Map(rows.map((t) => [t.id, t.name])))}
        {...common}
      />
    );
  } else if (view === "web") {
    const rows = webRows;
    table = (
      <DataTable
        label="Projects, web numbers"
        columns={pick(webColumns(resolved ?? webState.range, slot), shown, phone)}
        rows={pageOf(rows)}
        getRowKey={(p) => p.tenant.id}
        rowLabel={(p) => p.tenant.name}
        rowHref={(p) => `/operator/projects/${p.tenant.id}`}
        rowProps={(p) => ({ "data-tenant": p.tenant.id })}
        selection={selection(new Map(rows.map((p) => [p.tenant.id, p.tenant.name])))}
        {...common}
      />
    );
  } else {
    const rows = apiRows;
    table = (
      <DataTable
        label="Projects, API numbers"
        columns={pick(apiColumns(slot), shown, phone)}
        rows={pageOf(rows)}
        getRowKey={(p) => p.tenant.id}
        rowLabel={(p) => p.tenant.name}
        rowHref={(p) => `/operator/projects/${p.tenant.id}`}
        rowProps={(p) => ({ "data-tenant": p.tenant.id })}
        selection={selection(new Map(rows.map((p) => [p.tenant.id, p.tenant.name])))}
        {...common}
      />
    );
  }

  // the portfolio counts over the range of the list, so a count and the
  // rows it filters to agree
  const portfolio = usePortfolio(view === "status" ? { from: "", to: "" } : listState.range);
  const current = currentSegment(view, statusQ, filters);
  function pickSegment(seg: Segment) {
    setSearch(currentQ);
    if (seg.kind === "review") {
      setParams(statusParams({ ...statusQ, status: [], review: true }), { replace: true });
      return;
    }
    if (seg.kind === "all" || seg.kind === "status") {
      const statuses = seg.kind === "status" ? [seg.status] : [];
      if (view === "status") setStatusQ({ status: statuses, review: false });
      else setFilters({ ...EMPTY_FILTERS, q: filters.q, statuses });
      return;
    }
    // the data segments live in the web and api views
    const to: ProjectsView = seg.kind === "api" ? "api" : view === "api" ? "api" : "web";
    const next = switchView(params, to);
    const state = readListState<string>(
      next,
      to === "web" ? webSort.options : apiSort.options,
      to === "web" ? DEFAULT_WEB_SORT : DEFAULT_API_SORT,
    );
    const tri: Partial<ProjectFilters> =
      seg.kind === "web"
        ? { has_web_data: "true" }
        : seg.kind === "api"
          ? { has_api_data: "true" }
          : { has_web_data: "false", has_api_data: "false" };
    const out = writeListState(
      { ...state, filters: { ...EMPTY_FILTERS, q: state.filters.q, ...tri } },
      to === "web" ? DEFAULT_WEB_SORT : DEFAULT_API_SORT,
    );
    out.set("view", to);
    setParams(out, { replace: true });
  }

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <PageHeader
        title="All projects"
        items={[
          resolved ? (
            <span key="range">{`${formatDateRange(resolved.from, resolved.to)}, UTC`}</span>
          ) : null,
        ]}
        freshness={
          readAt
            ? {
                at: readAt,
                cadence:
                  view === "status"
                    ? "A status change shows at once."
                    : "Web numbers are read every hour. API numbers refresh every 5 minutes.",
              }
            : null
        }
        secondary={
          view !== "status" ? (
            <>
              <ListRange
                resolved={resolved}
                requested={listState.range}
                onRange={(range) => writeList({ ...listState, range })}
              />
              <AboutNumbers view={view} />
              {resolved && loaded > 0 && !badDays ? (
                <Button asChild variant="outline">
                  <a href={listExportHref(view, listState, resolved, loaded)} download>
                    <Download aria-hidden />
                    Export CSV
                  </a>
                </Button>
              ) : null}
            </>
          ) : null
        }
      />

      <PortfolioBar portfolio={portfolio} current={current} onPick={pickSegment} />

      <div className="flex min-w-0 flex-col gap-3">
        <div className="flex min-w-0 flex-wrap items-start gap-3">
          <Segmented
            label="View"
            value={view}
            options={VIEWS}
            onChange={(v) => setParams(switchView(params, v), { replace: true })}
            className="h-(--vs-control)"
          />
          <FilterBar
            collapsed
            className="min-w-0 flex-1 basis-80"
            search={search}
            onSearch={setSearch}
            searchLabel="Search projects"
            placeholder="Search projects"
            filters={
              view === "status"
                ? statusFilters(statusQ, setStatusQ)
                : listFilters(filters, setFilters, view === "web")
            }
            onClearAll={clearAll}
            actions={
              view === "status" ? null : (
                <ColumnMenu
                  view={view}
                  shown={shown}
                  sort={sort}
                  onChange={(ids) => {
                    saveColumns(view, ids);
                    setChosen((c) => ({ ...c, [view]: ids }));
                  }}
                />
              )
            }
          />
        </div>
        {badDays ? (
          <p role="alert" className="text-small text-critical">
            {dayPairError(filters)}
          </p>
        ) : null}
        {table}
        <SelectionBar
          selected={selected}
          onChange={setSelected}
          href={`/operator/compare?${compare.toString()}`}
          csv={exportPaths.compare(
            view === "api" ? "api" : "web",
            selected.map((x) => x.id),
            resolved ?? listState.range,
          )}
        />
      </div>
    </div>
  );
}

// the segment of the portfolio the list shows now, when its filters are
// exactly one segment's (the search aside)
function currentSegment(view: ProjectsView, q: TenantListQuery, f: ProjectFilters): string | null {
  if (view === "status") {
    if (q.review) return q.status.length === 0 ? "review" : null;
    if (q.status.length === 0) return "all";
    return q.status.length === 1 ? `status:${q.status[0]}` : null;
  }
  const others = (Object.keys(EMPTY_FILTERS) as (keyof ProjectFilters)[]).filter(
    (k) => !["q", "statuses", "has_web_data", "has_api_data"].includes(k),
  );
  if (others.some((k) => f[k] !== EMPTY_FILTERS[k])) return null;
  const tri = `${f.has_web_data}/${f.has_api_data}`;
  if (f.statuses.length === 0) {
    if (tri === "/") return "all";
    if (tri === "true/" && view === "web") return "web";
    if (tri === "/true" && view === "api") return "api";
    if (tri === "false/false") return "none";
    return null;
  }
  return f.statuses.length === 1 && tri === "/" ? `status:${f.statuses[0]}` : null;
}

// ---- filters

function statusFilters(q: TenantListQuery, set: (q: Partial<TenantListQuery>) => void): Filter[] {
  return [
    {
      key: "status",
      label: "Status",
      control: <StatusChoice value={q.status} onChange={(status) => set({ status })} />,
      active: q.status.length
        ? { label: q.status.map(statusText).join(", "), onClear: () => set({ status: [] }) }
        : null,
    },
    {
      key: "review",
      label: "Needs review",
      control: (
        <CheckField
          label="Needs review"
          hint="Pending projects, and active projects with a site in review."
          checked={q.review}
          onChange={(review) => set({ review })}
        />
      ),
      active: q.review ? { label: "Yes", onClear: () => set({ review: false }) } : null,
    },
  ];
}

const TRI_TEXT: Record<TriKey, { label: string; yes: string; no: string }> = {
  verified: { label: "Domain verified", yes: "At least one site", no: "No site" },
  stellar_toml: { label: "stellar.toml", yes: "Found on a site", no: "Not found" },
  has_web_data: { label: "Web data", yes: "Has page views", no: "No page views" },
  has_api_data: { label: "API data", yes: "Has requests", no: "No requests" },
};

const GRADE_TEXT: Record<Exclude<IntegrityFilter, "">, string> = {
  A: "Any grade given",
  B: "B or C",
  C: "C",
};

const COHORT_TEXT: Record<CohortKey, string> = {
  program: "SCF program",
  round: "SCF round",
  category: "SCF category",
};

function listFilters(f: ProjectFilters, set: (f: ProjectFilters) => void, web: boolean): Filter[] {
  const out: Filter[] = [
    {
      key: "status",
      label: "Status",
      control: <StatusChoice value={f.statuses} onChange={(statuses) => set({ ...f, statuses })} />,
      active: f.statuses.length
        ? {
            label: f.statuses.map(statusText).join(", "),
            onClear: () => set({ ...f, statuses: [] }),
          }
        : null,
    },
  ];
  for (const k of Object.keys(TRI_TEXT) as TriKey[]) {
    const t = TRI_TEXT[k];
    out.push({
      key: k,
      label: t.label,
      control: (
        <SelectField
          label={t.label}
          value={f[k]}
          onChange={(e) => set({ ...f, [k]: e.target.value as Tri })}
        >
          <option value="">Any</option>
          <option value="true">{t.yes}</option>
          <option value="false">{t.no}</option>
        </SelectField>
      ),
      active: f[k]
        ? { label: f[k] === "true" ? t.yes : t.no, onClear: () => set({ ...f, [k]: "" }) }
        : null,
    });
  }
  if (web)
    out.push({
      key: "grade",
      label: "Grade",
      control: (
        <SelectField
          label="Grade"
          value={f.integrity}
          onChange={(e) => set({ ...f, integrity: e.target.value as IntegrityFilter })}
        >
          <option value="">Any</option>
          <option value="A">{GRADE_TEXT.A}</option>
          <option value="B">{GRADE_TEXT.B}</option>
          <option value="C">{GRADE_TEXT.C}</option>
        </SelectField>
      ),
      active: f.integrity
        ? { label: GRADE_TEXT[f.integrity], onClear: () => set({ ...f, integrity: "" }) }
        : null,
    });
  out.push(
    dayFilter("enrolled_from", "Enrolled from", f, set),
    dayFilter("enrolled_to", "Enrolled to", f, set),
  );
  for (const k of COHORT_KEYS)
    out.push({
      key: k,
      label: COHORT_TEXT[k],
      control: <CohortChoice field={k} value={f[k]} onChange={(v) => set({ ...f, [k]: v })} />,
      active: f[k] ? { label: f[k], onClear: () => set({ ...f, [k]: "" }) } : null,
    });
  out.push(
    dayFilter("award_from", "Awarded from", f, set),
    dayFilter("award_to", "Awarded to", f, set),
  );
  return out;
}

function dayFilter(
  key: "enrolled_from" | "enrolled_to" | "award_from" | "award_to",
  label: string,
  f: ProjectFilters,
  set: (f: ProjectFilters) => void,
): Filter {
  return {
    key,
    label,
    control: <DayFilter label={label} value={f[key]} onChange={(v) => set({ ...f, [key]: v })} />,
    active: f[key] ? { label: formatDate(f[key]), onClear: () => set({ ...f, [key]: "" }) } : null,
  };
}

// a day filter takes effect once the field holds a whole day (or nothing),
// so a half typed day never clears the filter in the address
function DayFilter({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <DateField
      label={label}
      optional
      value={draft}
      onChange={(v) => {
        setDraft(v);
        const t = v.trim();
        if (t !== value && (t === "" || isIsoDay(t))) onChange(t);
      }}
    />
  );
}

function StatusChoice({
  value,
  onChange,
}: {
  value: TenantStatus[];
  onChange: (v: TenantStatus[]) => void;
}) {
  const id = useId();
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="pb-1 text-small font-medium text-fg">Status</legend>
      <div className="grid grid-cols-2 gap-2">
        {tenantStatus.options.map((s) => (
          <span key={s} className="flex items-center gap-2">
            <Checkbox
              id={`${id}-${s}`}
              checked={value.includes(s)}
              onCheckedChange={(c) =>
                onChange(c === true ? [...value, s] : value.filter((x) => x !== s))
              }
            />
            <label htmlFor={`${id}-${s}`} className="text-small text-fg">
              {statusText(s)}
            </label>
          </span>
        ))}
      </div>
    </fieldset>
  );
}

function CheckField({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  const id = useId();
  return (
    <span className="flex items-start gap-2">
      <Checkbox
        id={id}
        checked={checked}
        onCheckedChange={(c) => onChange(c === true)}
        aria-describedby={`${id}-h`}
        className="mt-1"
      />
      <span className="flex flex-col">
        <label htmlFor={id} className="text-small font-medium text-fg">
          {label}
        </label>
        <span id={`${id}-h`} className="text-caption text-fg-2">
          {hint}
        </span>
      </span>
    </span>
  );
}

// the cohort filters choose from the values operators confirmed; a value
// from the address that no project holds any more stays in the list
function CohortChoice({
  field,
  value,
  onChange,
}: {
  field: CohortKey;
  value: string;
  onChange: (v: string) => void;
}) {
  const list = useQuery(cohortListQuery);
  const key = field === "program" ? "programs" : field === "round" ? "rounds" : "categories";
  const opts = (list.data?.[key] ?? []).map((v) => v.value);
  if (value && !opts.includes(value)) opts.unshift(value);
  return (
    <SelectField
      label={COHORT_TEXT[field]}
      value={value}
      disabled={list.isPending}
      error={list.error ? "The cohort values did not load. Open the filters again." : undefined}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">{list.isPending ? "Loading" : "Any"}</option>
      {opts.map((o) => (
        <option key={o} value={o}>
          {o}
        </option>
      ))}
    </SelectField>
  );
}

// ---- columns

function ColumnMenu({
  view,
  shown,
  sort,
  onChange,
}: {
  view: ProjectsView;
  shown: string[];
  sort: string;
  onChange: (ids: string[] | null) => void;
}) {
  const specs = columnsOf(view);
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button variant="outline" className="max-lg:h-(--vs-control-touch)">
          <Columns3 aria-hidden />
          Columns
        </Button>
      </MenuTrigger>
      <MenuContent align="end" className="max-h-[70vh] overflow-y-auto">
        <MenuLabel>Show columns</MenuLabel>
        {specs.map((c) => {
          const sorted = c.sortKey === sort && c.id !== "name";
          return (
            <MenuCheckboxItem
              key={c.id}
              checked={shown.includes(c.id)}
              disabled={c.id === "name" || sorted}
              onCheckedChange={(on) =>
                onChange(on ? [...shown, c.id] : shown.filter((x) => x !== c.id))
              }
            >
              {c.label}
              {sorted ? <span className="ml-auto text-caption text-fg-2">Sorted</span> : null}
            </MenuCheckboxItem>
          );
        })}
        <MenuSeparator />
        <MenuItem onSelect={() => onChange(null)}>Show the default columns</MenuItem>
      </MenuContent>
    </Menu>
  );
}

// ---- comparison

// the selection bar floats at the foot of the list once a row is selected:
// how many, the way into the comparison, their csv and clear (escape)
function SelectionBar({
  selected,
  onChange,
  href,
  csv,
}: {
  selected: Selection;
  onChange: (s: Selection) => void;
  href: string;
  csv: string;
}) {
  const any = selected.length > 0;
  useEffect(() => {
    if (!any) return;
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      if ((e.target as HTMLElement | null)?.closest("dialog, [role=menu]")) return;
      onChange([]);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [any, onChange]);
  if (!any) return null;
  return (
    <section
      aria-label="Selected projects"
      className="sticky bottom-4 z-(--vs-layer-raised) mx-auto flex w-fit max-w-full animate-fade-in flex-wrap items-center gap-2 rounded-xl border border-line-2 bg-overlay py-1.5 pr-2 pl-4 shadow-overlay"
    >
      <span
        className="mr-2 text-body font-semibold text-fg tabular"
        aria-live="polite"
        title={selected.map((s) => s.name).join(", ")}
      >
        {selected.length} selected
        {selected.length >= MAX_COMPARE ? (
          <span className="ml-2 font-normal text-fg-3">at most {MAX_COMPARE}</span>
        ) : null}
      </span>
      <Button asChild size="sm" className="max-lg:h-(--vs-control-touch)">
        <Link to={href}>Open in Compare</Link>
      </Button>
      <Button asChild variant="outline" size="sm" className="max-lg:h-(--vs-control-touch)">
        <a href={csv} download>
          <Download aria-hidden />
          Export CSV
        </a>
      </Button>
      <Button
        variant="ghost"
        size="sm"
        aria-keyshortcuts="Escape"
        onClick={() => onChange([])}
        className="max-lg:h-(--vs-control-touch)"
      >
        Clear
        <span aria-hidden className="max-lg:hidden">
          <Kbd>Esc</Kbd>
        </span>
      </Button>
    </section>
  );
}

// ---- about

// what the numbers are, behind the (i) next to the period
function AboutNumbers({ view }: { view: "web" | "api" }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        aria-label="About these numbers"
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
      >
        <Info aria-hidden />
      </Button>
      <Dialog open={open} onOpenChange={setOpen} title="About these numbers" size="sm">
        <ul className="flex list-disc flex-col gap-1.5 pb-4 pl-5 text-body text-fg-2">
          <li>Every number comes from daily totals of UTC days.</li>
          <li>Today is partial. It holds the data read so far.</li>
          {view === "web" ? null : (
            <li>Latency percentiles come from summed duration buckets, not from averages.</li>
          )}
        </ul>
      </Dialog>
    </>
  );
}
