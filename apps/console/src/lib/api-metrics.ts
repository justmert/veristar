// the pure part of the api traffic page: url state, range rules and number
// formats, kept apart from the components so tests can check them directly.
// the rules mirror docs/reference/control-plane-api.md section 7.

import type { ApiFilters, ApiRangeQuery, ApiRoutesQuery } from "@/api/endpoints";
import {
  type ApiRange,
  type ApiRangePreset,
  type ApiRouteSort,
  apiRangePreset,
  apiRouteSort,
  type Percentile,
} from "@/api/schemas";

export const PRESETS: { value: ApiRangePreset; label: string; span: string }[] = [
  { value: "1h", label: "1 hour", span: "hour" },
  { value: "24h", label: "24 hours", span: "24 hours" },
  { value: "7d", label: "7 days", span: "7 days" },
  { value: "30d", label: "30 days", span: "30 days" },
  { value: "90d", label: "90 days", span: "90 days" },
  { value: "12mo", label: "12 months", span: "12 months" },
  { value: "24mo", label: "24 months", span: "24 months" },
];

export const DEFAULT_PRESET: ApiRangePreset = "24h";
export const PAGE_SIZE = 50;

// the methods the collector keeps (collector/internal/schema: the semantic
// conventions v1.45.0 known methods, and _OTHER for every other one)
export const METHODS = [
  "GET",
  "HEAD",
  "POST",
  "PUT",
  "PATCH",
  "DELETE",
  "OPTIONS",
  "CONNECT",
  "TRACE",
  "QUERY",
  "_OTHER",
];

export type DashboardState = {
  range: ApiRangeQuery;
  filters: ApiFilters;
  sort: ApiRouteSort;
  order: "asc" | "desc";
};

// contract 7.7: route sorts ascending by default, every number descending
export function defaultOrder(sort: ApiRouteSort): "asc" | "desc" {
  return sort === "route" ? "asc" : "desc";
}

// the page keeps its whole state in the url, so a view can be shared,
// reloaded and walked back with the browser's back button. a malformed
// parameter is ignored and the default shown, since a hand-edited url is
// not an error worth a page of its own; the server still validates all of it.
export function readState(params: URLSearchParams): DashboardState {
  const preset = params.get("range");
  const from = params.get("from");
  const to = params.get("to");
  let range: ApiRangeQuery = { range: DEFAULT_PRESET };
  if (preset === "custom" && from && to && isInstant(from) && isInstant(to)) {
    range = { range: "custom", from, to };
  } else {
    const p = apiRangePreset.safeParse(preset);
    if (p.success) range = { range: p.data };
  }
  const s = apiRouteSort.safeParse(params.get("sort"));
  const sort = s.success ? s.data : "requests";
  const o = params.get("order");
  const order = o === "asc" || o === "desc" ? o : defaultOrder(sort);
  return {
    range,
    // has() keeps an empty value apart from an absent one (contract 7.1)
    filters: {
      service: params.has("service") ? (params.get("service") ?? "") : null,
      route: params.has("route") ? (params.get("route") ?? "") : null,
      method: params.has("method") ? (params.get("method") ?? "") : null,
    },
    sort,
    order,
  };
}

export function writeState(s: DashboardState): URLSearchParams {
  const p = new URLSearchParams();
  p.set("range", s.range.range);
  if (s.range.range === "custom") {
    p.set("from", s.range.from);
    p.set("to", s.range.to);
  }
  if (s.filters.service !== null) p.set("service", s.filters.service);
  if (s.filters.route !== null) p.set("route", s.filters.route);
  if (s.filters.method !== null) p.set("method", s.filters.method);
  if (s.sort !== "requests") p.set("sort", s.sort);
  if (s.order !== defaultOrder(s.sort)) p.set("order", s.order);
  return p;
}

export function routesQuery(s: DashboardState, cursor: string | null): ApiRoutesQuery {
  return { sort: s.sort, order: s.order, limit: PAGE_SIZE, cursor };
}

function isInstant(v: string): boolean {
  return (
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(v) &&
    !Number.isNaN(Date.parse(v))
  );
}

// the custom range form uses datetime-local inputs, read as utc because
// every bucket of the api is a utc minute, hour or day
export function localInputToUtc(v: string): string | null {
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})(:\d{2})?$/.exec(v);
  if (!m) return null;
  const iso = `${m[1]}T${m[2]}${m[3] ?? ":00"}Z`;
  return Number.isNaN(Date.parse(iso)) ? null : iso;
}

export function utcToLocalInput(iso: string): string {
  return new Date(iso).toISOString().slice(0, 16);
}

const DAY = 86_400_000;
const EARLIEST = Date.parse("2000-01-01T00:00:00Z");

// the bounds of contract 7.1, checked before sending so the form can say
// which field is wrong; the server checks them again
export function validateCustom(
  from: string,
  to: string,
  now: Date,
): { from?: string; to?: string } {
  const f = localInputToUtc(from);
  const t = localInputToUtc(to);
  const out: { from?: string; to?: string } = {};
  if (!f) out.from = "Enter a start date and time.";
  if (!t) out.to = "Enter an end date and time.";
  if (!f || !t) return out;
  const fm = Date.parse(f);
  const tm = Date.parse(t);
  if (fm < EARLIEST) out.from = "Start on or after 1 January 2000.";
  if (tm > now.getTime() + DAY) out.to = "End no later than 24 hours from now.";
  if (fm >= tm) out.to = "End after the start.";
  else if (tm - fm > 800 * DAY) out.to = "A custom range covers at most 800 days.";
  return out;
}

// seconds as people read latency: milliseconds below a second
export function formatSeconds(s: number): string {
  if (s === 0) return "0 ms";
  if (s < 1) {
    const ms = s * 1000;
    return `${ms < 10 ? trim(ms.toFixed(1)) : Math.round(ms).toString()} ms`;
  }
  return `${trim(s.toFixed(2))} s`;
}

function trim(v: string): string {
  return v.includes(".") ? v.replace(/\.?0+$/, "") : v;
}

// contract 7.4: the open bucket is never a number
export const OPEN_BUCKET = ">10 s";

export function formatPercentile(p: Percentile): string | null {
  if (p === null) return null;
  return p === OPEN_BUCKET ? OPEN_BUCKET : formatSeconds(p);
}

export function formatRate(r: number | null): string | null {
  if (r === null) return null;
  if (r === 0) return "0%";
  const pct = r * 100;
  if (pct < 0.01) return "<0.01%";
  return `${trim(pct.toFixed(pct < 10 ? 2 : 1))}%`;
}

export type Direction = "up" | "down" | "same";

export type Change = { text: string; direction: Direction } | { text: string; direction: null };

const pct = new Intl.NumberFormat("en", { maximumFractionDigits: 1, signDisplay: "always" });

// relative change of a count or a percentile against the previous range
export function relativeChange(cur: number, prev: number): Change {
  if (prev === 0) return { text: "none in the previous range", direction: null };
  const r = (cur - prev) / prev;
  if (Math.abs(r) < 0.0005) return { text: "no change", direction: "same" };
  return { text: `${pct.format(r * 100)}%`, direction: r > 0 ? "up" : "down" };
}

const pts = new Intl.NumberFormat("en", { maximumFractionDigits: 2, signDisplay: "always" });

// rates change in percentage points; a relative change of a rate is a
// number nobody can act on
export function rateChange(cur: number | null, prev: number | null): Change {
  if (cur === null || prev === null)
    return { text: prev === null ? "no requests in the previous range" : "", direction: null };
  const d = (cur - prev) * 100;
  if (Math.abs(d) < 0.005) return { text: "no change", direction: "same" };
  return { text: `${pts.format(d)} pt`, direction: d > 0 ? "up" : "down" };
}

export function percentileChange(cur: Percentile, prev: Percentile): Change {
  if (prev === null) return { text: "no requests in the previous range", direction: null };
  if (cur === null) return { text: "", direction: null };
  if (cur === OPEN_BUCKET || prev === OPEN_BUCKET) {
    if (cur === prev) return { text: "above 10 s in both ranges", direction: "same" };
    return {
      text: `was ${formatPercentile(prev)}`,
      direction: cur === OPEN_BUCKET ? "up" : "down",
    };
  }
  return relativeChange(cur, prev);
}

const dayFmt = new Intl.DateTimeFormat("en", { dateStyle: "medium", timeZone: "UTC" });
const minuteFmt = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "UTC",
});
const clockFmt = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: "UTC",
});
const shortDayFmt = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  timeZone: "UTC",
});
const monthFmt = new Intl.DateTimeFormat("en", {
  month: "short",
  year: "numeric",
  timeZone: "UTC",
});

// the half-open range as people read it: day ranges end on their last day
export function formatRange(r: ApiRange): string {
  if (r.resolution === "day") {
    const last = new Date(Date.parse(r.to) - DAY).toISOString();
    return `${dayFmt.format(new Date(r.from))} to ${dayFmt.format(new Date(last))} (UTC)`;
  }
  return `${minuteFmt.format(new Date(r.from))} to ${minuteFmt.format(new Date(r.to))} UTC`;
}

// one bucket's start, at the precision of its resolution
export function formatBucket(at: string, r: ApiRange): string {
  const d = new Date(at);
  if (r.resolution === "day") return dayFmt.format(d);
  return `${minuteFmt.format(d)} UTC`;
}

// axis ticks: short, and the date only where the span needs it
export function tickFormatter(r: ApiRange): (at: string) => string {
  const span = Date.parse(r.to) - Date.parse(r.from);
  if (r.resolution === "day")
    return span > 120 * DAY
      ? (at) => monthFmt.format(new Date(at))
      : (at) => shortDayFmt.format(new Date(at));
  if (span <= DAY) return (at) => clockFmt.format(new Date(at));
  return (at) => shortDayFmt.format(new Date(at));
}

export const RESOLUTION_LABEL: Record<ApiRange["resolution"], string> = {
  minute: "per minute",
  hour: "per hour",
  day: "per day",
};

export const RESOLUTION_NOTE: Record<NonNullable<ApiRange["resolution_note"]>, string> = {
  older_than_minute_retention:
    "Minute data is kept for a limited time, so this range is shown per hour.",
  older_than_hourly_retention:
    "Hourly data is kept for a limited time, so this range is shown per day.",
};

// latency in charts: the open bucket is drawn on the 10 s line and marked,
// never placed at an invented height above it
export const OPEN_BUCKET_CEILING = 10;

export function plotPercentile(p: Percentile): number | null {
  if (p === null) return null;
  return p === OPEN_BUCKET ? OPEN_BUCKET_CEILING : p;
}
