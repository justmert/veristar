import { describe, expect, it } from "vitest";
import {
  countOf,
  countryName,
  formatBytes,
  formatChange,
  formatCohortPercentile,
  formatCompact,
  formatCount,
  formatDate,
  formatDateRange,
  formatDateTimeShort,
  formatDateTimeUtc,
  formatDuration,
  formatLatency,
  formatPercent,
  formatPoints,
  formatShareOf,
  formatTick,
  formatTimeRange,
  formatTimeUtc,
  formatUpdated,
  formatWeek,
  MISSING,
  niceTicks,
  OPEN_BUCKET,
  truncateMiddle,
  utcDay,
} from "@/lib/format";

describe("counts", () => {
  it("groups full counts", () => {
    expect(formatCount(0)).toBe("0");
    expect(formatCount(69798)).toBe("69,798");
    expect(formatCount(12_345_678_901)).toBe("12,345,678,901");
    expect(formatCount(2.6)).toBe("3");
  });

  it("shortens counts for axes and chips", () => {
    expect(formatCompact(0)).toBe("0");
    expect(formatCompact(999)).toBe("999");
    expect(formatCompact(45_900_000)).toBe("45.9M");
    expect(formatCompact(1_200)).toBe("1.2K");
    expect(formatCompact(3_400_000_000_000)).toBe("3.4T");
  });

  it("names the noun in the singular for one", () => {
    expect(countOf(1, "project")).toBe("1 project");
    expect(countOf(0, "project")).toBe("0 projects");
    expect(countOf(1200, "day")).toBe("1,200 days");
    expect(countOf(2, "entry", "entries")).toBe("2 entries");
  });

  it("sizes bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(3 * 1024 * 1024)).toBe("3.0 MiB");
  });
});

describe("percents", () => {
  it("keeps one decimal below 10% and none from 10% up", () => {
    expect(formatPercent(0)).toBe("0%");
    expect(formatPercent(0.009)).toBe("0.9%");
    expect(formatPercent(0.05)).toBe("5.0%");
    expect(formatPercent(0.0996)).toBe("10%");
    expect(formatPercent(0.444)).toBe("44%");
    expect(formatPercent(1)).toBe("100%");
    expect(formatPercent(12.5)).toBe("1250%");
  });

  it("says <0.1% for a small share that is not zero", () => {
    expect(formatPercent(0.0004)).toBe("<0.1%");
    expect(formatPercent(0.000001)).toBe("<0.1%");
    expect(formatPercent(0.0005)).toBe("0.1%");
  });

  it("divides a part by its whole, and has no value without a whole", () => {
    expect(formatShareOf(1, 4)).toBe("25%");
    expect(formatShareOf(0, 4)).toBe("0%");
    expect(formatShareOf(3, 0)).toBe(MISSING);
  });

  it("signs relative changes", () => {
    expect(formatChange(0)).toBe("0%");
    expect(formatChange(0.2)).toBe("+20%");
    expect(formatChange(-0.009)).toBe("-0.9%");
    expect(formatChange(1.161)).toBe("+116%");
    expect(formatChange(0.0001)).toBe("+<0.1%");
    expect(formatChange(-0.0001)).toBe("-<0.1%");
  });

  it("writes rate differences in percentage points", () => {
    expect(formatPoints(0)).toBe("0 pp");
    expect(formatPoints(0.012)).toBe("+1.2 pp");
    expect(formatPoints(-0.001)).toBe("-0.1 pp");
    expect(formatPoints(0.2)).toBe("+20 pp");
    expect(formatPoints(0.0001)).toBe("+<0.1 pp");
  });

  it("names a place in the cohort", () => {
    expect(formatCohortPercentile(0)).toBe("P0");
    expect(formatCohortPercentile(0.904)).toBe("P90");
    expect(formatCohortPercentile(1)).toBe("P100");
  });
});

describe("latency and durations", () => {
  it("formats latency", () => {
    expect(formatLatency(0)).toBe("0 ms");
    expect(formatLatency(0.0004)).toBe("<1 ms");
    expect(formatLatency(0.031)).toBe("31 ms");
    expect(formatLatency(0.9996)).toBe("1 s");
    expect(formatLatency(1.25)).toBe("1.25 s");
    expect(formatLatency(10)).toBe("10 s");
    expect(formatLatency(10.5)).toBe(OPEN_BUCKET);
    expect(formatLatency(OPEN_BUCKET)).toBe(">10 s");
  });

  it("formats durations", () => {
    expect(formatDuration(0)).toBe("0 s");
    expect(formatDuration(0.4)).toBe("<1 s");
    expect(formatDuration(2)).toBe("2 s");
    expect(formatDuration(65)).toBe("1 min 5 s");
    expect(formatDuration(120)).toBe("2 min");
    expect(formatDuration(2 * 3600 + 9 * 60 + 20)).toBe("2 h 9 min");
    expect(formatDuration(3600)).toBe("1 h");
    expect(formatDuration(3 * 86400 + 4 * 3600)).toBe("3 d 4 h");
    expect(formatDuration(86400)).toBe("1 d");
  });
});

describe("dates and times", () => {
  it("formats a day and an instant the same way, in utc", () => {
    expect(formatDate("2026-11-18")).toBe("Nov 18, 2026");
    expect(formatDate("2026-11-18T23:59:00Z")).toBe("Nov 18, 2026");
    expect(formatDate("2026-11-18T23:59:00-05:00")).toBe("Nov 19, 2026");
  });

  it("writes ranges with the year once", () => {
    expect(formatDateRange("2026-10-20", "2026-11-18")).toBe("Oct 20 to Nov 18, 2026");
    expect(formatDateRange("2025-12-20", "2026-01-18")).toBe("Dec 20, 2025 to Jan 18, 2026");
    expect(formatDateRange("2026-11-18", "2026-11-18")).toBe("Nov 18, 2026");
  });

  it("always says utc for times", () => {
    expect(formatTimeUtc("2026-11-18T01:49:30Z")).toBe("01:49 UTC");
    expect(formatTimeUtc("2026-11-18T01:49:05Z", { seconds: true })).toBe("01:49:05 UTC");
    expect(formatDateTimeUtc("2026-11-18T01:49:00Z")).toBe("Nov 18, 2026, 01:49 UTC");
    const now = new Date("2026-12-01T00:00:00Z");
    expect(formatDateTimeShort("2026-11-18T08:23:00Z", now)).toBe("Nov 18, 08:23 UTC");
    expect(formatDateTimeShort("2025-11-18T08:23:00Z", now)).toBe("Nov 18, 2025, 08:23 UTC");
    expect(formatDateTimeUtc("2026-11-18T00:00:00Z")).toBe("Nov 18, 2026, 00:00 UTC");
    expect(formatTimeRange("2026-11-18T14:00:00Z", "2026-11-18T15:00:00Z")).toBe(
      "Nov 18, 2026, 14:00 to 15:00 UTC",
    );
    expect(formatTimeRange("2026-11-17T14:00:00Z", "2026-11-18T15:00:00Z")).toBe(
      "Nov 17, 2026, 14:00 to Nov 18, 2026, 15:00 UTC",
    );
  });

  it("names iso weeks", () => {
    expect(formatWeek("2026-11-18")).toBe("2026-W47");
    expect(formatWeek("2026-11-12")).toBe("2026-W46");
    expect(formatWeek("2027-01-01")).toBe("2026-W53");
    expect(formatWeek("2024-12-30")).toBe("2025-W01");
  });

  it("states freshness with the day only when it is not today", () => {
    const now = new Date("2026-11-18T12:00:00Z");
    expect(formatUpdated("2026-11-18T01:49:00Z", now)).toBe("Updated 01:49 UTC");
    expect(formatUpdated("2026-11-17T01:49:00Z", now)).toBe("Updated Nov 17, 2026, 01:49 UTC");
  });

  it("keeps axis ticks short", () => {
    expect(formatTick("2026-11-18T14:00:00Z", "hour")).toBe("14:00");
    expect(formatTick("2026-11-18", "day")).toBe("Nov 18");
    expect(formatTick("2026-11-01", "month")).toBe("Nov 2026");
  });

  it("names the utc day of an instant", () => {
    expect(utcDay(new Date("2026-11-18T23:30:00-02:00"))).toBe("2026-11-19");
  });
});

describe("places and axes", () => {
  it("names countries in english and keeps unknown codes", () => {
    expect(countryName("DE")).toBe("Germany");
    expect(countryName("de")).toBe("Germany");
    expect(countryName("XX")).toBe("XX");
  });

  it("chooses 3 to 5 round ticks from zero", () => {
    expect(niceTicks(0)).toEqual([0, 1]);
    expect(niceTicks(-3)).toEqual([0, 1]);
    expect(niceTicks(100)).toEqual([0, 25, 50, 75, 100]);
    expect(niceTicks(1137)).toEqual([0, 500, 1000, 1500]);
    expect(niceTicks(0.3)).toEqual([0, 0.1, 0.2, 0.3]);
    expect(niceTicks(3, { integer: true })).toEqual([0, 1, 2, 3]);
    expect(niceTicks(2e12).at(-1)).toBeGreaterThanOrEqual(2e12);
  });
});

describe("text", () => {
  it("cuts a long name in the middle", () => {
    expect(truncateMiddle("stress-project-12", 40)).toBe("stress-project-12");
    expect(truncateMiddle("stress-project-twelve-12", 12)).toBe("stress…ve-12");
  });
});
