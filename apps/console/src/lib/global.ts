// the pure part of the global web and api views and the comparison: url
// state and query strings, kept apart from the components so
// tests can check them directly. the rules mirror
// docs/reference/control-plane-api.md section 8.

import { type TenantStatus, tenantStatus } from "@/api/schemas";

// "" means the filter is not sent
export type Tri = "" | "true" | "false";

export type ProjectFilters = {
  statuses: TenantStatus[];
  verified: Tri;
  stellar_toml: Tri;
  has_web_data: Tri;
  has_api_data: Tri;
  enrolled_from: string;
  enrolled_to: string;
  q: string;
  // 19.1 the cohort filters; they also set the population every percentile
  // is computed in
  program: string;
  round: string;
  category: string;
  award_from: string;
  award_to: string;
  // the web list only: projects graded this grade or worse ("" any grade)
  integrity: IntegrityFilter;
};

export type IntegrityFilter = "" | "A" | "B" | "C";

// utc days yyyy-mm-dd; "" leaves the end to the server's default (8.1)
export type DayRange = { from: string; to: string };

export type ListState<S extends string> = {
  range: DayRange;
  filters: ProjectFilters;
  sort: S;
  order: "asc" | "desc";
};

export const EMPTY_FILTERS: ProjectFilters = {
  statuses: [],
  verified: "",
  stellar_toml: "",
  has_web_data: "",
  has_api_data: "",
  enrolled_from: "",
  enrolled_to: "",
  q: "",
  program: "",
  round: "",
  category: "",
  award_from: "",
  award_to: "",
  integrity: "",
};

export const COHORT_KEYS = ["program", "round", "category"] as const;
export type CohortKey = (typeof COHORT_KEYS)[number];

export const TRI_KEYS = ["verified", "stellar_toml", "has_web_data", "has_api_data"] as const;
export type TriKey = (typeof TRI_KEYS)[number];

export const MAX_COMPARE = 5;
export const PAGE_SIZE = 50;

const DAY_RE = /^\d{4}-\d{2}-\d{2}$/;

export function isDay(v: string): boolean {
  return DAY_RE.test(v) && !Number.isNaN(Date.parse(`${v}T00:00:00Z`));
}

function tri(v: string | null): Tri {
  return v === "true" || v === "false" ? v : "";
}

function integrityFilter(v: string | null): IntegrityFilter {
  return v === "A" || v === "B" || v === "C" ? v : "";
}

// names sort a to z first; every number starts with the largest, which is
// also the contract's default order
export function firstOrder(sort: string): "asc" | "desc" {
  return sort === "name" ? "asc" : "desc";
}

// the list pages keep their whole state in the address, so a filtered and
// sorted view can be shared and reloaded. a malformed value is dropped and
// the default shown; the server validates everything again.
export function readListState<S extends string>(
  params: URLSearchParams,
  sorts: readonly S[],
  defaultSort: S,
): ListState<S> {
  const statuses = (params.get("status") ?? "")
    .split(",")
    .filter((s): s is TenantStatus => tenantStatus.safeParse(s).success);
  const day = (k: string) => {
    const v = params.get(k) ?? "";
    return isDay(v) ? v : "";
  };
  const s = params.get("sort") ?? "";
  const sort = (sorts as readonly string[]).includes(s) ? (s as S) : defaultSort;
  const o = params.get("order");
  return {
    range: { from: day("from"), to: day("to") },
    filters: {
      statuses: [...new Set(statuses)],
      verified: tri(params.get("verified")),
      stellar_toml: tri(params.get("stellar_toml")),
      has_web_data: tri(params.get("has_web_data")),
      has_api_data: tri(params.get("has_api_data")),
      enrolled_from: day("enrolled_from"),
      enrolled_to: day("enrolled_to"),
      q: (params.get("q") ?? "").slice(0, 100),
      program: (params.get("program") ?? "").slice(0, 80),
      round: (params.get("round") ?? "").slice(0, 80),
      category: (params.get("category") ?? "").slice(0, 80),
      award_from: day("award_from"),
      award_to: day("award_to"),
      integrity: integrityFilter(params.get("integrity_at_or_below")),
    },
    sort,
    order: o === "asc" || o === "desc" ? o : "desc",
  };
}

// the query parameters of contract 8.1 and 8.2, in a fixed order; empty
// values are not sent
export function listParams<S extends string>(s: ListState<S>): [string, string][] {
  const f = s.filters;
  const out: [string, string][] = [
    ["from", s.range.from],
    ["to", s.range.to],
    ["status", f.statuses.join(",")],
    ["verified", f.verified],
    ["stellar_toml", f.stellar_toml],
    ["has_web_data", f.has_web_data],
    ["has_api_data", f.has_api_data],
    ["enrolled_from", f.enrolled_from],
    ["enrolled_to", f.enrolled_to],
    ["q", f.q.trim()],
    ["program", f.program],
    ["round", f.round],
    ["category", f.category],
    ["award_from", f.award_from],
    ["award_to", f.award_to],
    ["integrity_at_or_below", f.integrity],
    ["sort", s.sort],
    ["order", s.order],
  ];
  return out.filter(([, v]) => v !== "");
}

export function writeListState<S extends string>(s: ListState<S>, defaultSort: S): URLSearchParams {
  const p = new URLSearchParams();
  for (const [k, v] of listParams(s)) {
    if (k === "sort" && v === defaultSort) continue;
    if (k === "order" && v === "desc") continue;
    p.set(k, v);
  }
  return p;
}

// 8.2 day pairs: an end before its start is 400 invalid_query, so the form
// says so and the list is not asked
export function dayPairError(f: ProjectFilters): string | null {
  if (f.enrolled_from && f.enrolled_to && f.enrolled_to < f.enrolled_from)
    return "Enrolled to is before Enrolled from. Change one of them.";
  if (f.award_from && f.award_to && f.award_to < f.award_from)
    return "Awarded to is before Awarded from. Change one of them.";
  return null;
}

export function activeFilterCount(f: ProjectFilters): number {
  return (
    (f.statuses.length ? 1 : 0) +
    TRI_KEYS.filter((k) => f[k] !== "").length +
    (f.enrolled_from ? 1 : 0) +
    (f.enrolled_to ? 1 : 0) +
    (f.q.trim() ? 1 : 0) +
    COHORT_KEYS.filter((k) => f[k] !== "").length +
    (f.award_from ? 1 : 0) +
    (f.award_to ? 1 : 0) +
    (f.integrity ? 1 : 0)
  );
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// the comparison's projects: lowercase uuids, distinct, at most five, in the
// order given (8.4); anything else in the address is dropped
export function readCompare(params: URLSearchParams): { tenants: string[]; range: DayRange } {
  const ids = (params.get("tenants") ?? "").split(",").filter((v) => UUID_RE.test(v));
  const day = (k: string) => {
    const v = params.get(k) ?? "";
    return isDay(v) ? v : "";
  };
  return {
    tenants: [...new Set(ids)].slice(0, MAX_COMPARE),
    range: { from: day("from"), to: day("to") },
  };
}

export function compareSearch(tenants: string[], range: DayRange): string {
  const p = new URLSearchParams();
  p.set("tenants", tenants.join(","));
  if (range.from) p.set("from", range.from);
  if (range.to) p.set("to", range.to);
  return p.toString();
}

function utcDay(d: Date): string {
  return d.toISOString().slice(0, 10);
}

// the last n utc days ending today, both included
export function lastDays(n: number, now: Date): DayRange {
  const to = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const from = new Date(to.getTime() - (n - 1) * 86_400_000);
  return { from: utcDay(from), to: utcDay(to) };
}

export const RANGE_PRESETS = [7, 30, 90, 365] as const;

// which preset a resolved range is, if any
export function presetOf(from: string, to: string, now: Date): number | null {
  for (const n of RANGE_PRESETS) {
    const r = lastDays(n, now);
    if (r.from === from && r.to === to) return n;
  }
  return null;
}

// the bounds of 8.1, checked before sending so the form can say which field
// is wrong; the server checks them again
export function validateDays(
  r: DayRange,
  now: Date,
  maxDays = 366,
): { from?: string; to?: string } {
  const out: { from?: string; to?: string } = {};
  if (!isDay(r.from)) out.from = "Enter a start day.";
  if (!isDay(r.to)) out.to = "Enter an end day.";
  if (out.from || out.to) return out;
  const today = lastDays(1, now).to;
  if (r.to > today) out.to = "End no later than today (UTC).";
  else if (r.to < r.from) out.to = "End on or after the start.";
  else if ((Date.parse(r.to) - Date.parse(r.from)) / 86_400_000 + 1 > maxDays)
    out.to = `A range covers at most ${maxDays} days.`;
  return out;
}
