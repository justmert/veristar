// the pure part of the project overview: its range, the api range of the
// same days and the one setup step to do next. kept apart from the page so
// tests can check them directly.

import type { ApiRangeQuery } from "@/api/endpoints";
import type { Ingest, Site } from "@/api/schemas";
import { utcDay } from "@/lib/format";
import { type DayRange, lastDays } from "@/lib/global";
import { sumRejected } from "@/lib/outcomes";
import { WEB_PRESETS } from "@/lib/web-dashboard";

export type OverviewPreset = (typeof WEB_PRESETS)[number]["value"];

export function readOverviewPreset(v: string | null): OverviewPreset {
  return WEB_PRESETS.find((p) => p.value === v)?.value ?? "30";
}

export function overviewDays(p: OverviewPreset, now: Date): DayRange {
  return lastDays(p === "today" ? 1 : Number(p), now);
}

// the api numbers of exactly the web range's utc days: from the first
// midnight to the midnight after the last day
export function apiRangeOfDays(r: DayRange): ApiRangeQuery {
  const end = new Date(Date.parse(`${r.to}T00:00:00Z`) + 86_400_000);
  return { range: "custom", from: `${r.from}T00:00:00Z`, to: `${utcDay(end)}T00:00:00Z` };
}

export type FirstEvent = "waiting" | "received" | "refused";

// a site's first event, from either source: the edge's outcome counts or
// the page views of the web totals. whichever sees a counted event first
// completes the step. undefined while neither answer is in.
export function siteFirstEvent(
  ingest: Pick<Ingest, "totals"> | undefined,
  pageviews: number | undefined,
): FirstEvent | undefined {
  if (ingest === undefined && pageviews === undefined) return undefined;
  const t = ingest?.totals ?? {};
  const counted = (t.accepted ?? 0) + (t.quarantined ?? 0) + (t.bot ?? 0);
  if (counted > 0 || (pageviews ?? 0) > 0) return "received";
  if (ingest === undefined) return undefined;
  return sumRejected(t) > 0 ? "refused" : "waiting";
}

export type SetupStep = {
  site: Site;
  step: "install" | "refused" | "verify";
  // of the three steps: site added, first event, domain verified
  done: number;
};

// the one setup step to do next, of the first active site that is not set
// up. a site in review or suspended waits for an operator, not for its
// project. null when every site is set up, or while an answer is missing.
export function nextSetupStep(
  sites: Site[],
  eventOf: (s: Site) => FirstEvent | undefined,
): SetupStep | null {
  for (const site of sites) {
    if (site.status !== "active" || site.in_review) continue;
    const event = eventOf(site);
    if (event === undefined) return null;
    const verified = site.verification === "verified";
    if (event === "received" && verified) continue;
    const done = 1 + (event === "received" ? 1 : 0) + (verified ? 1 : 0);
    const step = event === "waiting" ? "install" : event === "refused" ? "refused" : "verify";
    return { site, step, done };
  }
  return null;
}
