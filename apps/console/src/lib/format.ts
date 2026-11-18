// every number, date and time the console shows goes through this module,
// so one value reads the same on every page. all times are utc: the control
// plane counts in utc hours and days, and labels say so instead of shifting
// buckets into the viewer's zone.

// a value that does not exist (not measured, too few visits, no data in the
// range). "0" is only ever a real zero. the reason goes in a tooltip next to
// the glyph (components/states.tsx `Missing`).
export const MISSING = "–";

// ------------------------------------------------------------------ counts

const integer = new Intl.NumberFormat("en", { maximumFractionDigits: 0 });
const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });

// a count in full with grouping: tables and stat values ("69,798")
export function formatCount(n: number): string {
  return integer.format(n);
}

// a count in short form ("45.9M"): only on chart axes and in tight chips,
// with the full count in a tooltip
export function formatCompact(n: number): string {
  return compact.format(n);
}

// a count with its noun, singular for one: "1 day", "3 days"
export function countOf(n: number, one: string, many = `${one}s`): string {
  return `${formatCount(n)} ${n === 1 ? one : many}`;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

// ---------------------------------------------------------------- percents

// a percentage number: one decimal below 10, none from 10 up
function percentNumber(pct: number): string {
  const a = Math.abs(pct);
  if (a < 9.95) return a.toFixed(1);
  return Math.round(a).toString();
}

// a share or a rate of 0 to 1: "0.9%", "5.0%", "44%", "100%", and "<0.1%"
// for a small share that is not zero
export function formatPercent(ratio: number): string {
  if (ratio === 0) return "0%";
  const pct = ratio * 100;
  const sign = pct < 0 ? "-" : "";
  if (Math.abs(pct) < 0.05) return `${sign}<0.1%`;
  return `${sign}${percentNumber(pct)}%`;
}

// a part of a whole as a percent; nothing to divide by gives the missing
// glyph
export function formatShareOf(part: number, all: number): string {
  return all > 0 ? formatPercent(part / all) : MISSING;
}

// a relative change against the previous period, signed: "+12%", "-0.9%"
export function formatChange(ratio: number): string {
  if (ratio === 0) return "0%";
  const pct = ratio * 100;
  const sign = pct > 0 ? "+" : "-";
  if (Math.abs(pct) < 0.05) return `${sign}<0.1%`;
  return `${sign}${percentNumber(pct)}%`;
}

// a difference between two rates of 0 to 1, in percentage points: "+1.2 pp"
export function formatPoints(diff: number): string {
  const pp = diff * 100;
  if (pp === 0) return "0 pp";
  const sign = pp > 0 ? "+" : "-";
  if (Math.abs(pp) < 0.05) return `${sign}<0.1 pp`;
  return `${sign}${percentNumber(pp)} pp`;
}

// a place in a cohort of 0 to 1, as the share of projects at or below it:
// "P90"
export function formatCohortPercentile(p: number): string {
  return `P${Math.round(p * 100)}`;
}

// ------------------------------------------------------- latency, duration

// the open latency bucket of the api: above 10 seconds, never a number
export const OPEN_BUCKET = ">10 s";

// a latency in seconds: "<1 ms", "31 ms", "1.25 s", ">10 s"
export function formatLatency(seconds: number | typeof OPEN_BUCKET): string {
  if (seconds === OPEN_BUCKET || seconds > 10) return OPEN_BUCKET;
  if (seconds === 0) return "0 ms";
  const ms = Math.round(seconds * 1000);
  if (ms < 1) return "<1 ms";
  if (ms < 1000) return `${ms} ms`;
  return `${trim(seconds.toFixed(2))} s`;
}

// a duration in seconds: "2 s", "1 min 5 s", "2 h 9 min", "3 d 4 h"
export function formatDuration(seconds: number): string {
  if (seconds === 0) return "0 s";
  if (seconds < 1) return "<1 s";
  const total = Math.round(seconds);
  if (total < 60) return `${total} s`;
  const m = Math.floor(total / 60);
  const s = total % 60;
  if (m < 60) return s ? `${m} min ${s} s` : `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}

function trim(v: string): string {
  return v.includes(".") ? v.replace(/\.?0+$/, "") : v;
}

// ----------------------------------------------------------- dates, times

const UTC = "UTC";
const dayFmt = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  year: "numeric",
  timeZone: UTC,
});
const monthDayFmt = new Intl.DateTimeFormat("en", {
  month: "short",
  day: "numeric",
  timeZone: UTC,
});
const monthFmt = new Intl.DateTimeFormat("en", { month: "short", year: "numeric", timeZone: UTC });
const clockFmt = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  hourCycle: "h23",
  timeZone: UTC,
});
const clockSecondsFmt = new Intl.DateTimeFormat("en", {
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hourCycle: "h23",
  timeZone: UTC,
});

// a day ("2026-11-18") or an instant ("2026-11-18T01:49:00Z")
function at(value: string | Date): Date {
  if (value instanceof Date) return value;
  return new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T00:00:00Z` : value);
}

// "Nov 18, 2026"
export function formatDate(value: string | Date): string {
  return dayFmt.format(at(value));
}

// "Oct 20 to Nov 18, 2026": the year shows once when both days share it,
// and a single day reads as that day
export function formatDateRange(from: string | Date, to: string | Date): string {
  const a = at(from);
  const b = at(to);
  if (utcDay(a) === utcDay(b)) return formatDate(b);
  if (a.getUTCFullYear() === b.getUTCFullYear())
    return `${monthDayFmt.format(a)} to ${formatDate(b)}`;
  return `${formatDate(a)} to ${formatDate(b)}`;
}

// "01:49 UTC", or "01:49:05 UTC" where seconds matter (a live check)
export function formatTimeUtc(value: string | Date, opts: { seconds?: boolean } = {}): string {
  return `${(opts.seconds ? clockSecondsFmt : clockFmt).format(at(value))} UTC`;
}

// "Nov 18, 2026, 01:49 UTC"
export function formatDateTimeUtc(value: string | Date): string {
  const d = at(value);
  return `${formatDate(d)}, ${clockFmt.format(d)} UTC`;
}

// a date and time where the width is tight (a table cell, a phone card):
// "Nov 18, 08:23 UTC" in the current utc year, the full form otherwise
export function formatDateTimeShort(value: string | Date, now: Date = new Date()): string {
  const d = at(value);
  if (d.getUTCFullYear() !== now.getUTCFullYear()) return formatDateTimeUtc(d);
  return `${monthDayFmt.format(d)}, ${clockFmt.format(d)} UTC`;
}

// an iso week by its first day, "Week of Oct 27", as the console names weeks
export function formatWeekOf(from: string | Date): string {
  return `Week of ${monthDayFmt.format(at(from))}`;
}

// two instants: "Nov 18, 2026, 14:00 to 15:00 UTC" on one day,
// "Nov 17, 2026, 14:00 to Nov 18, 2026, 15:00 UTC" across days
export function formatTimeRange(from: string | Date, to: string | Date): string {
  const a = at(from);
  const b = at(to);
  if (utcDay(a) === utcDay(b))
    return `${formatDate(a)}, ${clockFmt.format(a)} to ${clockFmt.format(b)} UTC`;
  return `${formatDate(a)}, ${clockFmt.format(a)} to ${formatDate(b)}, ${clockFmt.format(b)} UTC`;
}

// the iso week of a day: "2026-W46"
export function formatWeek(value: string | Date): string {
  const d = at(value);
  const t = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate()));
  t.setUTCDate(t.getUTCDate() + 4 - (t.getUTCDay() || 7));
  const yearStart = Date.UTC(t.getUTCFullYear(), 0, 1);
  const week = Math.ceil(((t.getTime() - yearStart) / 86_400_000 + 1) / 7);
  return `${t.getUTCFullYear()}-W${String(week).padStart(2, "0")}`;
}

// the freshness line under a page header: "Updated 01:49 UTC" today,
// "Updated Nov 17, 2026, 01:49 UTC" on an earlier day
export function formatUpdated(value: string | Date, now: Date = new Date()): string {
  const d = at(value);
  return utcDay(d) === utcDay(now)
    ? `Updated ${formatTimeUtc(d)}`
    : `Updated ${formatDateTimeUtc(d)}`;
}

export type TickUnit = "minute" | "hour" | "day" | "month";

// a short axis tick: "14:00" for minutes and hours, "Nov 18" for days,
// "Nov 2026" for months
export function formatTick(value: string | Date, unit: TickUnit): string {
  const d = at(value);
  if (unit === "minute" || unit === "hour") return clockFmt.format(d);
  if (unit === "day") return monthDayFmt.format(d);
  return monthFmt.format(d);
}

// the utc day of an instant, as the api names days: "2026-11-18"
export function utcDay(value: Date = new Date()): string {
  return value.toISOString().slice(0, 10);
}

// ------------------------------------------------------------------ text

// a long name or slug cut in the middle, so its start and its end (where
// stress-project-01 and stress-project-12 differ) both stay: "stress-pr…t-12"
export function truncateMiddle(text: string, max: number): string {
  if (text.length <= max) return text;
  const keep = max - 1;
  const head = Math.ceil(keep / 2);
  return `${text.slice(0, head)}…${text.slice(text.length - (keep - head))}`;
}

// ---------------------------------------------------------------- places

let regions: Intl.DisplayNames | null = null;

// a country code as people read it ("Germany"); a code intl does not know
// stays as it is
export function countryName(code: string): string {
  const c = code.toUpperCase();
  try {
    regions ??= new Intl.DisplayNames(["en"], { type: "region" });
    const n = regions.of(c);
    return n && n !== c ? n : code;
  } catch {
    return code;
  }
}

// ------------------------------------------------------------------- axes

// 3 to 5 round steps from zero up to at least the largest value; `integer`
// keeps the step whole, for counts
export function niceTicks(max: number, opts: { integer?: boolean } = {}): number[] {
  if (!(max > 0)) return [0, 1];
  const rough = max / 4;
  const mag = 10 ** Math.floor(Math.log10(rough));
  let step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= rough - 1e-9) ?? 10 * mag;
  if (opts.integer && step < 1) step = 1;
  const top = Math.ceil(max / step - 1e-9) * step;
  const out: number[] = [];
  for (let v = 0; v <= top + step / 2; v += step) out.push(Number(v.toPrecision(12)));
  return out;
}
