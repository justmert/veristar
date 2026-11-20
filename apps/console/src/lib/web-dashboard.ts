// the pure part of the web dashboard: its address state, the range it asks
// for, trends against the previous period and how values read. kept apart
// from the components so tests can check them directly. the rules mirror
// docs/reference/control-plane-api.md section 18.

import type { Ingest } from "@/api/schemas";
import type { WebBand, WebDimension, WebPoint, WebQuery, WebTotals } from "@/api/web";
import type { StatChange } from "@/components/stat-card";
import {
  countryName,
  formatChange,
  formatCount,
  formatDuration,
  formatPercent,
  formatPoints,
  MISSING,
} from "@/lib/format";
import { type DayRange, isDay, lastDays, validateDays } from "@/lib/global";

export type WebPreset = "today" | "7" | "30" | "90" | "custom";

// the presets of the range control; it adds "Custom" itself
export const WEB_PRESETS: readonly { value: Exclude<WebPreset, "custom">; label: string }[] = [
  { value: "today", label: "Today" },
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
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
  const preset: WebPreset =
    p === "custom" || WEB_PRESETS.some((x) => x.value === p) ? (p as WebPreset) : "30";
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

export function formatMetric(m: WebMetric, v: number | null): string {
  if (v === null) return MISSING;
  if (m === "bounce_rate") return formatPercent(v);
  if (m === "avg_visit_duration") return formatDuration(v);
  return formatCount(v);
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
): StatChange | null {
  if (cur === null || prev === null) return null;
  const period = periodLabel(days);
  if (m === "bounce_rate") {
    const d = cur - prev;
    if (Math.abs(d) < 0.0005) return { text: "no change", direction: "flat", good: null, period };
    return {
      text: formatPoints(d),
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
    text: formatChange(r),
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

// the heading of the key column of a list: one row is one of these
export const DIMENSION_COLUMN: Record<WebDimension, string> = {
  page: "Page",
  entry_page: "Entry page",
  exit_page: "Exit page",
  referrer: "Referrer",
  country: "Country",
  browser: "Browser",
  os: "Operating system",
  device: "Device",
  event: "Event",
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

const DEVICE_LABEL: Record<string, string> = {
  desktop: "Desktop",
  laptop: "Laptop",
  mobile: "Mobile",
  tablet: "Tablet",
};

// the analytics engine's browser keys as people know the browsers
const BROWSER_LABEL: Record<string, string> = {
  android: "Android browser",
  aol: "AOL",
  bb10: "BlackBerry",
  chrome: "Chrome",
  "chromium-webview": "Chrome WebView",
  crios: "Chrome on iOS",
  curl: "curl",
  edge: "Edge (legacy)",
  "edge-chromium": "Edge",
  "edge-ios": "Edge on iOS",
  facebook: "Facebook in-app",
  firefox: "Firefox",
  fxios: "Firefox on iOS",
  ie: "Internet Explorer",
  instagram: "Instagram in-app",
  ios: "Safari on iOS",
  "ios-webview": "iOS WebView",
  kakaotalk: "KakaoTalk",
  miui: "MIUI Browser",
  opera: "Opera",
  "opera-mini": "Opera Mini",
  safari: "Safari",
  samsung: "Samsung Internet",
  searchbot: "Search bot",
  silk: "Silk",
  yandexbrowser: "Yandex Browser",
};

export function keyLabel(d: WebDimension, key: string): string {
  if (d === "country") return countryName(key);
  if (d === "device") return DEVICE_LABEL[key] ?? key;
  if (d === "browser") return BROWSER_LABEL[key] ?? key;
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

// the values of a metric per day, for a stat card's sparkline. a day not
// read yet is a gap in the chart, but the sparkline only shows the shape, so
// it reads as zero there; null when there is no shape to show: fewer than
// three days with a value above zero (a flat line with one spike at the end
// reads as a glitch), or every day the same
export const SPARK_MIN_DAYS = 3;

export function sparkOf(series: WebPoint[], m: WebMetric): number[] | null {
  const v = series.map((p) => pointValue(p, m) ?? 0);
  if (v.filter((x) => x > 0).length < SPARK_MIN_DAYS) return null;
  return Math.max(...v) === Math.min(...v) ? null : v;
}

// the gateway's outcome counts come in whole utc hours, so a range of days
// reads from its first midnight to the end of its last day or the end of the
// running hour, whichever comes first. null when the range starts in the
// future.
export function ingestWindow(
  r: DayRange,
  now: Date,
): { from: string; to: string; bucket: "day" } | null {
  const hour = new Date(now);
  hour.setUTCMinutes(0, 0, 0);
  hour.setUTCHours(hour.getUTCHours() + 1);
  const from = Date.parse(`${r.from}T00:00:00Z`);
  const to = Math.min(Date.parse(`${r.to}T00:00:00Z`) + 86_400_000, hour.getTime());
  if (to <= from) return null;
  const iso = (t: number) => new Date(t).toISOString().replace(/\.\d{3}Z$/, "Z");
  return { from: iso(from), to: iso(to), bucket: "day" };
}

// the outcome totals of several sites added up
export function sumOutcomes(
  answers: (Pick<Ingest, "totals"> | undefined)[],
): Record<string, number> {
  const out: Record<string, number> = {};
  for (const a of answers) {
    if (!a) continue;
    for (const [k, v] of Object.entries(a.totals)) out[k] = (out[k] ?? 0) + v;
  }
  return out;
}
