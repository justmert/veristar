// the pure part of the web dashboard: its address state, the range it asks
// for, trends against the previous period and how values read. kept apart
// from the components so tests can check them directly. the rules mirror
// docs/reference/control-plane-api.md section 18.

import type { WebBand, WebDimension, WebPoint, WebQuery, WebTotals } from "@/api/web";
import type { Trend } from "@/components/stat-tile";
import { type DayRange, formatDuration, isDay, lastDays, validateDays } from "@/lib/global";

export type WebPreset = "today" | "7" | "30" | "90" | "custom";

export const WEB_PRESETS: readonly { value: WebPreset; label: string }[] = [
  { value: "today", label: "Today" },
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "custom", label: "Custom" },
];

export type WebMetric = "pageviews" | "visitors" | "visits" | "bounce_rate" | "avg_visit_duration";

export type WebState = {
  preset: WebPreset;
  // the days of a custom range; ignored for the presets
  custom: DayRange;
  site: string | null;
  band: WebBand;
  metric: WebMetric;
};

const METRICS: readonly WebMetric[] = [
  "pageviews",
  "visitors",
  "visits",
  "bounce_rate",
  "avg_visit_duration",
];

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

// the dashboard keeps its whole state in the address, so a view can be
// shared and reloaded. a malformed value is dropped and the default shown;
// the server validates everything again.
export function readWebState(params: URLSearchParams): WebState {
  const p = params.get("range");
  const preset: WebPreset = WEB_PRESETS.some((x) => x.value === p) ? (p as WebPreset) : "30";
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  const site = params.get("site");
  const metric = params.get("metric");
  return {
    preset: preset === "custom" && !(isDay(from) && isDay(to)) ? "30" : preset,
    custom: { from: isDay(from) ? from : "", to: isDay(to) ? to : "" },
    site: site && UUID_RE.test(site) ? site : null,
    band: params.get("band") === "quarantine" ? "quarantine" : "primary",
    metric: METRICS.includes(metric as WebMetric) ? (metric as WebMetric) : "pageviews",
  };
}

export function writeWebState(s: WebState): URLSearchParams {
  const out = new URLSearchParams();
  if (s.preset !== "30") out.set("range", s.preset);
  if (s.preset === "custom") {
    out.set("from", s.custom.from);
    out.set("to", s.custom.to);
  }
  if (s.site) out.set("site", s.site);
  if (s.band !== "primary") out.set("band", s.band);
  if (s.metric !== "pageviews") out.set("metric", s.metric);
  return out;
}

// the utc days a state covers
export function webDays(s: WebState, now: Date): DayRange {
  if (s.preset === "custom") return s.custom;
  if (s.preset === "today") return lastDays(1, now);
  return lastDays(Number(s.preset), now);
}

export function webQuery(s: WebState, now: Date): WebQuery {
  const d = webDays(s, now);
  return { from: d.from, to: d.to, site: s.site, band: s.band };
}

// the web dashboard allows 400 days (18.1), more than the 366 of the
// operator views; the form says so before asking
export const WEB_MAX_DAYS = 400;

export function validateWebDays(r: DayRange, now: Date): { from?: string; to?: string } {
  return validateDays(r, now, WEB_MAX_DAYS);
}

export function dayCount(r: DayRange): number {
  return Math.round((Date.parse(r.to) - Date.parse(r.from)) / 86_400_000) + 1;
}

// a range cut to its last max days, for the views next to the dashboard that
// allow fewer days than it does (on-chain 15, integrity 17.2: 366)
export function lastDaysOf(r: DayRange, max: number): DayRange {
  if (dayCount(r) <= max) return r;
  const from = new Date(Date.parse(r.to) - (max - 1) * 86_400_000).toISOString().slice(0, 10);
  return { from, to: r.to };
}

// a single day is shown per hour, from the engine; longer ranges per day,
// from the aggregates
export function isSingleDay(q: { from: string; to: string }): boolean {
  return q.from === q.to;
}

export function periodLabel(days: number): string {
  return days === 1 ? "previous day" : `previous ${days} days`;
}

export function metricValue(t: WebTotals, m: WebMetric): number | null {
  return t[m];
}

export function pointValue(p: WebPoint, m: WebMetric): number | null {
  return p[m];
}

const pct = (r: number) => (r === 0 ? "0%" : r < 0.001 ? "<0.1%" : `${(r * 100).toFixed(1)}%`);

export function formatShare(r: number | null): string {
  return r === null ? "n/a" : pct(r);
}

export function formatMetric(m: WebMetric, v: number | null): string {
  if (v === null) return "n/a";
  if (m === "bounce_rate") return pct(v);
  if (m === "avg_visit_duration") return formatDuration(v) ?? "n/a";
  return new Intl.NumberFormat("en", { maximumFractionDigits: 0 }).format(v);
}

// a lower bounce rate is the better one; every other figure is better higher
export function higherIsBetter(m: WebMetric): boolean {
  return m !== "bounce_rate";
}

// the change against the previous period: relative for counts and
// durations, in points for the bounce rate. null when there is nothing to
// compare against.
export function webTrend(
  m: WebMetric,
  cur: number | null,
  prev: number | null,
  days: number,
): Trend | null {
  if (cur === null || prev === null) return null;
  const period = periodLabel(days);
  if (m === "bounce_rate") {
    const d = (cur - prev) * 100;
    if (Math.abs(d) < 0.05) return { text: "no change", direction: "flat", good: null, period };
    return {
      text: `${Math.abs(d).toFixed(1)} pt`,
      direction: d > 0 ? "up" : "down",
      good: d < 0,
      period,
    };
  }
  if (prev === 0) return null;
  const r = (cur - prev) / prev;
  if (Math.abs(r) < 0.0005) return { text: "no change", direction: "flat", good: null, period };
  const up = r > 0;
  return {
    text: `${Math.abs(r * 100).toFixed(1)}%`,
    direction: up ? "up" : "down",
    good: up === higherIsBetter(m),
    period,
  };
}

export const DIMENSION_LABEL: Record<WebDimension, string> = {
  page: "Pages",
  entry_page: "Entry pages",
  exit_page: "Exit pages",
  referrer: "Referrers",
  country: "Countries",
  browser: "Browsers",
  os: "Operating systems",
  device: "Devices",
  event: "Custom events",
  utm_source: "UTM source",
  utm_medium: "UTM medium",
  utm_campaign: "UTM campaign",
  utm_content: "UTM content",
  utm_term: "UTM term",
};

export const UTM_DIMENSIONS: readonly WebDimension[] = [
  "utm_source",
  "utm_medium",
  "utm_campaign",
  "utm_content",
  "utm_term",
];

let regions: Intl.DisplayNames | null = null;

// a country code as people read it; an unknown code stays as it is
export function countryName(code: string): string {
  try {
    regions ??= new Intl.DisplayNames(["en"], { type: "region" });
    const n = regions.of(code);
    return n && n !== code ? n : code;
  } catch {
    return code;
  }
}

const DEVICE_LABEL: Record<string, string> = {
  desktop: "Desktop",
  laptop: "Laptop",
  mobile: "Mobile",
  tablet: "Tablet",
};

export function keyLabel(d: WebDimension, key: string): string {
  if (d === "country") return countryName(key);
  if (d === "device") return DEVICE_LABEL[key] ?? key;
  return key;
}

// a value's share of the range's visitors, for the dimensions that count
// visitors. a visitor can carry two values of one dimension in a day (two
// referrers), so shares can add up to more than 100%.
export function shareOf(value: number, visitors: number): number | null {
  return visitors > 0 ? value / visitors : null;
}

// the dashboard of a project, or of one of its sites
export function webPath(tenantId: string, site?: string | null): string {
  return `/projects/${tenantId}/web${site ? `?site=${encodeURIComponent(site)}` : ""}`;
}
