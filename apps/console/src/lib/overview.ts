// the pure part of the project overview: its range and the api range of the
// same days. kept apart from the page so tests can check them directly. the
// next setup step comes from lib/site-setup.ts, as on the sites pages.

import type { ApiRangeQuery } from "@/api/endpoints";
import { utcDay } from "@/lib/format";
import { type DayRange, isDay, lastDays } from "@/lib/global";
import { WEB_PRESETS } from "@/lib/web-dashboard";

export type OverviewPreset = (typeof WEB_PRESETS)[number]["value"];

export function readOverviewPreset(v: string | null): OverviewPreset {
  return WEB_PRESETS.find((p) => p.value === v)?.value ?? "30";
}

export function overviewDays(p: OverviewPreset, now: Date): DayRange {
  return lastDays(p === "today" ? 1 : Number(p), now);
}

// the range in the address: a preset, or a custom pair of utc days
// (?range=custom&from=&to=), as on Web
export function readOverviewRange(
  params: URLSearchParams,
  now: Date,
): { preset: OverviewPreset | "custom"; days: DayRange } {
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  if (params.get("range") === "custom" && isDay(from) && isDay(to) && from <= to)
    return { preset: "custom", days: { from, to } };
  const preset = readOverviewPreset(params.get("range"));
  return { preset, days: overviewDays(preset, now) };
}

// the api numbers of exactly the web range's utc days: from the first
// midnight to the midnight after the last day
export function apiRangeOfDays(r: DayRange): ApiRangeQuery {
  const end = new Date(Date.parse(`${r.to}T00:00:00Z`) + 86_400_000);
  return { range: "custom", from: `${r.from}T00:00:00Z`, to: `${utcDay(end)}T00:00:00Z` };
}
