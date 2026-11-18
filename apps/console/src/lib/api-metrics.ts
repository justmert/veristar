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
import type { StatChange } from "@/components/stat-card";
import {
  formatChange,
  formatDate,
  formatDateRange,
  formatDateTimeUtc,
  formatLatency,
  formatPercent,
  formatPoints,
  formatTick,
  formatTimeRange,
  OPEN_BUCKET,
} from "@/lib/format";

// the labels of every range control; a phone shows them in a select
export const PRESETS: { value: ApiRangePreset; label: string }[] = [
  { value: "1h", label: "1 hour" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
  { value: "90d", label: "90 days" },
  { value: "12mo", label: "12 months" },
  { value: "24mo", label: "24 months" },
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

// a custom range's instant as the range control's field shows it:
// "2026-11-18 14:00", utc
export function instantToField(iso: string): string {
  return new Date(iso).toISOString().slice(0, 16).replace("T", " ");
}

const DAY = 86_400_000;

// the rules of contract 7.1 the shared checks do not know: the end at most
// 24 hours from now. the range control checks the format, the order, the
// first day (2000-01-01) and the length (800 days) itself.
export const CUSTOM_MIN_DAY = "2000-01-01";
export const CUSTOM_MAX_DAYS = 800;

export function validateCustomEnd(to: string, now: Date): string | null {
  return Date.parse(to) > now.getTime() + DAY ? "End no later than 24 hours from now." : null;
}

// a bucket that has not ended yet is partial: its count is still growing
export function isPartialBucket(at: string, r: ApiRange, now: number): boolean {
  return Date.parse(at) + r.bucket_seconds * 1000 > now;
}

// a latency percentile or a rate as text; null when the range has no
// request
export function latencyText(p: Percentile): string | null {
  return p === null ? null : formatLatency(p);
}

export function rateText(r: number | null): string | null {
  return r === null ? null : formatPercent(r);
}

export type Direction = "up" | "down" | "same";

export type Change = { text: string; direction: Direction } | { text: string; direction: null };

// relative change of a count or a percentile against the previous range
export function relativeChange(cur: number, prev: number): Change {
  if (prev === 0) return { text: "none in the previous range", direction: null };
  const r = (cur - prev) / prev;
  if (Math.abs(r) < 0.0005) return { text: "no change", direction: "same" };
  return { text: formatChange(r), direction: r > 0 ? "up" : "down" };
}

// rates change in percentage points; a relative change of a rate is a
// number nobody can act on
export function rateChange(cur: number | null, prev: number | null): Change {
  if (cur === null || prev === null)
    return { text: prev === null ? "no requests in the previous range" : "", direction: null };
  const d = cur - prev;
  if (Math.abs(d) < 0.0005) return { text: "no change", direction: "same" };
  return { text: formatPoints(d), direction: d > 0 ? "up" : "down" };
}

export function percentileChange(cur: Percentile, prev: Percentile): Change {
  if (prev === null) return { text: "no requests in the previous range", direction: null };
  if (cur === null) return { text: "", direction: null };
  if (cur === OPEN_BUCKET || prev === OPEN_BUCKET) {
    if (cur === prev) return { text: "above 10 s in both ranges", direction: "same" };
    return {
      text: `was ${formatLatency(prev)}`,
      direction: cur === OPEN_BUCKET ? "up" : "down",
    };
  }
  return relativeChange(cur, prev);
}

// the half-open range as people read it: day ranges end on their last day
export function formatRange(r: ApiRange): string {
  if (r.resolution === "day") {
    const last = new Date(Date.parse(r.to) - DAY);
    return formatDateRange(r.from, last);
  }
  return formatTimeRange(r.from, r.to);
}

// one bucket's start, at the precision of its resolution
export function formatBucket(at: string, r: ApiRange): string {
  if (r.resolution === "day") return formatDate(at);
  return formatDateTimeUtc(at);
}

// axis ticks: short, and the date only where the span needs it
export function tickFormatter(r: ApiRange): (at: string) => string {
  const span = Date.parse(r.to) - Date.parse(r.from);
  if (r.resolution === "day")
    return span > 120 * DAY ? (at) => formatTick(at, "month") : (at) => formatTick(at, "day");
  if (span <= DAY) return (at) => formatTick(at, "hour");
  return (at) => formatTick(at, "day");
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

// a change as a stat card shows it. a change with nothing to compare
// against has no line on the card: the page says once that the previous
// range had no requests.
export function toStatChange(
  c: Change,
  upIsBad: boolean | null,
  period = "previous range",
): StatChange | null {
  if (c.direction === null || !c.text) return null;
  const direction = c.direction === "same" ? "flat" : c.direction;
  const good = upIsBad === null || direction === "flat" ? null : (direction === "up") !== upIsBad;
  return { text: c.text, direction, good, period };
}
