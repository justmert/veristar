// the pure part of the operator project list: its three views (status, web,
// api), the address that holds the view with its filters and sort, and the
// columns each view can show. kept apart from the page so tests check it
// directly.

import type { TenantListQuery } from "@/api/endpoints";
import { exportPaths } from "@/api/onchain";
import {
  type ApiSort,
  apiSort,
  type TenantStatus,
  tenantStatus,
  type WebSort,
  webSort,
} from "@/api/schemas";
import {
  type DayRange,
  firstOrder,
  type ListState,
  listParams,
  readListState,
  writeListState,
} from "@/lib/global";

export type ProjectsView = "status" | "web" | "api";

export const VIEWS: readonly { value: ProjectsView; label: string }[] = [
  { value: "status", label: "Status" },
  { value: "web", label: "Web" },
  { value: "api", label: "API" },
];

export function readView(v: string | null): ProjectsView {
  return v === "web" || v === "api" ? v : "status";
}

export const DEFAULT_WEB_SORT: WebSort = "pageviews";
export const DEFAULT_API_SORT: ApiSort = "requests";

// ------------------------------------------------------------ status view

export const STATUS_SORTS = ["name", "created_at", "status_changed_at"] as const;
export type StatusSort = (typeof STATUS_SORTS)[number];

// the status view reads the project list of contract 5.3. values outside its
// sets are dropped here rather than sent, because the server answers 400
export function statusQuery(p: URLSearchParams): TenantListQuery {
  const statuses = (p.get("status") ?? "")
    .split(",")
    .filter((s): s is TenantStatus => tenantStatus.safeParse(s).success);
  const sort = p.get("sort") ?? "";
  return {
    status: [...new Set(statuses)],
    review: p.get("review") === "true",
    q: (p.get("q") ?? "").slice(0, 100),
    sort: (STATUS_SORTS as readonly string[]).includes(sort) ? (sort as StatusSort) : "created_at",
    order: p.get("order") === "asc" ? "asc" : "desc",
  };
}

export function statusParams(q: TenantListQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (q.status.length) p.set("status", q.status.join(","));
  if (q.review) p.set("review", "true");
  if (q.q.trim()) p.set("q", q.q.trim());
  if (q.sort !== "created_at") p.set("sort", q.sort);
  if (q.order !== "desc") p.set("order", q.order);
  return p;
}

// a header click flips the order of the current sort, or starts a new one in
// its natural order: names a to z, everything else largest or newest first
export function nextSort<S extends string>(
  current: { sort: S; order: "asc" | "desc" },
  key: S,
): { sort: S; order: "asc" | "desc" } {
  if (key === current.sort) return { sort: key, order: current.order === "asc" ? "desc" : "asc" };
  return { sort: key, order: firstOrder(key) };
}

// ----------------------------------------------------------- view switch

// the address of another view. the search and the status filter mean the
// same in every view and stay; the web and api views share every filter,
// the range and every sort they both know. a filter the next view cannot
// apply is dropped, so the filters shown are the filters sent.
export function switchView(params: URLSearchParams, to: ProjectsView): URLSearchParams {
  const from = readView(params.get("view"));
  if (from === to) return new URLSearchParams(params);
  const keepCommon = (out: URLSearchParams) => {
    for (const k of ["q", "status"]) {
      const v = params.get(k);
      if (v) out.set(k, v);
    }
    return out;
  };
  if (to === "status") return keepCommon(new URLSearchParams());
  const sorts = to === "web" ? webSort.options : apiSort.options;
  const fallback = to === "web" ? DEFAULT_WEB_SORT : DEFAULT_API_SORT;
  if (from === "status") {
    const out = keepCommon(new URLSearchParams());
    out.set("view", to);
    return out;
  }
  const state = readListState<string>(params, sorts, fallback);
  const filters = to === "api" ? { ...state.filters, integrity: "" as const } : state.filters;
  const out = writeListState({ ...state, filters }, fallback);
  out.set("view", to);
  return out;
}

// -------------------------------------------------------------- columns

// the columns a person can show or hide in a view, by id. a column that
// sorts carries its sort key as its id, so every sort key of contract 8.3
// and 8.5 is a column header.
export type ColumnSpec = { id: string; label: string; sortKey?: string; shown: boolean };

const growth = [
  { id: "visitors_growth", label: "Visitors change" },
  { id: "pageviews_growth", label: "Page views change" },
  { id: "requests_growth", label: "Requests change" },
  { id: "error_rate_change", label: "Error rate change" },
  { id: "onchain_operations_growth", label: "On-chain change" },
  { id: "visitors_percentile", label: "Visitors percentile" },
  { id: "pageviews_percentile", label: "Page views percentile" },
  { id: "requests_percentile", label: "Requests percentile" },
  { id: "error_rate_percentile", label: "Error rate percentile" },
  { id: "onchain_operations_percentile", label: "On-chain percentile" },
] as const;

const sorted = (id: string, label: string, shown = false): ColumnSpec => ({
  id,
  label,
  sortKey: id,
  shown,
});

// the status has its own column right after the name in every view
export const WEB_COLUMNS: ColumnSpec[] = [
  sorted("name", "Project", true),
  sorted("status", "Status", true),
  sorted("created_at", "Enrolled"),
  { id: "cohort", label: "Cohort", shown: false },
  sorted("integrity", "Grade", true),
  sorted("pageviews", "Page views", true),
  sorted("visitors", "Visitors", true),
  sorted("visits", "Visits"),
  sorted("bounces", "Bounces"),
  sorted("totaltime", "Total visit time"),
  sorted("bounce_rate", "Bounce rate", true),
  sorted("avg_visit_duration", "Average visit"),
  sorted("quarantine_share", "Held back", true),
  sorted("quarantine_pageviews", "Held back page views"),
  sorted("quarantine_visitors", "Held back visitors"),
  sorted("ingest_requests", "Gateway requests"),
  sorted("ingest_rejected", "Refused requests"),
  sorted("rejected_share", "Refused"),
  sorted("unmeasured_share", "Not measured"),
  sorted("sites", "Sites"),
  sorted("sites_verified", "Verified sites"),
  sorted("sites_with_stellar_toml", "Sites with stellar.toml"),
  ...growth.map((g) => sorted(g.id, g.label, g.id === "visitors_growth")),
  { id: "onchain", label: "On-chain", shown: true },
  { id: "countries", label: "Top countries", shown: false },
];

export const API_COLUMNS: ColumnSpec[] = [
  sorted("name", "Project", true),
  sorted("status", "Status", true),
  sorted("created_at", "Enrolled"),
  { id: "cohort", label: "Cohort", shown: false },
  sorted("requests", "Requests", true),
  sorted("requests_4xx", "4xx responses"),
  sorted("requests_5xx", "5xx responses"),
  sorted("rate_4xx", "4xx rate", true),
  sorted("rate_5xx", "5xx rate", true),
  sorted("p50", "p50 latency"),
  sorted("p95", "p95 latency", true),
  sorted("p99", "p99 latency"),
  sorted("services", "Services"),
  sorted("routes", "Routes"),
  sorted("sites", "Sites"),
  ...growth.map((g) =>
    sorted(g.id, g.label, g.id === "requests_growth" || g.id === "error_rate_change"),
  ),
  { id: "source", label: "Data source", shown: true },
  { id: "onchain", label: "On-chain", shown: false },
];

export const STATUS_COLUMNS: ColumnSpec[] = [
  sorted("name", "Project", true),
  { id: "status", label: "Status", shown: true },
  { id: "sites", label: "Sites", shown: true },
  { id: "sites_verified", label: "Verified sites", shown: true },
  { id: "sites_with_stellar_toml", label: "Sites with stellar.toml", shown: true },
  sorted("created_at", "Enrolled", true),
  sorted("status_changed_at", "Status since", true),
];

export function columnsOf(view: ProjectsView): ColumnSpec[] {
  return view === "web" ? WEB_COLUMNS : view === "api" ? API_COLUMNS : STATUS_COLUMNS;
}

// the ids shown: the person's choice (or the view's default), always the
// project name, and always the column the list is sorted by, so a sort from
// a shared address is never invisible
export function shownColumns(view: ProjectsView, chosen: string[] | null, sort: string): string[] {
  const specs = columnsOf(view);
  const base = chosen ?? specs.filter((c) => c.shown).map((c) => c.id);
  const out = new Set(base.filter((id) => specs.some((c) => c.id === id)));
  out.add("name");
  const bySort = specs.find((c) => c.sortKey === sort);
  if (bySort) out.add(bySort.id);
  return specs.filter((c) => out.has(c.id)).map((c) => c.id);
}

// the numbers a phone row keeps: the view's two main numbers and the sort
// column in place of the third
const PHONE: Record<ProjectsView, string[]> = {
  status: ["status", "sites", "created_at"],
  web: ["pageviews", "visitors", "integrity"],
  api: ["requests", "rate_5xx", "p95"],
};

export function phoneColumns(view: ProjectsView, shown: string[], sort: string): string[] {
  const base = PHONE[view].filter((id) => shown.includes(id));
  const bySort = columnsOf(view).find((c) => c.sortKey === sort && c.id !== "name");
  if (!bySort || base.includes(bySort.id)) return base.slice(0, 3);
  return [...base.slice(0, 2), bySort.id];
}

const STORE = (view: ProjectsView) => `veristar.operator.projects.columns.${view}`;

// the columns a person chose stay in this browser only; a blocked storage
// falls back to the defaults
export function loadColumns(view: ProjectsView): string[] | null {
  try {
    const raw = window.localStorage.getItem(STORE(view));
    if (!raw) return null;
    const v: unknown = JSON.parse(raw);
    return Array.isArray(v) && v.every((x) => typeof x === "string") ? v : null;
  } catch {
    return null;
  }
}

export function saveColumns(view: ProjectsView, ids: string[] | null) {
  try {
    if (ids === null) window.localStorage.removeItem(STORE(view));
    else window.localStorage.setItem(STORE(view), JSON.stringify(ids));
  } catch {
    // storage is a convenience; the defaults still work
  }
}

// a percentile says little in a small cohort, so it shows from this size up
export const MIN_COHORT = 5;

// exports exactly the rows on screen: the list's query with the range the
// server answered for, and as many rows as are loaded
export function listExportHref(
  view: "web" | "api",
  s: ListState<string>,
  resolved: DayRange,
  rows: number,
): string {
  const q = new URLSearchParams(listParams(s));
  q.set("from", resolved.from);
  q.set("to", resolved.to);
  return exportPaths.list(view, q, rows);
}
